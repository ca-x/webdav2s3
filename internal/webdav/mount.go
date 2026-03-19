// Package webdav provides multi-backend WebDAV routing using afero composition.
package webdav

import (
	"context"
	"os"
	"sort"
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
// Path /mounts/minio/file.txt routes to the "minio" backend.
type MountFs struct {
	mu     sync.RWMutex
	mounts map[string]afero.Fs // mount_path -> filesystem
	db     *ent.Client
	pool   *s3client.Pool
}

// NewMountFs creates a new mount filesystem.
func NewMountFs(db *ent.Client, pool *s3client.Pool) *MountFs {
	return &MountFs{
		mounts: make(map[string]afero.Fs),
		db:     db,
		pool:   pool,
	}
}

// LoadBackends loads all enabled backends from the database.
func (m *MountFs) LoadBackends(ctx context.Context) error {
	backends, err := m.db.S3Backend.Query().
		Where(s3backend.IsEnabledEQ(true)).
		All(ctx)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear existing mounts
	m.mounts = make(map[string]afero.Fs)

	for _, backend := range backends {
		if err := m.mountBackend(ctx, backend); err != nil {
			// Log error but continue loading other backends
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
	client, err := m.pool.Get(ctx, backend)
	if err != nil {
		return err
	}

	fs := afero.Fs(s3fs.New(client, backend.Bucket, backend.KeyPrefix))
	if backend.IsReadonly {
		fs = afero.NewReadOnlyFs(fs)
	}

	m.mounts[backend.MountPath] = fs
	return nil
}

// Unmount removes a backend from the mount table.
func (m *MountFs) Unmount(mountPath string) {
	m.mu.Lock()
	delete(m.mounts, mountPath)
	m.mu.Unlock()
}

// Resolve returns the filesystem and remaining path for a given request path.
// It finds the longest matching mount prefix.
func (m *MountFs) Resolve(requestPath string) (afero.Fs, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Find longest matching mount path
	var bestMatch string
	var bestFs afero.Fs

	for mountPath, fs := range m.mounts {
		if strings.HasPrefix(requestPath, mountPath) {
			if len(mountPath) > len(bestMatch) {
				bestMatch = mountPath
				bestFs = fs
			}
		}
	}

	if bestFs != nil {
		remainingPath := strings.TrimPrefix(requestPath, bestMatch)
		if remainingPath == "" {
			remainingPath = "/"
		}
		return bestFs, remainingPath
	}

	return nil, requestPath
}

// ListMounts returns all active mount paths.
func (m *MountFs) ListMounts() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	paths := make([]string, 0, len(m.mounts))
	for p := range m.mounts {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// ─────────────────────────────────────────────
// afero.Fs implementation (delegates to resolved fs)
// ─────────────────────────────────────────────

func (m *MountFs) Name() string { return "MountFs" }

func (m *MountFs) Create(name string) (afero.File, error) {
	fs, p := m.Resolve(name)
	if fs == nil {
		return nil, &os.PathError{Op: "create", Path: name, Err: os.ErrNotExist}
	}
	return fs.Create(p)
}

func (m *MountFs) Mkdir(name string, perm os.FileMode) error {
	fs, p := m.Resolve(name)
	if fs == nil {
		return &os.PathError{Op: "mkdir", Path: name, Err: os.ErrNotExist}
	}
	return fs.Mkdir(p, perm)
}

func (m *MountFs) MkdirAll(name string, perm os.FileMode) error {
	fs, p := m.Resolve(name)
	if fs == nil {
		return &os.PathError{Op: "mkdirall", Path: name, Err: os.ErrNotExist}
	}
	return fs.MkdirAll(p, perm)
}

func (m *MountFs) Open(name string) (afero.File, error) {
	fs, p := m.Resolve(name)
	if fs == nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrNotExist}
	}
	return fs.Open(p)
}

func (m *MountFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	fs, p := m.Resolve(name)
	if fs == nil {
		return nil, &os.PathError{Op: "openfile", Path: name, Err: os.ErrNotExist}
	}
	return fs.OpenFile(p, flag, perm)
}

func (m *MountFs) Remove(name string) error {
	fs, p := m.Resolve(name)
	if fs == nil {
		return &os.PathError{Op: "remove", Path: name, Err: os.ErrNotExist}
	}
	return fs.Remove(p)
}

func (m *MountFs) RemoveAll(name string) error {
	fs, p := m.Resolve(name)
	if fs == nil {
		return &os.PathError{Op: "removeall", Path: name, Err: os.ErrNotExist}
	}
	return fs.RemoveAll(p)
}

func (m *MountFs) Rename(oldname, newname string) error {
	oldFs, oldPath := m.Resolve(oldname)
	newFs, newPath := m.Resolve(newname)

	if oldFs == nil {
		return &os.PathError{Op: "rename", Path: oldname, Err: os.ErrNotExist}
	}
	if newFs == nil {
		return &os.PathError{Op: "rename", Path: newname, Err: os.ErrNotExist}
	}

	// Cross-filesystem rename not supported
	if oldFs != newFs {
		return &os.PathError{Op: "rename", Path: oldname, Err: os.ErrInvalid}
	}

	return oldFs.Rename(oldPath, newPath)
}

func (m *MountFs) Stat(name string) (os.FileInfo, error) {
	fs, p := m.Resolve(name)
	if fs == nil {
		return nil, &os.PathError{Op: "stat", Path: name, Err: os.ErrNotExist}
	}
	return fs.Stat(p)
}

func (m *MountFs) Chmod(name string, mode os.FileMode) error {
	fs, p := m.Resolve(name)
	if fs == nil {
		return &os.PathError{Op: "chmod", Path: name, Err: os.ErrNotExist}
	}
	return fs.Chmod(p, mode)
}

func (m *MountFs) Chown(name string, uid, gid int) error {
	fs, p := m.Resolve(name)
	if fs == nil {
		return &os.PathError{Op: "chown", Path: name, Err: os.ErrNotExist}
	}
	return fs.Chown(p, uid, gid)
}

func (m *MountFs) Chtimes(name string, atime, mtime time.Time) error {
	fs, p := m.Resolve(name)
	if fs == nil {
		return &os.PathError{Op: "chtimes", Path: name, Err: os.ErrNotExist}
	}
	return fs.Chtimes(p, atime, mtime)
}