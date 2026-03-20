package s3fs

import (
	"bytes"
	"context"
	"io"
	"os"
	"path"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ─────────────────────────────────────────────
// s3FileInfo
// ─────────────────────────────────────────────

type s3FileInfo struct {
	name    string
	size    int64
	isDir   bool
	modTime time.Time
}

func (fi *s3FileInfo) Name() string { return path.Base(fi.name) }
func (fi *s3FileInfo) Size() int64 {
	return fi.size
}
func (fi *s3FileInfo) Mode() os.FileMode {
	if fi.isDir {
		return os.ModeDir | 0755
	}
	return 0644
}
func (fi *s3FileInfo) ModTime() time.Time { return fi.modTime }
func (fi *s3FileInfo) IsDir() bool        { return fi.isDir }
func (fi *s3FileInfo) Sys() any   { return nil }

// ─────────────────────────────────────────────
// s3File – writable (buffers body, uploads on Close)
// ─────────────────────────────────────────────

type s3File struct {
	fs       *S3Fs
	name     string
	key      string
	buf      *bytes.Buffer
	reader   io.ReadCloser // set when opened for reading
	fileInfo *s3FileInfo
	dirMode  bool
	dirList  []os.FileInfo
	dirIdx   int
}

// newS3File creates a write-mode file (buffered upload).
func newS3File(fs *S3Fs, name, key string, dirMode bool) *s3File {
	return &s3File{
		fs:      fs,
		name:    name,
		key:     key,
		buf:     &bytes.Buffer{},
		dirMode: dirMode,
	}
}

// newS3Dir creates a directory pseudo-file for listing.
func newS3Dir(fs *S3Fs, name, key string) *s3File {
	return &s3File{
		fs:      fs,
		name:    name,
		key:     key,
		dirMode: true,
	}
}

// newS3FileFromGetOutput wraps an open GetObject response.
func newS3FileFromGetOutput(fs *S3Fs, name, key string, out *s3.GetObjectOutput) *s3File {
	mod := time.Now()
	if out.LastModified != nil {
		mod = *out.LastModified
	}
	var size int64
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	return &s3File{
		fs:     fs,
		name:   name,
		key:    key,
		reader: out.Body,
		fileInfo: &s3FileInfo{
			name:    path.Base(name),
			size:    size,
			isDir:   false,
			modTime: mod,
		},
	}
}

// ── afero.File interface ──

func (f *s3File) Close() error {
	if f.reader != nil {
		return f.reader.Close()
	}
	// Write mode: upload buffer to S3
	if f.buf != nil && f.buf.Len() > 0 {
		ctx := context.Background()
		data := f.buf.Bytes()
		size := int64(len(data))
		_, err := f.fs.client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:        aws.String(f.fs.bucket),
			Key:           aws.String(f.key),
			Body:          bytes.NewReader(data),
			ContentLength: aws.Int64(size),
		})
		return mapS3Error(err)
	}
	// Empty file write (e.g. touch)
	if f.buf != nil {
		ctx := context.Background()
		_, err := f.fs.client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:        aws.String(f.fs.bucket),
			Key:           aws.String(f.key),
			Body:          bytes.NewReader([]byte{}),
			ContentLength: aws.Int64(0),
		})
		return mapS3Error(err)
	}
	return nil
}

func (f *s3File) Read(p []byte) (int, error) {
	if f.reader != nil {
		return f.reader.Read(p)
	}
	if f.buf != nil {
		return f.buf.Read(p)
	}
	return 0, io.EOF
}

func (f *s3File) ReadAt(p []byte, off int64) (int, error) {
	if f.reader != nil {
		return 0, ErrNotImplemented
	}
	return 0, ErrNotImplemented
}

func (f *s3File) Seek(offset int64, whence int) (int64, error) {
	return 0, ErrNotImplemented
}

func (f *s3File) Write(p []byte) (int, error) {
	if f.buf == nil {
		f.buf = &bytes.Buffer{}
	}
	return f.buf.Write(p)
}

func (f *s3File) WriteAt(p []byte, off int64) (int, error) {
	return 0, ErrNotImplemented
}

func (f *s3File) WriteString(s string) (int, error) {
	return f.Write([]byte(s))
}

func (f *s3File) Truncate(size int64) error {
	if f.buf != nil {
		f.buf.Reset()
	}
	return nil
}

func (f *s3File) Sync() error { return nil }

func (f *s3File) Name() string { return f.name }

func (f *s3File) Stat() (os.FileInfo, error) {
	if f.fileInfo != nil {
		return f.fileInfo, nil
	}
	return f.fs.Stat(f.name)
}

func (f *s3File) Readdir(count int) ([]os.FileInfo, error) {
	if !f.dirMode {
		return nil, &os.PathError{Op: "readdir", Path: f.name, Err: os.ErrInvalid}
	}
	if f.dirList == nil {
		list, err := f.fs.listDir(f.name)
		if err != nil {
			return nil, err
		}
		f.dirList = list
		f.dirIdx = 0
	}
	if count <= 0 {
		result := f.dirList[f.dirIdx:]
		f.dirIdx = len(f.dirList)
		return result, nil
	}
	if f.dirIdx >= len(f.dirList) {
		return nil, io.EOF
	}
	end := f.dirIdx + count
	if end > len(f.dirList) {
		end = len(f.dirList)
	}
	result := f.dirList[f.dirIdx:end]
	f.dirIdx = end
	return result, nil
}

func (f *s3File) Readdirnames(n int) ([]string, error) {
	infos, err := f.Readdir(n)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(infos))
	for i, fi := range infos {
		names[i] = fi.Name()
	}
	return names, nil
}
