package vfs

import (
	"context"
	"errors"
	"io/fs"
	"path"
)

// SkipDir may be returned by a WalkFunc to skip a directory's contents.
var SkipDir = fs.SkipDir //nolint:errname,gochecknoglobals // mirrors io/fs.SkipDir

// WalkFunc is called for every entry below the walk root. A non-nil err reports a
// failure to read the directory at e; returning nil continues the walk.
type WalkFunc func(e Entry, err error) error

// Walk visits root and everything below it in lexical order per directory. A root
// that is a symlink to a directory is entered, but symlinks below the root are never
// followed, so the walk cannot loop and never escapes the tree.
func Walk(ctx context.Context, fsys FS, root string, fn WalkFunc) error {
	e, err := fsys.Stat(ctx, root)
	if err != nil {
		return fn(Entry{Path: root, Name: path.Base(root)}, err)
	}

	err = walk(ctx, fsys, e, fn, true)
	if errors.Is(err, SkipDir) {
		return nil
	}

	return err
}

func walk(ctx context.Context, fsys FS, e Entry, fn WalkFunc, isRoot bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := fn(e, nil); err != nil {
		return err
	}

	if !e.IsDir || (e.IsLink && !isRoot) {
		return nil
	}

	children, err := fsys.ReadDir(ctx, e.Path)
	if err != nil {
		if ferr := fn(e, err); ferr != nil && !errors.Is(ferr, SkipDir) {
			return ferr
		}

		return nil
	}

	return walkChildren(ctx, fsys, children, fn)
}

// walkChildren walks each child in turn; SkipDir from one child only skips that
// child.
func walkChildren(ctx context.Context, fsys FS, children []Entry, fn WalkFunc) error {
	for _, c := range children {
		if err := walk(ctx, fsys, c, fn, false); err != nil && !errors.Is(err, SkipDir) {
			return err
		}
	}

	return nil
}

// IsWithin reports whether p equals dir or lies below it.
func IsWithin(p, dir string) bool {
	p = path.Clean(p)
	dir = path.Clean(dir)

	if p == dir {
		return true
	}

	if dir == "/" {
		return true
	}

	return len(p) > len(dir) && p[:len(dir)] == dir && p[len(dir)] == '/'
}
