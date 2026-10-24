package vfs

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"time"
)

// Kind identifies a backend.
type Kind int

// Backend kinds.
const (
	KindLocal Kind = iota
	KindSSH
	KindArchive
	KindSearch
)

// ErrReadOnly is returned by backends that cannot perform a write operation.
var ErrReadOnly = errors.New("filesystem is read-only")

// Entry describes one directory entry. IsDir is resolved through symlinks, so a link
// to a directory can be entered; IsLink and Link describe the link itself.
type Entry struct {
	Name    string
	Path    string
	Size    int64
	Disk    int64 // allocated bytes when known, else Size
	Mode    fs.FileMode
	ModTime time.Time
	IsDir   bool
	IsLink  bool
	Link    string
}

// IsExec reports whether any execute bit is set on a regular file.
func (e Entry) IsExec() bool {
	return !e.IsDir && e.Mode&0o111 != 0
}

// Caps describes what a backend supports.
type Caps struct {
	Write   bool
	Rename  bool
	Symlink bool
}

// FS is a filesystem rooted at "/" with slash-separated absolute paths.
// Implementations must be safe for concurrent use.
type FS interface {
	// ID uniquely identifies the filesystem instance, e.g. "local" or "ssh:pi@nas:22".
	ID() string
	Kind() Kind
	// Label is a short human description of the root, shown in panel headers.
	Label() string
	Caps() Caps

	ReadDir(ctx context.Context, dir string) ([]Entry, error)
	Stat(ctx context.Context, p string) (Entry, error)
	Open(ctx context.Context, p string) (io.ReadCloser, error)
	Create(ctx context.Context, p string, perm fs.FileMode) (io.WriteCloser, error)
	MkdirAll(ctx context.Context, p string, perm fs.FileMode) error
	// Remove deletes a file, a symlink or an empty directory.
	Remove(ctx context.Context, p string) error
	Rename(ctx context.Context, from, to string) error
	Chtimes(ctx context.Context, p string, mtime time.Time) error
	Chmod(ctx context.Context, p string, mode fs.FileMode) error
	Symlink(ctx context.Context, target, p string) error

	Close() error
}

// Committer is implemented by filesystems that buffer writes (archives) and must
// persist them once a batch of operations is complete.
type Committer interface {
	Commit(ctx context.Context) error
	Dirty() bool
}

// Localizer is implemented by filesystems whose files exist on the local disk.
type Localizer interface {
	LocalPath(p string) (string, bool)
}

// Details is ownership and inode information beyond Entry. Zero values mean unknown.
type Details struct {
	Owner  string // user name, or the numeric id when it cannot be resolved
	Group  string
	Inode  uint64
	Links  uint64
	Access time.Time
	Change time.Time // inode change time
}

// Detailer is implemented by filesystems that can report Details.
type Detailer interface {
	Details(ctx context.Context, p string) (Details, error)
}

// Trasher is implemented by filesystems that can move files to a trash can.
type Trasher interface {
	Trash(ctx context.Context, p string) error
}

// Opener constructs a filesystem from a file on another filesystem (archives).
type Opener func(ctx context.Context, outer FS, p string) (FS, error)

// Same reports whether a and b are the same filesystem instance.
func Same(a, b FS) bool {
	return a != nil && b != nil && a.ID() == b.ID()
}
