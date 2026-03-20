package s3fs

import (
	"bytes"
	"os"
	"testing"
	"time"
)

func TestS3FileInfoName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "simple name", input: "/file.txt", expected: "file.txt"},
		{name: "nested path", input: "/path/to/file.txt", expected: "file.txt"},
		{name: "root", input: "/", expected: "/"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fi := &s3FileInfo{name: tt.input, size: 100, isDir: false, modTime: time.Now()}
			if got := fi.Name(); got != tt.expected {
				t.Errorf("Name() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestS3FileInfoSize(t *testing.T) {
	t.Parallel()

	fi := &s3FileInfo{name: "test", size: 12345, isDir: false, modTime: time.Now()}
	if got := fi.Size(); got != 12345 {
		t.Errorf("Size() = %d, want 12345", got)
	}
}

func TestS3FileInfoMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		isDir  bool
		isMask os.FileMode
	}{
		{name: "file mode", isDir: false, isMask: os.ModePerm},
		{name: "directory mode", isDir: true, isMask: os.ModeDir},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fi := &s3FileInfo{name: "test", size: 0, isDir: tt.isDir, modTime: time.Now()}
			mode := fi.Mode()
			if tt.isDir {
				if mode&os.ModeDir == 0 {
					t.Errorf("expected ModeDir bit set")
				}
			}
			if mode.IsDir() != tt.isDir {
				t.Errorf("IsDir() = %v, want %v", mode.IsDir(), tt.isDir)
			}
		})
	}
}

func TestS3FileInfoModTime(t *testing.T) {
	t.Parallel()

	now := time.Now()
	fi := &s3FileInfo{name: "test", size: 0, isDir: false, modTime: now}
	if got := fi.ModTime(); !got.Equal(now) {
		t.Errorf("ModTime() = %v, want %v", got, now)
	}
}

func TestS3FileInfoIsDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		isDir bool
	}{
		{name: "is directory", isDir: true},
		{name: "is file", isDir: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fi := &s3FileInfo{name: "test", size: 0, isDir: tt.isDir, modTime: time.Now()}
			if got := fi.IsDir(); got != tt.isDir {
				t.Errorf("IsDir() = %v, want %v", got, tt.isDir)
			}
		})
	}
}

func TestS3FileInfoSys(t *testing.T) {
	t.Parallel()

	fi := &s3FileInfo{name: "test", size: 0, isDir: false, modTime: time.Now()}
	if got := fi.Sys(); got != nil {
		t.Errorf("Sys() = %v, want nil", got)
	}
}

func TestS3FileRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		bufData string
		pLen    int
		wantN   int
		wantErr bool
	}{
		{name: "read from buffer", bufData: "hello world", pLen: 5, wantN: 5, wantErr: false},
		{name: "read all", bufData: "hi", pLen: 10, wantN: 2, wantErr: false},
		{name: "empty buffer", bufData: "", pLen: 5, wantN: 0, wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := &s3File{buf: bytes.NewBufferString(tt.bufData)}
			p := make([]byte, tt.pLen)
			n, err := f.Read(p)

			if (err != nil) != tt.wantErr {
				t.Errorf("Read() error = %v, wantErr %v", err, tt.wantErr)
			}
			if n != tt.wantN {
				t.Errorf("Read() n = %d, want %d", n, tt.wantN)
			}
		})
	}
}

func TestS3FileReadWithReader(t *testing.T) {
	t.Parallel()

	f := &s3File{reader: nil, buf: bytes.NewBufferString("hello")}
	p := make([]byte, 3)
	n, err := f.Read(p)

	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if n != 3 {
		t.Errorf("Read() n = %d, want 3", n)
	}
	if string(p) != "hel" {
		t.Errorf("Read() = %q, want %q", string(p), "hel")
	}
}

func TestS3FileWrite(t *testing.T) {
	t.Parallel()

	f := &s3File{}
	n, err := f.Write([]byte("hello"))

	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 5 {
		t.Errorf("Write() n = %d, want 5", n)
	}
	if f.buf == nil {
		t.Error("Write() buf should not be nil after write")
	}
	if f.buf.Len() != 5 {
		t.Errorf("buf.Len() = %d, want 5", f.buf.Len())
	}
}

func TestS3FileWriteNilBuf(t *testing.T) {
	t.Parallel()

	f := &s3File{buf: nil}
	n, err := f.Write([]byte("test"))

	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if n != 4 {
		t.Errorf("Write() n = %d, want 4", n)
	}
}

func TestS3FileTruncate(t *testing.T) {
	t.Parallel()

	f := &s3File{buf: bytes.NewBufferString("hello world")}
	err := f.Truncate(0)

	if err != nil {
		t.Fatalf("Truncate() error = %v", err)
	}
	if f.buf.Len() != 0 {
		t.Errorf("buf.Len() = %d, want 0", f.buf.Len())
	}
}

func TestS3FileTruncateNilBuf(t *testing.T) {
	t.Parallel()

	f := &s3File{buf: nil}
	err := f.Truncate(0)

	if err != nil {
		t.Fatalf("Truncate() error = %v", err)
	}
}

func TestS3FileCloseEmpty(t *testing.T) {
	t.Parallel()

	f := &s3File{buf: nil, reader: nil}
	err := f.Close()

	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestS3FileCloseWithReader(t *testing.T) {
	t.Parallel()

	// Test close when reader is set (reading mode)
	f := &s3File{reader: nil}
	err := f.Close()
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestS3FileReadAt(t *testing.T) {
	t.Parallel()

	f := &s3File{buf: bytes.NewBufferString("hello")}
	p := make([]byte, 3)
	_, err := f.ReadAt(p, 0)

	if err != ErrNotImplemented {
		t.Errorf("ReadAt() error = %v, want %v", err, ErrNotImplemented)
	}
}

func TestS3FileWriteAt(t *testing.T) {
	t.Parallel()

	f := &s3File{}
	_, err := f.WriteAt([]byte("test"), 0)

	if err != ErrNotImplemented {
		t.Errorf("WriteAt() error = %v, want %v", err, ErrNotImplemented)
	}
}

func TestS3FileSeek(t *testing.T) {
	t.Parallel()

	f := &s3File{}
	_, err := f.Seek(0, 0)

	if err != ErrNotImplemented {
		t.Errorf("Seek() error = %v, want %v", err, ErrNotImplemented)
	}
}

func TestS3FileSync(t *testing.T) {
	t.Parallel()

	f := &s3File{}
	err := f.Sync()

	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
}

func TestS3FileWriteString(t *testing.T) {
	t.Parallel()

	f := &s3File{}
	n, err := f.WriteString("hello")

	if err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	if n != 5 {
		t.Errorf("WriteString() n = %d, want 5", n)
	}
}
