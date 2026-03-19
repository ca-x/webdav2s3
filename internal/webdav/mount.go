// Package webdav provides multi-backend WebDAV routing using afero composition.
package webdav

import (
	"context"
	"fmt"
	"log"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/spf13/afero"

	"github.com/example/webdav-s3/ent"
	"github.com/example/webdav-s3/ent/s3backend"
	"github.com/example/webdav-s3/internal/s3client"
	s3fs "github.com/example/webdav-s3/pkg/s3fs"
)

// MountFs routes requests to different S3Fs backends based on path prefix.
// Supports:
//   - Single backend per path: /mounts/minio/file.txt -> minio backend
//   - Multiple backends per path (replication): write to all, read from primary/first
type MountFs struct {
	mu     sync.RWMutex
	mounts map[string][]*mountEntry // mount_path -> []backends (first is primary)
	db     *ent.Client
	pool   *s3client.Pool
}

type mountEntry struct {
	id         int
	fs         afero.Fs
	isPrimary  bool
	isReadonly bool
	name       string
}

// NewMountFs creates a new mount filesystem.
func NewMountFs(db *ent.Client, pool *s3client.Pool) *MountFs {
	return &MountFs{
		mounts: make(map[string][]*mountEntry),
		db:     db,
		pool:   pool,
	}
}

// LoadBackends loads all enabled backends from the database.
func (m *MountFs) LoadBackends(ctx context.Context) error {
	backends, err := m.db.S3Backend.Query().
		Where(s3backend.IsEnabledEQ(true)).
		Order(ent.Asc(s3backend.FieldMountPath), ent.Desc(s3backend.FieldIsPrimary)).
		All(ctx)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear existing mounts
	m.mounts = make(map[string][]*mountEntry)

	for _, backend := range backends {
		if err := m.mountBackend(ctx, backend); err != nil {
			log.Printf("Failed to mount backend %s: %v", backend.Name, err)
			continue
		}
	}

	return nil
}

// Mount adds a backend to the mount table.
func (m *MountFs) Mount(ctx context.Context, backend *ent.S3Backend) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mountBackend(ctx, backend)
}

func (m *MountFs) mountBackend(ctx context.Context, backend *ent.S3Backend) error {
	mountPath, err := normalizeMountPath(backend.MountPath)
	if err != nil {
		return fmt.Errorf("invalid mount_path %q: %w", backend.MountPath, err)
	}

	client, err := m.pool.Get(ctx, backend)
	if err != nil {
		return err
	}

	fs := afero.Fs(s3fs.New(client, backend.Bucket, backend.KeyPrefix))
	if backend.IsReadonly {
		fs = afero.NewReadOnlyFs(fs)
	}

	entry := &mountEntry{
		id:         backend.ID,
		fs:         fs,
		isPrimary:  backend.IsPrimary,
		isReadonly: backend.IsReadonly,
		name:       backend.Name,
	}

	// Insert into correct position (primary first)
	entries := m.mounts[mountPath]
	if backend.IsPrimary {
		entries = append([]*mountEntry{entry}, entries...)
	} else {
		entries = append(entries, entry)
	}
	m.mounts[mountPath] = entries

	return nil
}

// Unmount removes a backend from the mount table.
func (m *MountFs) Unmount(backendID int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for path, entries := range m.mounts {
		for i, e := range entries {
			if e.id == backendID {
				m.mounts[path] = append(entries[:i], entries[i+1:]...)
				if len(m.mounts[path]) == 0 {
					delete(m.mounts, path)
				}
				m.pool.Remove(backendID)
				return
			}
		}
	}
}

// Resolve returns the filesystem entries and remaining path for a given request path.
func (m *MountFs) Resolve(requestPath string) ([]*mountEntry, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	requestPath = normalizeRequestPath(requestPath)

	// Find longest matching mount path
	var bestMatch string
	var bestEntries []*mountEntry

	for mountPath, entries := range m.mounts {
		if pathMatchesMount(requestPath, mountPath) {
			if len(mountPath) > len(bestMatch) {
				bestMatch = mountPath
				bestEntries = entries
			}
		}
	}

	if bestEntries != nil {
		remainingPath := strings.TrimPrefix(requestPath, bestMatch)
		if remainingPath == "" {
			remainingPath = "/"
		} else if !strings.HasPrefix(remainingPath, "/") {
			remainingPath = "/" + remainingPath
		}
		return bestEntries, remainingPath
	}

	return nil, requestPath
}

// ListMounts returns all active mount paths with backend info.
func (m *MountFs) ListMounts() map[string][]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]string)
	for path, entries := range m.mounts {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.name
		}
		result[path] = names
	}
	return result
}

// ─────────────────────────────────────────────
// afero.Fs implementation
// ─────────────────────────────────────────────

func (m *MountFs) Name() string { return "MountFs" }

// Read operation: use primary/first backend
func (m *MountFs) Open(name string) (afero.File, error) {
	entries, p := m.Resolve(name)
	if len(entries) == 0 {
		return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrNotExist}
	}
	return entries[0].fs.Open(p)
}

func (m *MountFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	entries, p := m.Resolve(name)
	if len(entries) == 0 {
		return nil, &os.PathError{Op: "openfile", Path: name, Err: os.ErrNotExist}
	}

	// Write operation: replicate to all backends
	if flag&os.O_WRONLY != 0 || flag&os.O_RDWR != 0 || flag&os.O_CREATE != 0 {
		return m.openForWrite(entries, p, flag, perm)
	}

	return entries[0].fs.OpenFile(p, flag, perm)
}

func (m *MountFs) openForWrite(entries []*mountEntry, path string, flag int, perm os.FileMode) (afero.File, error) {
	if len(entries) == 1 {
		if entries[0].isReadonly {
			return nil, &os.PathError{Op: "openfile", Path: path, Err: os.ErrPermission}
		}
		return entries[0].fs.OpenFile(path, flag, perm)
	}

	// Multiple backends: create a replicating file wrapper
	writableEntries := make([]*mountEntry, 0)
	for _, e := range entries {
		if !e.isReadonly {
			writableEntries = append(writableEntries, e)
		}
	}

	if len(writableEntries) == 0 {
		return nil, &os.PathError{Op: "openfile", Path: path, Err: os.ErrPermission}
	}

	// For simplicity, open first and replicate on close
	// A more sophisticated approach would use io.MultiWriter
	f, err := writableEntries[0].fs.OpenFile(path, flag, perm)
	if err != nil {
		return nil, err
	}

	return &replicatingFile{
		File:    f,
		entries: writableEntries[1:],
		path:    path,
		flag:    flag,
		perm:    perm,
	}, nil
}

func (m *MountFs) Stat(name string) (os.FileInfo, error) {
	entries, p := m.Resolve(name)
	if len(entries) == 0 {
		return nil, &os.PathError{Op: "stat", Path: name, Err: os.ErrNotExist}
	}
	return entries[0].fs.Stat(p)
}

// Write operations: replicate to all backends

func (m *MountFs) Create(name string) (afero.File, error) {
	entries, p := m.Resolve(name)
	if len(entries) == 0 {
		return nil, &os.PathError{Op: "create", Path: name, Err: os.ErrNotExist}
	}
	return m.openForWrite(entries, p, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
}

func (m *MountFs) Mkdir(name string, perm os.FileMode) error {
	entries, p := m.Resolve(name)
	if len(entries) == 0 {
		return &os.PathError{Op: "mkdir", Path: name, Err: os.ErrNotExist}
	}

	var lastErr error
	for _, e := range entries {
		if !e.isReadonly {
			if err := e.fs.Mkdir(p, perm); err != nil {
				lastErr = err
			}
		}
	}
	return lastErr
}

func (m *MountFs) MkdirAll(name string, perm os.FileMode) error {
	entries, p := m.Resolve(name)
	if len(entries) == 0 {
		return &os.PathError{Op: "mkdirall", Path: name, Err: os.ErrNotExist}
	}

	var lastErr error
	for _, e := range entries {
		if !e.isReadonly {
			if err := e.fs.MkdirAll(p, perm); err != nil {
				lastErr = err
			}
		}
	}
	return lastErr
}

func (m *MountFs) Remove(name string) error {
	entries, p := m.Resolve(name)
	if len(entries) == 0 {
		return &os.PathError{Op: "remove", Path: name, Err: os.ErrNotExist}
	}

	var lastErr error
	for _, e := range entries {
		if !e.isReadonly {
			if err := e.fs.Remove(p); err != nil && !os.IsNotExist(err) {
				lastErr = err
			}
		}
	}
	return lastErr
}

func (m *MountFs) RemoveAll(name string) error {
	entries, p := m.Resolve(name)
	if len(entries) == 0 {
		return &os.PathError{Op: "removeall", Path: name, Err: os.ErrNotExist}
	}

	var lastErr error
	for _, e := range entries {
		if !e.isReadonly {
			if err := e.fs.RemoveAll(p); err != nil {
				lastErr = err
			}
		}
	}
	return lastErr
}

func (m *MountFs) Rename(oldname, newname string) error {
	oldEntries, oldPath := m.Resolve(oldname)
	newEntries, newPath := m.Resolve(newname)

	if len(oldEntries) == 0 {
		return &os.PathError{Op: "rename", Path: oldname, Err: os.ErrNotExist}
	}
	if len(newEntries) == 0 {
		return &os.PathError{Op: "rename", Path: newname, Err: os.ErrNotExist}
	}

	// Only support rename within same mount path
	if oldEntries[0].id != newEntries[0].id {
		return &os.PathError{Op: "rename", Path: oldname, Err: os.ErrInvalid}
	}

	return oldEntries[0].fs.Rename(oldPath, newPath)
}

func (m *MountFs) Chmod(name string, mode os.FileMode) error {
	entries, p := m.Resolve(name)
	if len(entries) == 0 {
		return &os.PathError{Op: "chmod", Path: name, Err: os.ErrNotExist}
	}
	return entries[0].fs.Chmod(p, mode)
}

func (m *MountFs) Chown(name string, uid, gid int) error {
	entries, p := m.Resolve(name)
	if len(entries) == 0 {
		return &os.PathError{Op: "chown", Path: name, Err: os.ErrNotExist}
	}
	return entries[0].fs.Chown(p, uid, gid)
}

func (m *MountFs) Chtimes(name string, atime, mtime time.Time) error {
	entries, p := m.Resolve(name)
	if len(entries) == 0 {
		return &os.PathError{Op: "chtimes", Path: name, Err: os.ErrNotExist}
	}
	return entries[0].fs.Chtimes(p, atime, mtime)
}

// ─────────────────────────────────────────────
// ReplicatingFile - wraps file and replicates writes
// ─────────────────────────────────────────────

type replicatingFile struct {
	afero.File
	entries []*mountEntry
	path    string
	flag    int
	perm    os.FileMode
	buffer  []byte
}

func (f *replicatingFile) Write(p []byte) (n int, err error) {
	f.buffer = append(f.buffer, p...)
	return f.File.Write(p)
}

func (f *replicatingFile) Close() error {
	if err := f.File.Close(); err != nil {
		return err
	}

	var replicaErrors []string

	// Replicate to other backends
	for _, e := range f.entries {
		if e.isReadonly {
			continue
		}
		// Open and write the same content
		otherFile, err := e.fs.OpenFile(f.path, f.flag, f.perm)
		if err != nil {
			replicaErrors = append(replicaErrors, fmt.Sprintf("%s(open): %v", e.name, err))
			log.Printf("Failed to replicate to %s: %v", e.name, err)
			continue
		}

		var replicaErr error
		if len(f.buffer) > 0 {
			if _, err := otherFile.Write(f.buffer); err != nil {
				replicaErr = err
				log.Printf("Failed to write replica to %s: %v", e.name, err)
			}
		}

		if closeErr := otherFile.Close(); closeErr != nil && replicaErr == nil {
			replicaErr = closeErr
			log.Printf("Failed to close replica file on %s: %v", e.name, closeErr)
		}

		if replicaErr != nil {
			replicaErrors = append(replicaErrors, fmt.Sprintf("%s(write): %v", e.name, replicaErr))
		}
	}

	if len(replicaErrors) > 0 {
		return fmt.Errorf("replication failed: %s", strings.Join(replicaErrors, "; "))
	}

	return nil
}

func normalizeMountPath(mountPath string) (string, error) {
	mountPath = strings.TrimSpace(mountPath)
	if mountPath == "" {
		return "", fmt.Errorf("mount_path is required")
	}
	if !strings.HasPrefix(mountPath, "/") {
		mountPath = "/" + mountPath
	}
	mountPath = path.Clean(mountPath)
	if mountPath == "." {
		mountPath = "/"
	}
	return mountPath, nil
}

func normalizeRequestPath(requestPath string) string {
	if requestPath == "" {
		return "/"
	}
	cleanPath := path.Clean("/" + requestPath)
	if cleanPath == "." {
		return "/"
	}
	return cleanPath
}

func pathMatchesMount(requestPath, mountPath string) bool {
	if mountPath == "/" {
		return strings.HasPrefix(requestPath, "/")
	}
	if requestPath == mountPath {
		return true
	}
	return strings.HasPrefix(requestPath, mountPath+"/")
}
