package archive

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/mholt/archives"

	"github.com/abunjevac/bfiles/internal/vfs"
)

// FS is an archive opened as a filesystem.
type FS struct {
	outer     vfs.FS
	outerPath string
	tmpDir    string

	mu     sync.RWMutex
	src    string // local file holding the current archive bytes ("" for a new archive)
	format archives.Format
	writer archives.ArchiverAsync // nil when the format is read-only
	root   *node
	dirty  bool
}

var (
	_ vfs.FS        = (*FS)(nil)
	_ vfs.Committer = (*FS)(nil)
)

// Open opens the archive at p on outer. Non-local archives are first downloaded to a
// private temp directory, because zip and 7z need random access.
func Open(ctx context.Context, outer vfs.FS, p string) (*FS, error) {
	a, err := newFS(outer, p)
	if err != nil {
		return nil, err
	}

	if lp, ok := localPath(outer, p); ok {
		a.src = lp
	} else {
		cached, err := download(ctx, outer, p, a.tmpDir)
		if err != nil {
			_ = a.Close()

			return nil, err
		}

		a.src = cached
	}

	if err := a.index(ctx); err != nil {
		_ = a.Close()

		return nil, err
	}

	return a, nil
}

// NewEmpty prepares a new, not yet existing archive at p; the format follows the file
// name. The archive file is written by the first Commit.
func NewEmpty(ctx context.Context, outer vfs.FS, p string) (*FS, error) {
	format, _, err := archives.Identify(ctx, path.Base(p), nil)
	if err != nil {
		return nil, fmt.Errorf("archive %s: unsupported format", path.Base(p))
	}

	w := writerFor(format)
	if w == nil {
		return nil, fmt.Errorf("archive %s: format is read-only", path.Base(p))
	}

	a, err := newFS(outer, p)
	if err != nil {
		return nil, err
	}

	a.format = format
	a.writer = w
	a.root = newDir("", time.Now())
	a.dirty = true

	return a, nil
}

func newFS(outer vfs.FS, p string) (*FS, error) {
	tmp, err := os.MkdirTemp("", "bfiles-archive-*")
	if err != nil {
		return nil, fmt.Errorf("archive-tempdir: %w", err)
	}

	return &FS{outer: outer, outerPath: p, tmpDir: tmp}, nil
}

func localPath(outer vfs.FS, p string) (string, bool) {
	if l, ok := outer.(vfs.Localizer); ok {
		return l.LocalPath(p)
	}

	return "", false
}

func download(ctx context.Context, outer vfs.FS, p, dir string) (string, error) {
	r, err := outer.Open(ctx, p)
	if err != nil {
		return "", fmt.Errorf("archive-download: %w", err)
	}

	defer r.Close()

	f, err := os.CreateTemp(dir, "source-*")
	if err != nil {
		return "", fmt.Errorf("archive-download: %w", err)
	}

	_, err = io.Copy(f, ctxReader{ctx, r})

	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		return "", fmt.Errorf("archive-download: %w", err)
	}

	return f.Name(), nil
}

// addEntry inserts one archive member into the tree below root.
func addEntry(root *node, fi archives.FileInfo) {
	name := strings.Trim(path.Clean("/"+fi.NameInArchive), "/")
	if name == "" {
		return
	}

	parent, err := root.mkdirAll(path.Dir("/"+name), fi.ModTime())
	if err != nil {
		// a file shadowing a dir path is skipped, not fatal
		return
	}

	base := path.Base(name)

	if fi.IsDir() {
		if existing, ok := parent.children[base]; ok && existing.isDir {
			existing.mtime = fi.ModTime()
			existing.mode = fi.Mode()

			return
		}

		d := newDir(base, fi.ModTime())
		d.mode = fi.Mode() | fs.ModeDir
		parent.children[base] = d

		return
	}

	n := &node{name: base, size: fi.Size(), mode: fi.Mode(), mtime: fi.ModTime(), orig: fi.NameInArchive}

	// hard links have a target too, but carry their own data and are kept as files
	if fi.Mode()&fs.ModeSymlink != 0 {
		n.link = fi.LinkTarget
	}

	parent.children[base] = n
}

// writerFor returns a configured writer for formats bfiles can rebuild, or nil.
func writerFor(format archives.Format) archives.ArchiverAsync {
	switch f := format.(type) {
	case archives.Zip:
		return archives.Zip{Compression: zip.Deflate, SelectiveCompression: true}
	case archives.Tar:
		return f
	case archives.CompressedArchive:
		if _, ok := f.Archival.(archives.Tar); ok && f.Compression != nil {
			return f
		}
	}

	return nil
}

// ID implements vfs.FS.
func (a *FS) ID() string { return "archive:" + a.outer.ID() + ":" + a.outerPath }

// Kind implements vfs.FS.
func (*FS) Kind() vfs.Kind { return vfs.KindArchive }

// Label implements vfs.FS.
func (a *FS) Label() string {
	if l := a.outer.Label(); l != "" {
		return l + ":" + a.outerPath
	}

	return a.outerPath
}

// Outer returns the filesystem and path of the archive file itself.
func (a *FS) Outer() (vfs.FS, string) { return a.outer, a.outerPath }

// Caps implements vfs.FS.
func (a *FS) Caps() vfs.Caps {
	a.mu.RLock()
	defer a.mu.RUnlock()

	w := a.writer != nil

	return vfs.Caps{Write: w, Rename: w, Symlink: w}
}

// Dirty implements vfs.Committer.
func (a *FS) Dirty() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.dirty
}

// Close removes the overlay and any downloaded copy.
func (a *FS) Close() error {
	return os.RemoveAll(a.tmpDir)
}

// ReadDir implements vfs.FS.
func (a *FS) ReadDir(_ context.Context, dir string) ([]vfs.Entry, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	n, ok := a.root.lookup(dir)
	if !ok {
		return nil, fs.ErrNotExist
	}

	if !n.isDir {
		return nil, fmt.Errorf("%s: not a directory", dir)
	}

	out := make([]vfs.Entry, 0, len(n.children))

	for name, c := range n.children {
		out = append(out, c.entry(path.Join(dir, name)))
	}

	return out, nil
}

// Stat implements vfs.FS.
func (a *FS) Stat(_ context.Context, p string) (vfs.Entry, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	n, ok := a.root.lookup(p)
	if !ok {
		return vfs.Entry{}, fs.ErrNotExist
	}

	e := n.entry(path.Clean("/" + p))

	if e.Path == "/" {
		e.Name = "/"
	}

	return e, nil
}

// Open implements vfs.FS. Content from the source archive is streamed by a
// goroutine through a pipe; closing the reader stops it.
func (a *FS) Open(ctx context.Context, p string) (io.ReadCloser, error) {
	a.mu.RLock()
	n, ok := a.root.lookup(p)
	src, format := a.src, a.format
	a.mu.RUnlock()

	switch {
	case !ok:
		return nil, fs.ErrNotExist
	case n.isDir:
		return nil, fmt.Errorf("%s: is a directory", p)
	case n.overlay != "":
		return os.Open(n.overlay)
	case n.orig == "":
		return io.NopCloser(strings.NewReader(n.link)), nil
	}

	return extractOne(ctx, src, format, n.orig)
}

func extractOne(ctx context.Context, src string, format archives.Format, name string) (io.ReadCloser, error) {
	ex, ok := format.(archives.Extractor)
	if !ok {
		return nil, fmt.Errorf("archive cannot be read")
	}

	f, err := os.Open(src)
	if err != nil {
		return nil, err
	}

	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		defer f.Close()
		defer cancel()

		found := false

		err := ex.Extract(ctx, f, func(_ context.Context, fi archives.FileInfo) error {
			if fi.NameInArchive != name {
				return nil
			}

			found = true

			r, err := fi.Open()
			if err != nil {
				return err
			}

			defer r.Close()

			if _, err := io.Copy(pw, r); err != nil {
				return err
			}

			return fs.SkipAll
		})

		if err == nil && !found {
			err = fs.ErrNotExist
		}

		pw.CloseWithError(err)
	}()

	return readCloser{Reader: pr, close: func() error {
		cancel()

		return pr.Close()
	}}, nil
}

type readCloser struct {
	io.Reader

	close func() error
}

func (r readCloser) Close() error { return r.close() }

type ctxReader struct {
	ctx context.Context //nolint:containedctx // scoped to a single copy
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}

	return c.r.Read(p)
}

// Create implements vfs.FS. Data goes to an overlay file; the node is updated when
// the writer is closed.
func (a *FS) Create(_ context.Context, p string, perm fs.FileMode) (io.WriteCloser, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.writable(); err != nil {
		return nil, err
	}

	parent, base, err := a.parentOf(p)
	if err != nil {
		return nil, err
	}

	if existing, ok := parent.children[base]; ok && existing.isDir {
		return nil, fmt.Errorf("%s: is a directory", p)
	}

	f, err := os.CreateTemp(a.tmpDir, "overlay-*")
	if err != nil {
		return nil, err
	}

	n := &node{name: base, mode: perm.Perm(), mtime: time.Now(), overlay: f.Name()}

	parent.children[base] = n
	a.dirty = true

	return &overlayWriter{File: f, fs: a, n: n}, nil
}

type overlayWriter struct {
	*os.File

	fs *FS
	n  *node
}

func (w *overlayWriter) Close() error {
	fi, statErr := w.Stat()
	err := w.File.Close()

	w.fs.mu.Lock()
	defer w.fs.mu.Unlock()

	if statErr == nil {
		w.n.size = fi.Size()
	}

	return err
}

// MkdirAll implements vfs.FS.
func (a *FS) MkdirAll(_ context.Context, p string, _ fs.FileMode) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.writable(); err != nil {
		return err
	}

	if _, ok := a.root.lookup(p); ok {
		return nil
	}

	if _, err := a.root.mkdirAll(p, time.Now()); err != nil {
		return err
	}

	a.dirty = true

	return nil
}

// Remove implements vfs.FS.
func (a *FS) Remove(_ context.Context, p string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.writable(); err != nil {
		return err
	}

	parent, base, err := a.parentOf(p)
	if err != nil {
		return err
	}

	n, ok := parent.children[base]
	if !ok {
		return fs.ErrNotExist
	}

	if n.isDir && len(n.children) > 0 {
		return fmt.Errorf("%s: directory not empty", p)
	}

	if n.overlay != "" {
		_ = os.Remove(n.overlay)
	}

	delete(parent.children, base)

	a.dirty = true

	return nil
}

// Rename implements vfs.FS.
func (a *FS) Rename(_ context.Context, from, to string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.writable(); err != nil {
		return err
	}

	if vfs.IsWithin(to, from) {
		return fmt.Errorf("cannot move %s into itself", from)
	}

	fromParent, fromBase, err := a.parentOf(from)
	if err != nil {
		return err
	}

	n, ok := fromParent.children[fromBase]
	if !ok {
		return fs.ErrNotExist
	}

	toParent, toBase, err := a.parentOf(to)
	if err != nil {
		return err
	}

	if existing, ok := toParent.children[toBase]; ok && existing.isDir && len(existing.children) > 0 {
		return fmt.Errorf("%s: directory not empty", to)
	}

	delete(fromParent.children, fromBase)

	n.name = toBase
	toParent.children[toBase] = n
	a.dirty = true

	return nil
}

// Chtimes implements vfs.FS.
func (a *FS) Chtimes(_ context.Context, p string, mtime time.Time) error {
	return a.update(p, func(n *node) { n.mtime = mtime })
}

// Chmod implements vfs.FS for permission and setuid/setgid/sticky bits.
func (a *FS) Chmod(_ context.Context, p string, mode fs.FileMode) error {
	const bits = fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky

	return a.update(p, func(n *node) { n.mode = n.mode&^bits | mode&bits })
}

// Symlink implements vfs.FS.
func (a *FS) Symlink(_ context.Context, target, p string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.writable(); err != nil {
		return err
	}

	parent, base, err := a.parentOf(p)
	if err != nil {
		return err
	}

	if _, ok := parent.children[base]; ok {
		return fs.ErrExist
	}

	parent.children[base] = &node{
		name:  base,
		mode:  fs.ModeSymlink | 0o777,
		mtime: time.Now(),
		link:  target,
		size:  int64(len(target)),
	}
	a.dirty = true

	return nil
}

// Readlink returns the target of the symlink at p.
func (a *FS) Readlink(_ context.Context, p string) (string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	n, ok := a.root.lookup(p)
	if !ok {
		return "", fs.ErrNotExist
	}

	if n.link == "" {
		return "", fmt.Errorf("%s: not a symlink", p)
	}

	return n.link, nil
}

// index reads the archive's table of contents into a fresh tree.
func (a *FS) index(ctx context.Context) error {
	f, err := os.Open(a.src)
	if err != nil {
		return fmt.Errorf("archive-open: %w", err)
	}

	defer f.Close()

	format, ex, err := a.identify(ctx, f)
	if err != nil {
		return err
	}

	root := newDir("", time.Time{})

	err = ex.Extract(ctx, f, func(_ context.Context, fi archives.FileInfo) error {
		addEntry(root, fi)

		return nil
	})
	if err != nil {
		return fmt.Errorf("archive-index: %w", err)
	}

	a.format = format
	a.writer = writerFor(format)
	a.root = root
	a.dirty = false

	return nil
}

// identify detects the archive format of f and rewinds it for extraction.
func (a *FS) identify(ctx context.Context, f *os.File) (archives.Format, archives.Extractor, error) {
	format, _, err := archives.Identify(ctx, path.Base(a.outerPath), f)
	if err != nil {
		return nil, nil, fmt.Errorf("archive %s: unsupported format", path.Base(a.outerPath))
	}

	ex, ok := format.(archives.Extractor)
	if !ok {
		return nil, nil, fmt.Errorf("archive %s: format cannot be read", path.Base(a.outerPath))
	}

	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, nil, fmt.Errorf("archive-seek: %w", err)
	}

	return format, ex, nil
}

func (a *FS) writable() error {
	if a.writer == nil {
		return vfs.ErrReadOnly
	}

	return nil
}

// parentOf returns the directory node that will hold p.
func (a *FS) parentOf(p string) (*node, string, error) {
	parts := split(p)
	if len(parts) == 0 {
		return nil, "", fmt.Errorf("invalid path %q", p)
	}

	parent, ok := a.root.lookup(path.Dir("/" + strings.Join(parts, "/")))
	if !ok || !parent.isDir {
		return nil, "", fs.ErrNotExist
	}

	return parent, parts[len(parts)-1], nil
}

func (a *FS) update(p string, fn func(*node)) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.writable(); err != nil {
		return err
	}

	n, ok := a.root.lookup(p)
	if !ok {
		return fs.ErrNotExist
	}

	fn(n)

	a.dirty = true

	return nil
}
