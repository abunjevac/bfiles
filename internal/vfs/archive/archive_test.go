package archive_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mholt/archives"
	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/vfs"
	"github.com/abunjevac/bfiles/internal/vfs/archive"
	"github.com/abunjevac/bfiles/internal/vfs/local"
)

// makeArchive builds an archive at dst from a small tree: a.txt, dir/b.txt, dir/sub/c.txt.
func makeArchive(t *testing.T, dst string) {
	t.Helper()

	ctx := context.Background()
	src := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(src, "dir", "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "a.txt"), []byte("alpha"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(src, "dir", "b.txt"), []byte("bravo"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(src, "dir", "sub", "c.txt"), []byte("charlie"), 0o600))

	files, err := archives.FilesFromDisk(ctx, nil, map[string]string{src + "/": ""})
	require.NoError(t, err)

	format, _, err := archives.Identify(ctx, filepath.Base(dst), nil)
	require.NoError(t, err)

	out, err := os.Create(dst)
	require.NoError(t, err)

	archiver, ok := format.(archives.Archiver)
	require.True(t, ok)

	require.NoError(t, archiver.Archive(ctx, out, files))
	require.NoError(t, out.Close())
}

func names(t *testing.T, fsys vfs.FS, dir string) []string {
	t.Helper()

	entries, err := fsys.ReadDir(context.Background(), dir)
	require.NoError(t, err)

	out := make([]string, 0, len(entries))

	for _, e := range entries {
		out = append(out, e.Name)
	}

	slices.Sort(out)

	return out
}

func readFile(t *testing.T, fsys vfs.FS, p string) string {
	t.Helper()

	r, err := fsys.Open(context.Background(), p)
	require.NoError(t, err)

	defer r.Close()

	data, err := io.ReadAll(r)
	require.NoError(t, err)

	return string(data)
}

func writeFile(t *testing.T, fsys vfs.FS, p, content string) {
	t.Helper()

	w, err := fsys.Create(context.Background(), p, 0o644)
	require.NoError(t, err)

	_, err = io.WriteString(w, content)
	require.NoError(t, err)
	require.NoError(t, w.Close())
}

func TestReadAndModify(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"test.zip", "test.tar", "test.tar.gz", "test.tar.zst", "test.tar.xz", "test.tar.bz2"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			path := filepath.Join(t.TempDir(), name)

			makeArchive(t, path)

			a, err := archive.Open(ctx, local.New(), path)
			require.NoError(t, err)

			defer a.Close()

			require.True(t, a.Caps().Write)
			require.Equal(t, []string{"a.txt", "dir"}, names(t, a, "/"))
			require.Equal(t, []string{"b.txt", "sub"}, names(t, a, "/dir"))
			require.Equal(t, "charlie", readFile(t, a, "/dir/sub/c.txt"))

			e, err := a.Stat(ctx, "/dir/b.txt")
			require.NoError(t, err)
			require.Equal(t, int64(5), e.Size)

			writeFile(t, a, "/dir/new.txt", "delta")
			writeFile(t, a, "/a.txt", "alpha2")
			require.NoError(t, a.Remove(ctx, "/dir/sub/c.txt"))
			require.NoError(t, a.Rename(ctx, "/dir/b.txt", "/b-moved.txt"))
			require.NoError(t, a.MkdirAll(ctx, "/empty/dir", 0o755))

			require.True(t, a.Dirty())
			require.NoError(t, a.Commit(ctx))
			require.False(t, a.Dirty())

			reopened, err := archive.Open(ctx, local.New(), path)
			require.NoError(t, err)

			defer reopened.Close()

			require.Equal(t, []string{"a.txt", "b-moved.txt", "dir", "empty"}, names(t, reopened, "/"))
			require.Equal(t, []string{"new.txt", "sub"}, names(t, reopened, "/dir"))
			require.Empty(t, names(t, reopened, "/dir/sub"))
			require.Equal(t, "alpha2", readFile(t, reopened, "/a.txt"))
			require.Equal(t, "bravo", readFile(t, reopened, "/b-moved.txt"))
			require.Equal(t, "delta", readFile(t, reopened, "/dir/new.txt"))

			// the committed FS itself reflects the new archive too
			require.Equal(t, "delta", readFile(t, a, "/dir/new.txt"))

			matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".*bfiles-tmp"))
			require.NoError(t, err)
			require.Empty(t, matches)
		})
	}
}

func TestNewEmptyPack(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "packed.tar.gz")

	a, err := archive.NewEmpty(ctx, local.New(), path)
	require.NoError(t, err)

	defer a.Close()

	require.NoError(t, a.MkdirAll(ctx, "/docs", 0o755))
	writeFile(t, a, "/docs/readme.md", "# hi")
	require.NoError(t, a.Symlink(ctx, "docs/readme.md", "/link"))
	require.NoError(t, a.Commit(ctx))

	reopened, err := archive.Open(ctx, local.New(), path)
	require.NoError(t, err)

	defer reopened.Close()

	require.Equal(t, "# hi", readFile(t, reopened, "/docs/readme.md"))

	link, err := reopened.Readlink(ctx, "/link")
	require.NoError(t, err)
	require.Equal(t, "docs/readme.md", link)
}

func TestReadOnlyFormats(t *testing.T) {
	t.Parallel()

	_, err := archive.NewEmpty(context.Background(), local.New(), filepath.Join(t.TempDir(), "x.7z"))
	require.ErrorContains(t, err, "read-only")
}

func TestRenameIntoItselfRejected(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "t.zip")

	makeArchive(t, path)

	a, err := archive.Open(ctx, local.New(), path)
	require.NoError(t, err)

	defer a.Close()

	require.Error(t, a.Rename(ctx, "/dir", "/dir/sub/dir"))
}

// failingRename wraps the local FS and refuses renames, simulating a failure at the
// last step of a commit.
type failingRename struct {
	*local.FS
}

func (failingRename) Rename(context.Context, string, string) error {
	return errors.New("rename refused")
}

func TestCommitFailureKeepsOriginal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "keep.zip")

	makeArchive(t, path)

	before, err := os.ReadFile(path)
	require.NoError(t, err)

	a, err := archive.Open(ctx, failingRename{local.New()}, path)
	require.NoError(t, err)

	defer a.Close()

	writeFile(t, a, "/x.txt", "x")
	require.ErrorContains(t, a.Commit(ctx), "rename refused")

	after, err := os.ReadFile(path)
	require.NoError(t, err)

	require.Equal(t, before, after)
	require.True(t, a.Dirty())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "temp file must be cleaned up")
}

// remoteFS hides LocalPath, forcing the download/cache path used for SFTP archives.
type remoteFS struct {
	vfs.FS
}

func (remoteFS) ID() string { return "remote-test" }

func TestNonLocalOuter(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "r.tar.gz")

	makeArchive(t, path)

	a, err := archive.Open(ctx, remoteFS{local.New()}, path)
	require.NoError(t, err)

	defer a.Close()

	require.Equal(t, "alpha", readFile(t, a, "/a.txt"))

	writeFile(t, a, "/z.txt", "zulu")
	require.NoError(t, a.Commit(ctx))

	require.Equal(t, "zulu", readFile(t, a, "/z.txt"))
	require.Equal(t, "alpha", readFile(t, a, "/a.txt"))
}
