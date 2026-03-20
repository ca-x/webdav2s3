// Package s3fs implements the afero.Fs interface backed by Amazon S3.
//
// S3 is a flat key-value store, so we simulate a directory hierarchy by:
//   - treating keys ending in "/" as directory markers
//   - using ListObjectsV2 with delimiters to enumerate directory contents
//
// Each "directory" is represented by a zero-byte object with a trailing slash.
package s3fs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/spf13/afero"
)

var (
	ErrNotImplemented = errors.New("not implemented")
	ErrReadOnly       = errors.New("filesystem is read-only")
)

// S3Fs implements afero.Fs on top of an S3 bucket.
type S3Fs struct {
	client    *s3.Client
	bucket    string
	keyPrefix string // always ends with "/" if non-empty
}

// New creates a new S3Fs.
// keyPrefix is an optional key prefix applied to all operations (e.g. "webdav/").
func New(client *s3.Client, bucket, keyPrefix string) *S3Fs {
	if keyPrefix != "" && !strings.HasSuffix(keyPrefix, "/") {
		keyPrefix += "/"
	}
	return &S3Fs{client: client, bucket: bucket, keyPrefix: keyPrefix}
}

// s3Key converts an absolute FS path to an S3 key.
func (fs *S3Fs) s3Key(name string) string {
	name = path.Clean("/" + name)
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return fs.keyPrefix
	}
	return fs.keyPrefix + name
}

// s3DirKey returns the key for a directory marker.
func (fs *S3Fs) s3DirKey(name string) string {
	k := fs.s3Key(name)
	if k == "" || k == fs.keyPrefix {
		return fs.keyPrefix // root
	}
	return strings.TrimSuffix(k, "/") + "/"
}

// ─────────────────────────────────────────────
// afero.Fs implementation
// ─────────────────────────────────────────────

func (fs *S3Fs) Name() string { return "S3Fs" }

// Create creates or truncates a file for writing.
func (fs *S3Fs) Create(name string) (afero.File, error) {
	key := fs.s3Key(name)
	// Ensure parent directory exists
	if err := fs.MkdirAll(path.Dir(name), 0755); err != nil {
		return nil, err
	}
	return newS3File(fs, name, key, false), nil
}

// Mkdir creates a directory marker in S3.
func (fs *S3Fs) Mkdir(name string, perm os.FileMode) error {
	ctx := context.Background()
	key := fs.s3DirKey(name)
	if key == fs.keyPrefix {
		return nil // root always exists
	}
	_, err := fs.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(fs.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader([]byte{}),
		ContentType: aws.String("application/x-directory"),
	})
	return mapS3Error(err)
}

// MkdirAll creates the directory and all parent directories.
func (fs *S3Fs) MkdirAll(path string, perm os.FileMode) error {
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	cur := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		cur += "/" + p
		if err := fs.Mkdir(cur, perm); err != nil {
			return err
		}
	}
	return nil
}

// Open opens a file or directory for reading.
func (fs *S3Fs) Open(name string) (afero.File, error) {
	key := fs.s3Key(name)
	dirKey := fs.s3DirKey(name)
	ctx := context.Background()

	// Check if it's a directory
	if dirKey == fs.keyPrefix {
		// root
		return newS3Dir(fs, name, dirKey), nil
	}

	// Check directory marker
	_, errDir := fs.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(fs.bucket),
		Key:    aws.String(dirKey),
	})
	if errDir == nil {
		return newS3Dir(fs, name, dirKey), nil
	}

	// Try as regular file
	out, err := fs.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(fs.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, mapS3Error(err)
	}
	return newS3FileFromGetOutput(fs, name, key, out), nil
}

// OpenFile opens a file with the given flags.
func (fs *S3Fs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	writeMode := flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_APPEND|os.O_TRUNC) != 0
	if !writeMode {
		return fs.Open(name)
	}

	fi, statErr := fs.Stat(name)
	exists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}

	if !exists && flag&os.O_CREATE == 0 {
		return nil, &os.PathError{Op: "openfile", Path: name, Err: os.ErrNotExist}
	}
	if exists && flag&os.O_CREATE != 0 && flag&os.O_EXCL != 0 {
		return nil, &os.PathError{Op: "openfile", Path: name, Err: os.ErrExist}
	}
	if exists && fi.IsDir() {
		return nil, &os.PathError{Op: "openfile", Path: name, Err: os.ErrInvalid}
	}

	key := fs.s3Key(name)
	if !exists {
		if err := fs.MkdirAll(path.Dir(name), 0755); err != nil {
			return nil, err
		}
	}

	f := newS3File(fs, name, key, false)
	if exists && flag&os.O_APPEND != 0 && flag&os.O_TRUNC == 0 {
		if err := fs.loadFileToBuffer(name, f); err != nil {
			return nil, err
		}
	}

	return f, nil
}

func (fs *S3Fs) loadFileToBuffer(name string, f *s3File) error {
	ctx := context.Background()
	key := fs.s3Key(name)
	out, err := fs.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(fs.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return mapS3Error(err)
	}
	defer out.Body.Close()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return err
	}
	f.buf.Reset()
	_, _ = f.buf.Write(data)
	return nil
}

// Remove deletes a file or empty directory.
func (fs *S3Fs) Remove(name string) error {
	ctx := context.Background()
	key := fs.s3Key(name)
	dirKey := fs.s3DirKey(name)

	// Try directory first
	if dirKey != fs.keyPrefix {
		// Check if directory is empty
		resp, err := fs.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:    aws.String(fs.bucket),
			Prefix:    aws.String(dirKey),
			MaxKeys:   aws.Int32(2),
			Delimiter: aws.String("/"),
		})
		if err == nil && (len(resp.Contents) > 1 || len(resp.CommonPrefixes) > 0) {
			return &os.PathError{Op: "remove", Path: name, Err: errors.New("directory not empty")}
		}
		// Delete directory marker
		fs.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(fs.bucket),
			Key:    aws.String(dirKey),
		})
	}

	// Delete as regular file
	_, err := fs.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(fs.bucket),
		Key:    aws.String(key),
	})
	return mapS3Error(err)
}

// RemoveAll removes a path and all children.
func (fs *S3Fs) RemoveAll(path string) error {
	ctx := context.Background()
	prefix := fs.s3DirKey(path)
	if prefix == fs.keyPrefix {
		prefix = fs.keyPrefix
	}

	var contToken *string
	for {
		resp, err := fs.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(fs.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: contToken,
		})
		if err != nil {
			return mapS3Error(err)
		}
		for _, obj := range resp.Contents {
			if _, err := fs.client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(fs.bucket),
				Key:    obj.Key,
			}); err != nil {
				return mapS3Error(err)
			}
		}
		if !*resp.IsTruncated {
			break
		}
		contToken = resp.NextContinuationToken
	}

	// Also delete the file itself if not a dir path
	fileKey := fs.s3Key(path)
	fs.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(fs.bucket),
		Key:    aws.String(fileKey),
	})
	return nil
}

// Rename moves a file or directory.
func (fs *S3Fs) Rename(oldname, newname string) error {
	ctx := context.Background()
	oldInfo, err := fs.Stat(oldname)
	if err != nil {
		return err
	}

	// Directory rename in S3 requires prefix-copy semantics.
	if oldInfo.IsDir() {
		return fs.renameDir(ctx, oldname, newname)
	}

	oldKey := fs.s3Key(oldname)
	newKey := fs.s3Key(newname)

	// Copy then delete
	_, err = fs.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(fs.bucket),
		CopySource: aws.String(copySource(fs.bucket, oldKey)),
		Key:        aws.String(newKey),
	})
	if err != nil {
		return mapS3Error(err)
	}
	_, err = fs.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(fs.bucket),
		Key:    aws.String(oldKey),
	})
	return mapS3Error(err)
}

func (fs *S3Fs) renameDir(ctx context.Context, oldname, newname string) error {
	oldPrefix := fs.s3DirKey(oldname)
	newPrefix := fs.s3DirKey(newname)
	if oldPrefix == fs.keyPrefix {
		return &os.PathError{Op: "rename", Path: oldname, Err: os.ErrInvalid}
	}

	var contToken *string
	for {
		resp, err := fs.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(fs.bucket),
			Prefix:            aws.String(oldPrefix),
			ContinuationToken: contToken,
		})
		if err != nil {
			return mapS3Error(err)
		}

		for _, obj := range resp.Contents {
			if obj.Key == nil {
				continue
			}

			oldObjectKey := *obj.Key
			suffix := strings.TrimPrefix(oldObjectKey, oldPrefix)
			newObjectKey := newPrefix + suffix

			if _, err := fs.client.CopyObject(ctx, &s3.CopyObjectInput{
				Bucket:     aws.String(fs.bucket),
				CopySource: aws.String(copySource(fs.bucket, oldObjectKey)),
				Key:        aws.String(newObjectKey),
			}); err != nil {
				return mapS3Error(err)
			}
		}

		for _, obj := range resp.Contents {
			if obj.Key == nil {
				continue
			}
			if _, err := fs.client.DeleteObject(ctx, &s3.DeleteObjectInput{
				Bucket: aws.String(fs.bucket),
				Key:    obj.Key,
			}); err != nil {
				return mapS3Error(err)
			}
		}

		if !*resp.IsTruncated {
			break
		}
		contToken = resp.NextContinuationToken
	}
	return nil
}

// Stat returns file info.
func (fs *S3Fs) Stat(name string) (os.FileInfo, error) {
	ctx := context.Background()
	key := fs.s3Key(name)
	dirKey := fs.s3DirKey(name)

	// Root
	if dirKey == fs.keyPrefix {
		return &s3FileInfo{name: "/", size: 0, isDir: true, modTime: time.Now()}, nil
	}

	// Check directory marker
	headDir, errDir := fs.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(fs.bucket),
		Key:    aws.String(dirKey),
	})
	if errDir == nil {
		mod := time.Now()
		if headDir.LastModified != nil {
			mod = *headDir.LastModified
		}
		return &s3FileInfo{
			name:    path.Base(name),
			size:    0,
			isDir:   true,
			modTime: mod,
		}, nil
	}

	// Check as regular file
	head, err := fs.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(fs.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		// Check if prefix exists (implicit directory)
		resp, listErr := fs.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:  aws.String(fs.bucket),
			Prefix:  aws.String(dirKey),
			MaxKeys: aws.Int32(1),
		})
		if listErr == nil && len(resp.Contents) > 0 {
			return &s3FileInfo{name: path.Base(name), size: 0, isDir: true, modTime: time.Now()}, nil
		}
		return nil, &os.PathError{Op: "stat", Path: name, Err: os.ErrNotExist}
	}

	mod := time.Now()
	if head.LastModified != nil {
		mod = *head.LastModified
	}
	var size int64
	if head.ContentLength != nil {
		size = *head.ContentLength
	}
	return &s3FileInfo{
		name:    path.Base(name),
		size:    size,
		isDir:   false,
		modTime: mod,
	}, nil
}

// Chmod / Chown / Chtimes are no-ops for S3.
func (fs *S3Fs) Chmod(name string, mode os.FileMode) error         { return nil }
func (fs *S3Fs) Chown(name string, uid, gid int) error             { return nil }
func (fs *S3Fs) Chtimes(name string, atime, mtime time.Time) error { return nil }

// ─────────────────────────────────────────────
// ListObjects helper (used by s3File.Readdir)
// ─────────────────────────────────────────────

func (fs *S3Fs) listDir(dirPath string) ([]os.FileInfo, error) {
	ctx := context.Background()
	prefix := fs.s3DirKey(dirPath)

	var infos []os.FileInfo
	var contToken *string

	for {
		resp, err := fs.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(fs.bucket),
			Prefix:            aws.String(prefix),
			Delimiter:         aws.String("/"),
			ContinuationToken: contToken,
		})
		if err != nil {
			return nil, mapS3Error(err)
		}

		// Sub-directories (common prefixes)
		for _, cp := range resp.CommonPrefixes {
			if cp.Prefix == nil {
				continue
			}
			p := strings.TrimSuffix(*cp.Prefix, "/")
			p = strings.TrimPrefix(p, fs.keyPrefix)
			infos = append(infos, &s3FileInfo{
				name:    path.Base(p),
				size:    0,
				isDir:   true,
				modTime: time.Now(),
			})
		}

		// Files
		for _, obj := range resp.Contents {
			if obj.Key == nil {
				continue
			}
			// Skip the directory marker itself
			if *obj.Key == prefix {
				continue
			}
			name := strings.TrimPrefix(*obj.Key, prefix)
			if name == "" || strings.Contains(name, "/") {
				continue
			}
			mod := time.Now()
			if obj.LastModified != nil {
				mod = *obj.LastModified
			}
			size := int64(0)
			if obj.Size != nil {
				size = *obj.Size
			}
			infos = append(infos, &s3FileInfo{
				name:    name,
				size:    size,
				isDir:   false,
				modTime: mod,
			})
		}

		if !*resp.IsTruncated {
			break
		}
		contToken = resp.NextContinuationToken
	}

	return infos, nil
}

// ─────────────────────────────────────────────
// Error mapping
// ─────────────────────────────────────────────

func mapS3Error(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := errors.AsType[*types.NoSuchKey](err); ok {
		return os.ErrNotExist
	}
	if _, ok := errors.AsType[*types.NoSuchBucket](err); ok {
		return os.ErrNotExist
	}
	return err
}

func copySource(bucket, key string) string {
	escaped := url.PathEscape(bucket + "/" + key)
	return strings.ReplaceAll(escaped, "%2F", "/")
}
