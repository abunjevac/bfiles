package local_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/vfs"
	"github.com/abunjevac/bfiles/internal/vfs/local"
)

func TestReadDirResolvesSymlinks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0o600))
	require.NoError(t, os.Symlink("sub", filepath.Join(dir, "linkdir")))
	require.NoError(t, os.Symlink("missing", filepath.Join(dir, "dangling")))

	entries, err := local.New().ReadDir(context.Background(), dir)
	require.NoError(t, err)

	byName := map[string]vfs.Entry{}

	for _, e := range entries {
		byName[e.Name] = e
	}

	require.Len(t, byName, 4)
	require.True(t, byName["sub"].IsDir)
	require.Equal(t, int64(5), byName["f.txt"].Size)
	require.True(t, byName["linkdir"].IsLink)
	require.True(t, byName["linkdir"].IsDir)
	require.Equal(t, "sub", byName["linkdir"].Link)
	require.True(t, byName["dangling"].IsLink)
	require.False(t, byName["dangling"].IsDir)
}

func TestReadDirLarge(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for i := range 3000 {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d", i)), nil, 0o600))
	}

	entries, err := local.New().ReadDir(context.Background(), dir)
	require.NoError(t, err)

	require.Len(t, entries, 3000)

	for _, e := range entries {
		require.NotEmpty(t, e.Name)
		require.Equal(t, filepath.Join(dir, e.Name), e.Path)
	}
}

func TestCreateRenameRemove(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := local.New()
	dir := t.TempDir()
	p := filepath.Join(dir, "a", "b")

	require.NoError(t, fsys.MkdirAll(ctx, p, 0o755))

	w, err := fsys.Create(ctx, filepath.Join(p, "x"), 0o600)
	require.NoError(t, err)

	_, err = io.WriteString(w, "data")
	require.NoError(t, err)
	require.NoError(t, w.Close())

	require.NoError(t, fsys.Rename(ctx, filepath.Join(p, "x"), filepath.Join(p, "y")))

	r, err := fsys.Open(ctx, filepath.Join(p, "y"))
	require.NoError(t, err)

	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())

	require.Equal(t, "data", string(data))

	require.NoError(t, fsys.Remove(ctx, filepath.Join(p, "y")))

	_, err = fsys.Stat(ctx, filepath.Join(p, "y"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestWalkDoesNotFollowDirLinks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a", "b", "f"), nil, 0o600))
	require.NoError(t, os.Symlink(dir, filepath.Join(dir, "a", "loop")))

	var seen []string

	err := vfs.Walk(context.Background(), local.New(), dir, func(e vfs.Entry, err error) error {
		require.NoError(t, err)

		rel, _ := filepath.Rel(dir, e.Path)
		seen = append(seen, rel)

		return nil
	})
	require.NoError(t, err)

	require.Equal(t, []string{".", "a", "a/b", "a/b/f", "a/loop"}, seen)
}

func TestTrash(t *testing.T) {
	dataHome := t.TempDir()

	t.Setenv("XDG_DATA_HOME", dataHome)

	dir := t.TempDir()
	p := filepath.Join(dir, "my file.txt")

	require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))
	require.NoError(t, local.New().Trash(context.Background(), p))

	require.NoFileExists(t, p)
	require.FileExists(t, filepath.Join(dataHome, "Trash", "files", "my file.txt"))

	info, err := os.ReadFile(filepath.Join(dataHome, "Trash", "info", "my file.txt.trashinfo"))
	require.NoError(t, err)

	require.Contains(t, string(info), "my%20file.txt")
}

func TestTrashKeepsFileWithoutInfo(t *testing.T) {
	dataHome := t.TempDir()

	t.Setenv("XDG_DATA_HOME", dataHome)

	old := filepath.Join(dataHome, "Trash", "files", "f.txt")

	require.NoError(t, os.MkdirAll(filepath.Dir(old), 0o700))
	require.NoError(t, os.WriteFile(old, []byte("old"), 0o600))

	p := filepath.Join(t.TempDir(), "f.txt")

	require.NoError(t, os.WriteFile(p, []byte("new"), 0o600))
	require.NoError(t, local.New().Trash(context.Background(), p))

	data, err := os.ReadFile(old)
	require.NoError(t, err)
	require.Equal(t, "old", string(data))

	data, err = os.ReadFile(filepath.Join(dataHome, "Trash", "files", "f.txt.1"))
	require.NoError(t, err)
	require.Equal(t, "new", string(data))
	require.FileExists(t, filepath.Join(dataHome, "Trash", "info", "f.txt.1.trashinfo"))
}

func TestWalkEntersSymlinkedRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "real", "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "real", "sub", "f"), nil, 0o600))
	require.NoError(t, os.Symlink("real", filepath.Join(dir, "link")))
	require.NoError(t, os.Symlink(dir, filepath.Join(dir, "real", "loop")))

	root := filepath.Join(dir, "link")

	var seen []string

	err := vfs.Walk(context.Background(), local.New(), root, func(e vfs.Entry, err error) error {
		require.NoError(t, err)

		rel, _ := filepath.Rel(root, e.Path)
		seen = append(seen, rel)

		return nil
	})
	require.NoError(t, err)

	require.Equal(t, []string{".", "loop", "sub", "sub/f"}, seen)
}

func TestIdentifier(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "x", "d"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x", "f"), nil, 0o600))
	require.NoError(t, os.Link(filepath.Join(dir, "x", "f"), filepath.Join(dir, "hard")))
	require.NoError(t, os.Symlink("x", filepath.Join(dir, "y")))

	fsys := local.New()

	got, err := fsys.RealPath(ctx, filepath.Join(dir, "y", "d"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "x", "d"), got)

	got, err = fsys.RealPath(ctx, filepath.Join(dir, "y", "missing", "m"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "x", "missing", "m"), got)

	require.True(t, fsys.SameFile(ctx, filepath.Join(dir, "x", "d"), filepath.Join(dir, "y", "d")))
	require.True(t, fsys.SameFile(ctx, filepath.Join(dir, "x", "f"), filepath.Join(dir, "hard")))
	require.False(t, fsys.SameFile(ctx, filepath.Join(dir, "x", "f"), filepath.Join(dir, "x", "d")))
	require.False(t, fsys.SameFile(ctx, filepath.Join(dir, "x", "f"), filepath.Join(dir, "nope")))
}

func TestIsWithin(t *testing.T) {
	t.Parallel()

	require.True(t, vfs.IsWithin("/a/b", "/a"))
	require.True(t, vfs.IsWithin("/a", "/a"))
	require.False(t, vfs.IsWithin("/ab", "/a"))
	require.True(t, vfs.IsWithin("/x", "/"))
}

func TestDetails(t *testing.T) {
	t.Parallel()

	p := filepath.Join(t.TempDir(), "f")

	require.NoError(t, os.WriteFile(p, []byte("x"), 0o600))

	d, err := local.New().Details(context.Background(), p)
	require.NoError(t, err)

	require.NotEmpty(t, d.Owner)
	require.NotEmpty(t, d.Group)
	require.NotZero(t, d.Inode)
	require.Equal(t, uint64(1), d.Links)
	require.False(t, d.Change.IsZero())
}
