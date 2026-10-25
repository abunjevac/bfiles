package jobs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/jobs"
	"github.com/abunjevac/bfiles/internal/vfs/local"
)

func runJob(t *testing.T, spec jobs.Spec) jobs.Info {
	t.Helper()

	rec := newRecorder()
	q := jobs.NewQueue(context.Background(), 1, rec.notify)

	defer q.Close()

	q.Add(spec)

	return rec.waitFinished(t, 1)[0]
}

func TestMoveOntoItselfThroughSymlinkKeepsData(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write(t, filepath.Join(dir, "x", "d", "f.txt"), "only copy")
	require.NoError(t, os.Symlink("x", filepath.Join(dir, "y")))

	fsys := local.New()

	for _, op := range []jobs.Op{jobs.OpMove, jobs.OpCopy} {
		got := runJob(t, jobs.Spec{
			Op: op, Src: fsys, Paths: []string{filepath.Join(dir, "x", "d")},
			Dst: fsys, DstDir: filepath.Join(dir, "y"), Overwrite: true,
		})
		require.Equal(t, jobs.Failed, got.State)
		require.ErrorIs(t, got.Err, jobs.ErrSameFile)
		require.Equal(t, "only copy", read(t, filepath.Join(dir, "x", "d", "f.txt")))
	}
}

func TestCopyIntoItselfThroughSymlinkRejected(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write(t, filepath.Join(dir, "x", "d", "sub", "f"), "x")
	require.NoError(t, os.Symlink(filepath.Join("x", "d", "sub"), filepath.Join(dir, "z")))

	fsys := local.New()
	got := runJob(t, jobs.Spec{Op: jobs.OpCopy, Src: fsys, Paths: []string{filepath.Join(dir, "x", "d")}, Dst: fsys, DstDir: filepath.Join(dir, "z")})

	require.Equal(t, jobs.Failed, got.State)
	require.ErrorIs(t, got.Err, jobs.ErrIntoItself)
	require.NoDirExists(t, filepath.Join(dir, "x", "d", "sub", "d"))
}

// Hard links stand in for bind mounts: the same file under unrelated paths.
func TestMoveOntoSameFileUnderOtherNameKeepsSource(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write(t, filepath.Join(dir, "x", "d", "f"), "data")
	write(t, filepath.Join(dir, "x", "d", "g"), "other")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "y", "d"), 0o755))
	require.NoError(t, os.Link(filepath.Join(dir, "x", "d", "f"), filepath.Join(dir, "y", "d", "f")))

	fsys := local.New()
	got := runJob(t, jobs.Spec{
		Op: jobs.OpMove, Src: fsys, Paths: []string{filepath.Join(dir, "x", "d")},
		Dst: fsys, DstDir: filepath.Join(dir, "y"), Overwrite: true,
	})

	require.Equal(t, jobs.Failed, got.State)
	require.ErrorIs(t, got.Err, jobs.ErrSameFile)
	require.Equal(t, "data", read(t, filepath.Join(dir, "x", "d", "f")))
	require.Equal(t, "other", read(t, filepath.Join(dir, "y", "d", "g")))
	require.NoFileExists(t, filepath.Join(dir, "x", "d", "g"), "copied items are still moved")
}

// growingFS adds a file to the source directory once the first file was read.
type growingFS struct {
	*local.FS

	extra string
	done  atomic.Bool
}

func (g *growingFS) ID() string { return "growing" }

func (g *growingFS) Remove(ctx context.Context, p string) error {
	if g.done.CompareAndSwap(false, true) {
		if err := os.WriteFile(g.extra, []byte("late"), 0o600); err != nil {
			return err
		}
	}

	return g.FS.Remove(ctx, p)
}

func TestMoveKeepsFilesCreatedDuringMove(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src")

	write(t, filepath.Join(src, "a"), "alpha")

	g := &growingFS{FS: local.New(), extra: filepath.Join(src, "late")}
	got := runJob(t, jobs.Spec{Op: jobs.OpMove, Src: g, Paths: []string{src}, Dst: local.New(), DstDir: filepath.Join(dir, "dst")})

	require.Equal(t, jobs.Failed, got.State)
	require.ErrorContains(t, got.Err, "changed during the move")
	require.Equal(t, "alpha", read(t, filepath.Join(dir, "dst", "src", "a")))
	require.NoFileExists(t, filepath.Join(src, "a"))
	require.Equal(t, "late", read(t, filepath.Join(src, "late")))
}

func TestSymlinkOverwrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write(t, filepath.Join(dir, "src", "a.txt"), "alpha")
	require.NoError(t, os.Symlink("a.txt", filepath.Join(dir, "src", "link")))
	write(t, filepath.Join(dir, "dst", "link"), "old file")
	write(t, filepath.Join(dir, "dst2", "link", "keep"), "dir content")

	fsys := local.New()
	got := runJob(t, jobs.Spec{
		Op: jobs.OpCopy, Src: fsys, Paths: []string{filepath.Join(dir, "src", "link")},
		Dst: fsys, DstDir: filepath.Join(dir, "dst"), Overwrite: true,
	})
	require.Equal(t, jobs.Done, got.State, "%v", got.Err)

	link, err := os.Readlink(filepath.Join(dir, "dst", "link"))
	require.NoError(t, err)
	require.Equal(t, "a.txt", link)

	got = runJob(t, jobs.Spec{
		Op: jobs.OpCopy, Src: fsys, Paths: []string{filepath.Join(dir, "src", "link")},
		Dst: fsys, DstDir: filepath.Join(dir, "dst2"), Overwrite: true,
	})
	require.Equal(t, jobs.Failed, got.State)
	require.ErrorContains(t, got.Err, "target is a directory")
	require.Equal(t, "dir content", read(t, filepath.Join(dir, "dst2", "link", "keep")))
}

func TestCopyDirReplacesDestinationSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write(t, filepath.Join(dir, "src", "a"), "alpha")
	write(t, filepath.Join(dir, "elsewhere", "keep"), "keep")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dst"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(dir, "elsewhere"), filepath.Join(dir, "dst", "src")))

	fsys := local.New()
	got := runJob(t, jobs.Spec{
		Op: jobs.OpCopy, Src: fsys, Paths: []string{filepath.Join(dir, "src")},
		Dst: fsys, DstDir: filepath.Join(dir, "dst"), Overwrite: true,
	})
	require.Equal(t, jobs.Done, got.State, "%v", got.Err)

	fi, err := os.Lstat(filepath.Join(dir, "dst", "src"))
	require.NoError(t, err)
	require.True(t, fi.IsDir(), "the link must be replaced by a directory")
	require.Equal(t, "alpha", read(t, filepath.Join(dir, "dst", "src", "a")))
	require.NoFileExists(t, filepath.Join(dir, "elsewhere", "a"))
}

// noRenameFS refuses renames of anything but partial files, like a rename across
// devices, forcing the copy fallback of a move.
type noRenameFS struct {
	*local.FS
}

func (noRenameFS) ID() string { return "no-rename" }

func (n noRenameFS) Rename(ctx context.Context, from, to string) error {
	if !strings.HasSuffix(from, ".bfiles-part") {
		return errors.New("cross-device link")
	}

	return n.FS.Rename(ctx, from, to)
}

func TestMoveFallbackAsksOnce(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write(t, filepath.Join(dir, "src", "f"), "new")
	write(t, filepath.Join(dir, "dst", "f"), "old")

	rec := newRecorder()
	q := jobs.NewQueue(context.Background(), 1, rec.notify)

	defer q.Close()

	fsys := noRenameFS{local.New()}
	id := q.Add(jobs.Spec{Op: jobs.OpMove, Src: fsys, Paths: []string{filepath.Join(dir, "src", "f")}, Dst: fsys, DstDir: filepath.Join(dir, "dst")})

	<-rec.conflicts
	q.Answer(id, jobs.Answer{Res: jobs.Overwrite})

	got := rec.waitFinished(t, 1)
	require.Equal(t, jobs.Done, got[0].State, "%v", got[0].Err)
	require.Empty(t, rec.conflicts, "the answer must be reused by the copy fallback")
	require.Equal(t, "new", read(t, filepath.Join(dir, "dst", "f")))
	require.NoFileExists(t, filepath.Join(dir, "src", "f"))
}

func TestAfterRunsForJobsThatNeverStart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write(t, filepath.Join(dir, "big"), string(make([]byte, 1<<20)))
	write(t, filepath.Join(dir, "a"), "x")

	rec := newRecorder()
	q := jobs.NewQueue(context.Background(), 1, rec.notify)
	src := &slowFS{FS: local.New(), started: make(chan struct{})}

	var first, canceled, closed atomic.Int32

	q.Add(jobs.Spec{
		Op: jobs.OpCopy, Src: src, Paths: []string{filepath.Join(dir, "big")}, Dst: local.New(), DstDir: filepath.Join(dir, "o1"),
		After: func() { first.Add(1) },
	})

	waiting := q.Add(jobs.Spec{
		Op: jobs.OpCopy, Src: local.New(), Paths: []string{filepath.Join(dir, "a")}, Dst: local.New(), DstDir: filepath.Join(dir, "o2"),
		After: func() { canceled.Add(1) },
	})
	q.Add(jobs.Spec{
		Op: jobs.OpCopy, Src: local.New(), Paths: []string{filepath.Join(dir, "a")}, Dst: local.New(), DstDir: filepath.Join(dir, "o3"),
		After: func() { closed.Add(1) },
	})

	<-src.started

	q.Cancel(waiting)
	q.Cancel(waiting)
	require.Equal(t, int32(1), canceled.Load())

	q.Close()

	require.Equal(t, int32(1), first.Load())
	require.Equal(t, int32(1), canceled.Load())
	require.Equal(t, int32(1), closed.Load())
}
