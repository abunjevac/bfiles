package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"

	"github.com/abunjevac/bfiles/internal/vfs"
)

// parallelStatThreshold is the directory size above which entries are stat'ed by
// several goroutines; below it the overhead outweighs the gain.
const parallelStatThreshold = 2048

// FS is the local filesystem.
type FS struct{}

var (
	_ vfs.FS         = (*FS)(nil)
	_ vfs.Localizer  = (*FS)(nil)
	_ vfs.Trasher    = (*FS)(nil)
	_ vfs.Identifier = (*FS)(nil)
)

// New returns the local filesystem.
func New() *FS {
	return &FS{}
}

// ID implements vfs.FS.
func (*FS) ID() string { return "local" }

// Kind implements vfs.FS.
func (*FS) Kind() vfs.Kind { return vfs.KindLocal }

// Label implements vfs.FS.
func (*FS) Label() string { return "" }

// Caps implements vfs.FS.
func (*FS) Caps() vfs.Caps { return vfs.Caps{Write: true, Rename: true, Symlink: true} }

// LocalPath implements vfs.Localizer.
func (*FS) LocalPath(p string) (string, bool) { return p, true }

// Close implements vfs.FS.
func (*FS) Close() error { return nil }

// ReadDir lists dir; entries are stat'ed concurrently for large directories.
func (*FS) ReadDir(ctx context.Context, dir string) ([]vfs.Entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	out := make([]vfs.Entry, len(des))

	statOne := func(i int) {
		p := filepath.Join(dir, des[i].Name())

		e, err := stat(p)
		if err != nil {
			// the entry vanished or is unreadable; keep it visible with what we know
			e = vfs.Entry{Name: des[i].Name(), Path: p, IsDir: des[i].IsDir(), Mode: des[i].Type()}
		}

		out[i] = e
	}

	if len(des) < parallelStatThreshold {
		for i := range des {
			statOne(i)
		}

		return out, ctx.Err()
	}

	workers := runtime.GOMAXPROCS(0)
	chunk := (len(des) + workers - 1) / workers

	var wg sync.WaitGroup

	for start := 0; start < len(des); start += chunk {
		end := min(start+chunk, len(des))

		wg.Go(func() {
			for i := start; i < end; i++ {
				if ctx.Err() != nil {
					return
				}

				statOne(i)
			}
		})
	}

	wg.Wait()

	return out, ctx.Err()
}

// Stat returns entry info for p, resolving symlinks for IsDir and Size.
func (*FS) Stat(_ context.Context, p string) (vfs.Entry, error) {
	return stat(p)
}

func stat(p string) (vfs.Entry, error) {
	li, err := os.Lstat(p)
	if err != nil {
		return vfs.Entry{}, err
	}

	e := vfs.Entry{
		Name:    filepath.Base(p),
		Path:    p,
		Size:    li.Size(),
		Mode:    li.Mode(),
		ModTime: li.ModTime(),
		IsDir:   li.IsDir(),
	}

	e.Disk = diskUsage(li, e.Size)

	if li.Mode()&fs.ModeSymlink != 0 {
		e.IsLink = true
		e.Link, _ = os.Readlink(p)

		if ti, err := os.Stat(p); err == nil {
			e.IsDir = ti.IsDir()
			e.Size = ti.Size()
			e.Mode = ti.Mode()
		}
	}

	return e, nil
}

func diskUsage(fi fs.FileInfo, size int64) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * 512
	}

	return size
}

// Open implements vfs.FS.
func (*FS) Open(_ context.Context, p string) (io.ReadCloser, error) {
	return os.Open(p)
}

// Create implements vfs.FS; an existing file is truncated.
func (*FS) Create(_ context.Context, p string, perm fs.FileMode) (io.WriteCloser, error) {
	return os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm.Perm())
}

// MkdirAll implements vfs.FS.
func (*FS) MkdirAll(_ context.Context, p string, perm fs.FileMode) error {
	return os.MkdirAll(p, perm.Perm())
}

// Remove implements vfs.FS.
func (*FS) Remove(_ context.Context, p string) error {
	return os.Remove(p)
}

// Rename implements vfs.FS.
func (*FS) Rename(_ context.Context, from, to string) error {
	return os.Rename(from, to)
}

// Chtimes implements vfs.FS.
func (*FS) Chtimes(_ context.Context, p string, mtime time.Time) error {
	return os.Chtimes(p, mtime, mtime)
}

// Chmod implements vfs.FS.
func (*FS) Chmod(_ context.Context, p string, mode fs.FileMode) error {
	return os.Chmod(p, mode)
}

// Symlink implements vfs.FS.
func (*FS) Symlink(_ context.Context, target, p string) error {
	return os.Symlink(target, p)
}

// RealPath implements vfs.Identifier.
func (*FS) RealPath(_ context.Context, p string) (string, error) {
	return realPath(filepath.Clean(p))
}

// SameFile implements vfs.Identifier by device and inode, so it also sees through
// bind mounts and hard links.
func (*FS) SameFile(_ context.Context, a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}

	bi, err := os.Stat(b)
	if err != nil {
		return false
	}

	return os.SameFile(ai, bi)
}

// realPath resolves symlinks in p; a missing tail is appended to its resolved parent.
func realPath(p string) (string, error) {
	r, err := filepath.EvalSymlinks(p)
	if err == nil {
		return r, nil
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	dir := filepath.Dir(p)
	if dir == p {
		return p, nil
	}

	rd, err := realPath(dir)
	if err != nil {
		return "", err
	}

	return filepath.Join(rd, filepath.Base(p)), nil
}

// Readlink returns the target of the symlink at p.
func (*FS) Readlink(_ context.Context, p string) (string, error) {
	return os.Readlink(p)
}

// Trash moves p to the freedesktop trash ($XDG_DATA_HOME/Trash) of the home volume.
func (*FS) Trash(_ context.Context, p string) error {
	trash, err := trashDir()
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}

	abs, err := filepath.Abs(p)
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}

	base := filepath.Base(abs)
	name := base

	for i := 1; ; i++ {
		info := filepath.Join(trash, "info", name+".trashinfo")
		dst := filepath.Join(trash, "files", name)

		// a file without info (e.g. left by another tool) must not be overwritten
		if _, err := os.Lstat(dst); err == nil {
			name = fmt.Sprintf("%s.%d", base, i)

			continue
		}

		f, err := os.OpenFile(info, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if os.IsExist(err) {
			name = fmt.Sprintf("%s.%d", base, i)

			continue
		}

		if err != nil {
			return fmt.Errorf("trash: %w", err)
		}

		if err := moveToTrash(f, info, abs, dst); err != nil {
			return fmt.Errorf("trash: %w", err)
		}

		return nil
	}
}

// trashDir returns the trash directory, creating its files and info parts.
func trashDir() (string, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")

	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}

		dataHome = filepath.Join(home, ".local", "share")
	}

	trash := filepath.Join(dataHome, "Trash")

	if err := os.MkdirAll(filepath.Join(trash, "files"), 0o700); err != nil { //nolint:gosec // trash dir is the user's own
		return "", err
	}

	if err := os.MkdirAll(filepath.Join(trash, "info"), 0o700); err != nil { //nolint:gosec // trash dir is the user's own
		return "", err
	}

	return trash, nil
}

// moveToTrash writes the trash info into f and moves abs to dst; on failure the
// info file is removed again.
func moveToTrash(f *os.File, info, abs, dst string) error {
	_, err := fmt.Fprintf(f, "[Trash Info]\nPath=%s\nDeletionDate=%s\n",
		escapeTrashPath(abs), time.Now().Format("2006-01-02T15:04:05"))

	cerr := f.Close()

	if err == nil {
		err = cerr
	}

	if err == nil {
		err = os.Rename(abs, dst)
	}

	if err != nil {
		_ = os.Remove(info)

		return err
	}

	return nil
}

// escapeTrashPath percent-encodes a path as the trash spec requires, keeping '/'.
func escapeTrashPath(p string) string {
	const hex = "0123456789ABCDEF"

	out := make([]byte, 0, len(p))

	for i := range len(p) {
		c := p[i]

		if c == '/' || isUnreserved(c) {
			out = append(out, c)

			continue
		}

		out = append(out, '%', hex[c>>4], hex[c&15])
	}

	return string(out)
}

// isUnreserved reports whether c is an RFC 3986 unreserved character.
func isUnreserved(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	default:
		return c == '-' || c == '_' || c == '.' || c == '~'
	}
}
