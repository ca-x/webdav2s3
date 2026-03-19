package webdav

import (
	"context"
	"os"
	"testing"

	"github.com/spf13/afero"
)

type spyFs struct {
	afero.Fs
	mkdirCalled    bool
	mkdirAllCalled bool
	renameCalled   bool
	lastOldName    string
	lastNewName    string
}

func newSpyFs() *spyFs {
	return &spyFs{Fs: afero.NewMemMapFs()}
}

func (s *spyFs) Mkdir(name string, perm os.FileMode) error {
	s.mkdirCalled = true
	return nil
}

func (s *spyFs) MkdirAll(path string, perm os.FileMode) error {
	s.mkdirAllCalled = true
	return nil
}

func (s *spyFs) Rename(oldname, newname string) error {
	s.renameCalled = true
	s.lastOldName = oldname
	s.lastNewName = newname
	return nil
}

func TestMkdirUsesMkdirNotMkdirAll(t *testing.T) {
	t.Parallel()

	spy := newSpyFs()
	adapter := &AferoFS{fs: spy}

	if err := adapter.Mkdir(context.Background(), "/parent/child", 0o755); err != nil {
		t.Fatalf("Mkdir() error: %v", err)
	}

	if !spy.mkdirCalled {
		t.Fatal("expected Mkdir to call underlying Mkdir")
	}
	if spy.mkdirAllCalled {
		t.Fatal("did not expect Mkdir to call underlying MkdirAll")
	}
}

func TestRenameDoesNotCreateParentAndCleansPath(t *testing.T) {
	t.Parallel()

	spy := newSpyFs()
	adapter := &AferoFS{fs: spy}

	if err := adapter.Rename(context.Background(), "a/../src.txt", "dst/../target.txt"); err != nil {
		t.Fatalf("Rename() error: %v", err)
	}

	if !spy.renameCalled {
		t.Fatal("expected Rename to call underlying Rename")
	}
	if spy.mkdirAllCalled {
		t.Fatal("did not expect Rename to call underlying MkdirAll")
	}
	if spy.lastOldName != "/src.txt" {
		t.Fatalf("unexpected old path: got %q want %q", spy.lastOldName, "/src.txt")
	}
	if spy.lastNewName != "/target.txt" {
		t.Fatalf("unexpected new path: got %q want %q", spy.lastNewName, "/target.txt")
	}
}
