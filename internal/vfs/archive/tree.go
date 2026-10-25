package archive

import (
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/abunjevac/bfiles/internal/vfs"
)

// node is one entry of the in-memory archive tree. Content comes either from the
// source archive (orig, the occ-th member of that name) or from a local overlay file
// (overlay); never both.
type node struct {
	name     string
	size     int64
	mode     fs.FileMode
	mtime    time.Time
	isDir    bool
	link     string
	orig     string
	occ      int
	overlay  string
	children map[string]*node
}

func newDir(name string, mtime time.Time) *node {
	return &node{name: name, isDir: true, mode: fs.ModeDir | 0o755, mtime: mtime, children: map[string]*node{}}
}

func (n *node) entry(p string) vfs.Entry {
	return vfs.Entry{
		Name:    n.name,
		Path:    p,
		Size:    n.size,
		Disk:    n.size,
		Mode:    n.mode,
		ModTime: n.mtime,
		IsDir:   n.isDir,
		IsLink:  n.link != "",
		Link:    n.link,
	}
}

// split turns "/a/b/c" into ["a", "b", "c"]; "/" yields nil.
func split(p string) []string {
	p = strings.Trim(path.Clean("/"+p), "/")
	if p == "" {
		return nil
	}

	return strings.Split(p, "/")
}

// lookup finds the node at p.
func (n *node) lookup(p string) (*node, bool) {
	cur := n

	for _, part := range split(p) {
		if !cur.isDir {
			return nil, false
		}

		next, ok := cur.children[part]
		if !ok {
			return nil, false
		}

		cur = next
	}

	return cur, true
}

// mkdirAll creates missing directories along p and returns the last one.
func (n *node) mkdirAll(p string, mtime time.Time) (*node, error) {
	cur := n

	for _, part := range split(p) {
		next, ok := cur.children[part]

		switch {
		case !ok:
			next = newDir(part, mtime)
			cur.children[part] = next
		case !next.isDir:
			return nil, fs.ErrExist
		}

		cur = next
	}

	return cur, nil
}

// walk visits every node below n depth-first, directories before their contents,
// children in name order so archives are rebuilt deterministically.
func (n *node) walk(p string, fn func(p string, n *node)) {
	names := make([]string, 0, len(n.children))

	for name := range n.children {
		names = append(names, name)
	}

	slices.Sort(names)

	for _, name := range names {
		child := n.children[name]
		cp := path.Join(p, name)

		fn(cp, child)

		if child.isDir {
			child.walk(cp, fn)
		}
	}
}

// fileInfo adapts a node to fs.FileInfo for the archive writers.
type fileInfo struct {
	n *node
}

func (fi fileInfo) Name() string       { return fi.n.name }
func (fi fileInfo) Size() int64        { return fi.n.size }
func (fi fileInfo) Mode() fs.FileMode  { return fi.n.mode }
func (fi fileInfo) ModTime() time.Time { return fi.n.mtime }
func (fi fileInfo) IsDir() bool        { return fi.n.isDir }
func (fi fileInfo) Sys() any           { return nil }
