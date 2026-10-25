package archive_test

import (
	"archive/tar"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/vfs"
	"github.com/abunjevac/bfiles/internal/vfs/archive"
	"github.com/abunjevac/bfiles/internal/vfs/local"
)

// tarMember is one header written by writeTar; Linkname makes a hard link.
type tarMember struct {
	name     string
	data     string
	linkname string
}

func writeTar(t *testing.T, dst string, members ...tarMember) {
	t.Helper()

	f, err := os.Create(dst)
	require.NoError(t, err)

	tw := tar.NewWriter(f)

	for _, m := range members {
		hdr := &tar.Header{Name: m.name, Mode: 0o644, Size: int64(len(m.data)), Typeflag: tar.TypeReg, ModTime: time.Now()}

		if m.linkname != "" {
			hdr.Typeflag = tar.TypeLink
			hdr.Linkname = m.linkname
			hdr.Size = 0
		}

		require.NoError(t, tw.WriteHeader(hdr))

		_, err := io.WriteString(tw, m.data)
		require.NoError(t, err)
	}

	require.NoError(t, tw.Close())
	require.NoError(t, f.Close())
}

func TestDuplicateMembersAreReadOnly(t *testing.T) {
	t.Parallel()

	for name, members := range map[string][]tarMember{
		"repeated":  {{name: "a", data: "old"}, {name: "a", data: "newer"}, {name: "z", data: "zulu"}},
		"dot-slash": {{name: "./a", data: "old"}, {name: "a", data: "newer"}, {name: "z", data: "zulu"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "d.tar")

			writeTar(t, path, members...)

			before, err := os.ReadFile(path)
			require.NoError(t, err)

			a, err := archive.Open(ctx, local.New(), path)
			require.NoError(t, err)

			defer a.Close()

			require.Equal(t, "newer", readFile(t, a, "/a"), "the last member wins")
			require.Equal(t, "zulu", readFile(t, a, "/z"))
			require.False(t, a.Caps().Write)
			require.Contains(t, a.ReadOnlyReason(), "duplicate")

			_, err = a.Create(ctx, "/b", 0o644)
			require.ErrorIs(t, err, vfs.ErrReadOnly)
			require.ErrorContains(t, err, "duplicate")
			require.ErrorIs(t, a.MkdirAll(ctx, "/x", 0o755), vfs.ErrReadOnly)

			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after)
		})
	}
}

func TestHardLinkReadsTargetData(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "h.tar")

	writeTar(t, path, tarMember{name: "f", data: "hello"}, tarMember{name: "h", linkname: "f"})

	a, err := archive.Open(ctx, local.New(), path)
	require.NoError(t, err)

	defer a.Close()

	e, err := a.Stat(ctx, "/h")
	require.NoError(t, err)
	require.Equal(t, int64(5), e.Size)
	require.Equal(t, "hello", readFile(t, a, "/h"))
	require.False(t, a.Caps().Write)
	require.Contains(t, a.ReadOnlyReason(), "hard links")
}

func TestCommitWaitsForOpenWriter(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "w.zip")

	makeArchive(t, path)

	a, err := archive.Open(ctx, local.New(), path)
	require.NoError(t, err)

	defer a.Close()

	w, err := a.Create(ctx, "/big.txt", 0o644)
	require.NoError(t, err)

	_, err = io.WriteString(w, "first ")
	require.NoError(t, err)

	canceled, cancel := context.WithCancel(ctx)
	cancel()

	require.ErrorIs(t, a.Commit(canceled), context.Canceled)

	done := make(chan error, 1)

	go func() { done <- a.Commit(ctx) }()

	select {
	case got := <-done:
		t.Fatalf("commit must wait for the open writer, returned %v", got)
	case <-time.After(50 * time.Millisecond):
	}

	_, err = io.WriteString(w, "second")
	require.NoError(t, err)
	require.NoError(t, w.Close())
	require.NoError(t, <-done)

	reopened, err := archive.Open(ctx, local.New(), path)
	require.NoError(t, err)

	defer reopened.Close()

	require.Equal(t, "first second", readFile(t, reopened, "/big.txt"))
}

// corruptingRename renames and then overwrites the new archive with garbage, so the
// re-read after a commit fails.
type corruptingRename struct {
	*local.FS
}

func (c corruptingRename) Rename(ctx context.Context, from, to string) error {
	if err := c.FS.Rename(ctx, from, to); err != nil {
		return err
	}

	return os.WriteFile(to, []byte("not an archive"), 0o600)
}

func TestCommitReindexFailureLeavesConsistentState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "c.zip")

	makeArchive(t, path)

	a, err := archive.Open(ctx, corruptingRename{local.New()}, path)
	require.NoError(t, err)

	defer a.Close()

	writeFile(t, a, "/x.txt", "x")
	require.ErrorContains(t, a.Commit(ctx), "re-reading failed")

	require.False(t, a.Dirty())
	require.False(t, a.Caps().Write)
	require.NoError(t, a.Commit(ctx))
	require.Empty(t, names(t, a, "/"))
}
