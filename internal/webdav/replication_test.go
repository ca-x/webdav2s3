package webdav

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

type openFileFailFs struct {
	afero.Fs
}

func (f openFileFailFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	return nil, errors.New("replica open failure")
}

func TestReplicatingFileCloseReturnsReplicaError(t *testing.T) {
	t.Parallel()

	primaryFs := afero.NewMemMapFs()
	primaryFile, err := primaryFs.OpenFile("/file.txt", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("open primary file: %v", err)
	}

	rf := &replicatingFile{
		File: primaryFile,
		entries: []*mountEntry{
			{
				name: "replica-1",
				fs:   openFileFailFs{Fs: afero.NewMemMapFs()},
			},
		},
		path:   "/file.txt",
		flag:   os.O_CREATE | os.O_WRONLY | os.O_TRUNC,
		perm:   0o644,
		buffer: []byte("hello"),
	}

	if _, err := rf.Write([]byte(" world")); err != nil {
		t.Fatalf("write to primary: %v", err)
	}

	err = rf.Close()
	if err == nil {
		t.Fatal("expected Close to return replication error")
	}
	if !strings.Contains(err.Error(), "replication failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
