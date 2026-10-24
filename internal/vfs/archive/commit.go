package archive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"

	"github.com/mholt/archives"

	"github.com/abunjevac/bfiles/internal/vfs"
)

// errStopped reports that the archive writer stopped consuming files early.
var errStopped = errors.New("archive writer stopped")

// Commit rebuilds the archive with all pending changes. The new archive is written
// next to the original under a temporary name and renamed over it only when complete,
// so a failure at any point leaves the original untouched.
func (a *FS) Commit(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.dirty {
		return nil
	}

	if err := a.writable(); err != nil {
		return err
	}

	tmpPath := path.Join(path.Dir(a.outerPath), "."+path.Base(a.outerPath)+".bfiles-tmp")

	newSrc, err := a.writeTemp(ctx, tmpPath)
	if err != nil {
		return fmt.Errorf("archive-commit: %w", err)
	}

	if err := a.outer.Rename(ctx, tmpPath, a.outerPath); err != nil {
		_ = a.outer.Remove(ctx, tmpPath)
		removeCache(newSrc, a.tmpDir)

		return fmt.Errorf("archive-commit: %w", err)
	}

	if newSrc == "" {
		newSrc, _ = localPath(a.outer, a.outerPath)
	}

	removeCache(a.src, a.tmpDir)
	a.discardOverlay()

	a.src = newSrc

	if err := a.index(ctx); err != nil {
		return err
	}

	// nested archive: the outer archive now holds a modified member
	if c, ok := a.outer.(vfs.Committer); ok {
		return c.Commit(ctx)
	}

	return nil
}

// writeTemp writes the rebuilt archive to tmpPath on the outer filesystem. For a
// non-local outer filesystem the bytes are also kept in a local cache file, whose
// path is returned so the archive stays readable without downloading it again.
func (a *FS) writeTemp(ctx context.Context, tmpPath string) (string, error) {
	w, err := a.outer.Create(ctx, tmpPath, a.archiveMode(ctx))
	if err != nil {
		return "", err
	}

	var (
		out   = io.Writer(w)
		cache *os.File
	)

	if _, ok := localPath(a.outer, a.outerPath); !ok {
		cache, err = os.CreateTemp(a.tmpDir, "source-*")
		if err != nil {
			_ = w.Close()
			_ = a.outer.Remove(ctx, tmpPath)

			return "", err
		}

		out = io.MultiWriter(w, cache)
	}

	ew := &errWriter{w: out}

	err = a.write(ctx, ew)
	if err == nil {
		err = ew.err
	}

	if cerr := w.Close(); err == nil {
		err = cerr
	}

	cacheName := ""

	if cache != nil {
		cacheName = cache.Name()

		if cerr := cache.Close(); err == nil {
			err = cerr
		}
	}

	if err != nil {
		_ = a.outer.Remove(ctx, tmpPath)
		removeCache(cacheName, a.tmpDir)

		return "", err
	}

	return cacheName, nil
}

func (a *FS) archiveMode(ctx context.Context) fs.FileMode {
	if e, err := a.outer.Stat(ctx, a.outerPath); err == nil {
		return e.Mode.Perm()
	}

	return 0o644
}

// write streams every node to the archive writer: directories and links first, then
// unchanged members in one pass over the source archive, then overlay files.
func (a *FS) write(ctx context.Context, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan archives.ArchiveAsyncJob)
	done := make(chan struct{})

	var werr error

	go func() {
		defer close(done)

		werr = a.writer.ArchiveAsync(ctx, out, jobs)
	}()

	send := func(fi archives.FileInfo) error {
		res := make(chan error, 1)

		select {
		case jobs <- archives.ArchiveAsyncJob{File: fi, Result: res}:
		case <-done:
			return errStopped
		case <-ctx.Done():
			return ctx.Err()
		}

		select {
		case err := <-res:
			return err
		case <-done:
			return errStopped
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	err := a.produce(ctx, send)

	close(jobs)
	<-done

	if err != nil {
		return err
	}

	return werr
}

// member is a node with its path inside the archive.
type member struct {
	rel string
	n   *node
}

func (a *FS) produce(ctx context.Context, send func(archives.FileInfo) error) error {
	meta, pending, overlays := collectMembers(a.root)

	for _, m := range meta {
		if err := send(archives.FileInfo{FileInfo: fileInfo{m.n}, NameInArchive: m.rel, LinkTarget: m.n.link}); err != nil {
			return err
		}
	}

	if len(pending) > 0 {
		err := a.copyMembers(ctx, len(pending), func(orig string, open func() (fs.File, error)) (bool, error) {
			m, ok := pending[orig]
			if !ok {
				return false, nil
			}

			return true, send(archives.FileInfo{FileInfo: fileInfo{m.n}, NameInArchive: m.rel, Open: open})
		})
		if err != nil {
			return err
		}
	}

	for _, m := range overlays {
		overlay := m.n.overlay

		err := send(archives.FileInfo{
			FileInfo:      fileInfo{m.n},
			NameInArchive: m.rel,
			Open:          func() (fs.File, error) { return os.Open(overlay) },
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// collectMembers sorts the tree into entries without data (directories and
// symlinks), unchanged members keyed by their original name, and overlay files.
func collectMembers(root *node) ([]member, map[string]member, []member) {
	var (
		meta     []member
		pending  = map[string]member{}
		overlays []member
	)

	root.walk("/", func(p string, n *node) {
		rel := p[1:]

		switch {
		case n.isDir:
			meta = append(meta, member{rel + "/", n})
		case n.link != "":
			meta = append(meta, member{rel, n})
		case n.overlay != "":
			overlays = append(overlays, member{rel, n})
		default:
			pending[n.orig] = member{rel, n}
		}
	})

	return meta, pending, overlays
}

// copyMembers walks the source archive once, offering every member to emit, which
// reports whether it used it. The walk stops early once want members were used.
func (a *FS) copyMembers(
	ctx context.Context,
	want int,
	emit func(orig string, open func() (fs.File, error)) (bool, error),
) error {
	ex, ok := a.format.(archives.Extractor)
	if !ok {
		return fmt.Errorf("archive cannot be read")
	}

	f, err := os.Open(a.src)
	if err != nil {
		return err
	}

	defer f.Close()

	used := 0

	err = ex.Extract(ctx, f, func(_ context.Context, fi archives.FileInfo) error {
		took, emitErr := emit(fi.NameInArchive, fi.Open)
		if emitErr != nil {
			return emitErr
		}

		if took {
			used++
		}

		if used == want {
			return fs.SkipAll
		}

		return nil
	})
	if err != nil {
		return err
	}

	if used != want {
		return fmt.Errorf("source archive changed: %d members missing", want-used)
	}

	return nil
}

// discardOverlay deletes overlay files after their content was committed.
func (a *FS) discardOverlay() {
	a.root.walk("/", func(_ string, n *node) {
		if n.overlay != "" {
			_ = os.Remove(n.overlay)
		}
	})
}

// removeCache deletes p if it is a downloaded copy inside the private temp dir.
func removeCache(p, tmpDir string) {
	if p != "" && vfs.IsWithin(p, tmpDir) {
		_ = os.Remove(p)
	}
}

// errWriter remembers the first write error, because archive writers ignore errors
// from closing their compression streams.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}

	n, err := e.w.Write(p)
	if err != nil {
		e.err = err
	}

	return n, err
}
