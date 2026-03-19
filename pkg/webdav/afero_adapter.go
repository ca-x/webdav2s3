// Package webdav provides the glue between golang.org/x/net/webdav and afero.Fs.
package webdav

import (
	"context"
	"os"
	"path"

	"github.com/spf13/afero"
	xwebdav "golang.org/x/net/webdav"
)

// AferoFS adapts any afero.Fs to the webdav.FileSystem interface.
type AferoFS struct {
	fs afero.Fs
}

// NewAferoFS wraps an afero.Fs for use as a webdav.FileSystem.
func NewAferoFS(fs afero.Fs) xwebdav.FileSystem {
	return &AferoFS{fs: fs}
}

func (a *AferoFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	return a.fs.MkdirAll(name, perm)
}

func (a *AferoFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (xwebdav.File, error) {
	// Normalize path
	name = path.Clean("/" + name)

	// Check if it's a directory first
	fi, err := a.fs.Stat(name)
	if err == nil && fi.IsDir() {
		f, err := a.fs.Open(name)
		if err != nil {
			return nil, err
		}
		return f, nil
	}

	// Delegate to afero based on flags
	if flag&os.O_CREATE != 0 || flag&os.O_WRONLY != 0 || flag&os.O_RDWR != 0 {
		return a.fs.OpenFile(name, flag, perm)
	}
	return a.fs.Open(name)
}

func (a *AferoFS) RemoveAll(ctx context.Context, name string) error {
	return a.fs.RemoveAll(name)
}

func (a *AferoFS) Rename(ctx context.Context, oldName, newName string) error {
	// Ensure destination parent directory exists
	if err := a.fs.MkdirAll(path.Dir(newName), 0755); err != nil {
		return err
	}
	return a.fs.Rename(oldName, newName)
}

func (a *AferoFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	return a.fs.Stat(path.Clean("/" + name))
}
