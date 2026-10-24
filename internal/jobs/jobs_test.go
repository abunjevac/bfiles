package jobs_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/jobs"
	"github.com/abunjevac/bfiles/internal/vfs"
	"github.com/abunjevac/bfiles/internal/vfs/archive"
	"github.com/abunjevac/bfiles/internal/vfs/local"
)

// recorder collects queue events and lets tests wait for conditions.
type recorder struct {
	mu        sync.Mutex
	finished  []jobs.Info
	conflicts chan jobs.Conflict
	changed   chan struct{}
}

func newRecorder() *recorder {
	return &recorder{conflicts: make(chan jobs.Conflict, 16), changed: make(chan struct{}, 1)}
}

func (r *recorder) notify(ev jobs.Event) {
	r.mu.Lock()

	if ev.Finished != nil {
		r.finished = append(r.finished, *ev.Finished)
	}

	r.mu.Unlock()

	if ev.Conflict != nil {
		r.conflicts <- *ev.Conflict
	}

	select {
	case r.changed <- struct{}{}:
	default:
	}
}

func (r *recorder) waitFinished(t *testing.T, n int) []jobs.Info {
	t.Helper()

	deadline := time.After(10 * time.Second)

	for {
		r.mu.Lock()
		got := append([]jobs.Info(nil), r.finished...)
		r.mu.Unlock()

		if len(got) >= n {
			return got
		}

		select {
		case <-r.changed:
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatalf("timed out waiting for %d finished jobs, have %d", n, len(got))
		}
	}
}

func write(t *testing.T, p, content string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
}

func read(t *testing.T, p string) string {
	t.Helper()

	data, err := os.ReadFile(p)
	require.NoError(t, err)

	return string(data)
}

func tree(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	write(t, filepath.Join(dir, "src", "a.txt"), "alpha")
	write(t, filepath.Join(dir, "src", "sub", "b.txt"), "bravo")
	require.NoError(t, os.Symlink("a.txt", filepath.Join(dir, "src", "link")))

	return dir
}

func TestCopyTree(t *testing.T) {
	t.Parallel()

	dir := tree(t)
	rec := newRecorder()
	q := jobs.NewQueue(context.Background(), 1, rec.notify)

	defer q.Close()

	fsys := local.New()

	q.Add(jobs.Spec{Op: jobs.OpCopy, Src: fsys, Paths: []string{filepath.Join(dir, "src")}, Dst: fsys, DstDir: filepath.Join(dir, "dst")})

	got := rec.waitFinished(t, 1)
	require.Equal(t, jobs.Done, got[0].State, "%v", got[0].Err)

	require.Equal(t, "alpha", read(t, filepath.Join(dir, "dst", "src", "a.txt")))
	require.Equal(t, "bravo", read(t, filepath.Join(dir, "dst", "src", "sub", "b.txt")))

	link, err := os.Readlink(filepath.Join(dir, "dst", "src", "link"))
	require.NoError(t, err)
	require.Equal(t, "a.txt", link)

	require.Equal(t, int64(10), got[0].Done)
}

func TestMoveSameFSUsesRename(t *testing.T) {
	t.Parallel()

	dir := tree(t)
	rec := newRecorder()
	q := jobs.NewQueue(context.Background(), 1, rec.notify)

	defer q.Close()

	fsys := local.New()
	src := filepath.Join(dir, "src")

	before, err := os.Stat(filepath.Join(src, "a.txt"))
	require.NoError(t, err)

	q.Add(jobs.Spec{Op: jobs.OpMove, Src: fsys, Paths: []string{src}, Dst: fsys, DstDir: filepath.Join(dir, "moved")})

	got := rec.waitFinished(t, 1)
	require.Equal(t, jobs.Done, got[0].State, "%v", got[0].Err)

	require.NoDirExists(t, src)

	after, err := os.Stat(filepath.Join(dir, "moved", "src", "a.txt"))
	require.NoError(t, err)

	require.True(t, os.SameFile(before, after), "rename must keep the inode")
}

func TestMoveAcrossFilesystems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := tree(t)
	rec := newRecorder()
	q := jobs.NewQueue(context.Background(), 1, rec.notify)

	defer q.Close()

	arc, err := archive.NewEmpty(ctx, local.New(), filepath.Join(dir, "out.zip"))
	require.NoError(t, err)

	defer arc.Close()

	q.Add(jobs.Spec{Op: jobs.OpMove, Src: local.New(), Paths: []string{filepath.Join(dir, "src", "sub")}, Dst: arc, DstDir: "/"})

	got := rec.waitFinished(t, 1)
	require.Equal(t, jobs.Done, got[0].State, "%v", got[0].Err)

	require.NoDirExists(t, filepath.Join(dir, "src", "sub"))
	require.FileExists(t, filepath.Join(dir, "out.zip"), "archive must be committed")

	r, err := arc.Open(ctx, "/sub/b.txt")
	require.NoError(t, err)

	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, "bravo", string(data))
}

func TestCopyConflictAsks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write(t, filepath.Join(dir, "src", "f1"), "new1")
	write(t, filepath.Join(dir, "src", "f2"), "new2")
	write(t, filepath.Join(dir, "src", "f3"), "new3")
	write(t, filepath.Join(dir, "dst", "f1"), "old1")
	write(t, filepath.Join(dir, "dst", "f2"), "old2")
	write(t, filepath.Join(dir, "dst", "f3"), "old3")

	rec := newRecorder()
	q := jobs.NewQueue(context.Background(), 1, rec.notify)

	defer q.Close()

	fsys := local.New()
	id := q.Add(jobs.Spec{
		Op: jobs.OpCopy, Src: fsys, Dst: fsys, DstDir: filepath.Join(dir, "dst"),
		Paths: []string{filepath.Join(dir, "src", "f1"), filepath.Join(dir, "src", "f2"), filepath.Join(dir, "src", "f3")},
	})

	c := <-rec.conflicts
	require.Equal(t, id, c.JobID)
	require.Equal(t, "f1", c.Dst.Name)
	q.Answer(id, jobs.Answer{Res: jobs.Skip})

	c = <-rec.conflicts
	require.Equal(t, "f2", c.Dst.Name)
	q.Answer(id, jobs.Answer{Res: jobs.RenameNew})

	c = <-rec.conflicts
	require.Equal(t, "f3", c.Dst.Name)
	q.Answer(id, jobs.Answer{Res: jobs.Overwrite})

	got := rec.waitFinished(t, 1)
	require.Equal(t, jobs.Done, got[0].State, "%v", got[0].Err)

	require.Equal(t, "old1", read(t, filepath.Join(dir, "dst", "f1")))
	require.Equal(t, "old2", read(t, filepath.Join(dir, "dst", "f2")))
	require.Equal(t, "new2", read(t, filepath.Join(dir, "dst", "f2 (1)")))
	require.Equal(t, "new3", read(t, filepath.Join(dir, "dst", "f3")))
}

func TestConflictAnswerForAll(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write(t, filepath.Join(dir, "src", "f1"), "new")
	write(t, filepath.Join(dir, "src", "f2"), "new")
	write(t, filepath.Join(dir, "dst", "f1"), "old")
	write(t, filepath.Join(dir, "dst", "f2"), "old")

	rec := newRecorder()
	q := jobs.NewQueue(context.Background(), 1, rec.notify)

	defer q.Close()

	fsys := local.New()
	id := q.Add(jobs.Spec{
		Op: jobs.OpCopy, Src: fsys, Dst: fsys, DstDir: filepath.Join(dir, "dst"),
		Paths: []string{filepath.Join(dir, "src", "f1"), filepath.Join(dir, "src", "f2")},
	})

	<-rec.conflicts
	q.Answer(id, jobs.Answer{Res: jobs.Overwrite, All: true})

	got := rec.waitFinished(t, 1)
	require.Equal(t, jobs.Done, got[0].State)
	require.Empty(t, rec.conflicts)

	require.Equal(t, "new", read(t, filepath.Join(dir, "dst", "f2")))
}

func TestCopyIntoSelfRejected(t *testing.T) {
	t.Parallel()

	dir := tree(t)
	rec := newRecorder()
	q := jobs.NewQueue(context.Background(), 1, rec.notify)

	defer q.Close()

	fsys := local.New()
	src := filepath.Join(dir, "src")

	q.Add(jobs.Spec{Op: jobs.OpCopy, Src: fsys, Paths: []string{src}, Dst: fsys, DstDir: filepath.Join(src, "sub")})
	q.Add(jobs.Spec{Op: jobs.OpMove, Src: fsys, Paths: []string{src}, Dst: fsys, DstDir: dir})

	got := rec.waitFinished(t, 2)

	require.Equal(t, jobs.Failed, got[0].State)
	require.ErrorIs(t, got[0].Err, jobs.ErrIntoItself)
	require.Equal(t, jobs.Failed, got[1].State)
	require.ErrorIs(t, got[1].Err, jobs.ErrSameFile)
	require.NoDirExists(t, filepath.Join(src, "sub", "src"))
}

// slowReader lets a test cancel a copy while it is in progress.
type slowFS struct {
	*local.FS

	started chan struct{}
	once    sync.Once
}

func (s *slowFS) ID() string { return "slow" }

func (s *slowFS) Open(ctx context.Context, p string) (io.ReadCloser, error) {
	r, err := s.FS.Open(ctx, p)
	if err != nil {
		return nil, err
	}

	return &slowReader{r: r, fs: s}, nil
}

type slowReader struct {
	r  io.ReadCloser
	fs *slowFS
}

func (s *slowReader) Read(p []byte) (int, error) {
	s.fs.once.Do(func() { close(s.fs.started) })
	time.Sleep(5 * time.Millisecond)

	return s.r.Read(p[:min(len(p), 1024)])
}

func (s *slowReader) Close() error { return s.r.Close() }

func TestCopyCancelLeavesNoPartial(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	big := make([]byte, 4<<20)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "big.bin"), big, 0o600))

	rec := newRecorder()
	q := jobs.NewQueue(context.Background(), 1, rec.notify)

	defer q.Close()

	src := &slowFS{FS: local.New(), started: make(chan struct{})}
	dst := filepath.Join(dir, "out")

	id := q.Add(jobs.Spec{Op: jobs.OpCopy, Src: src, Paths: []string{filepath.Join(dir, "big.bin")}, Dst: local.New(), DstDir: dst})

	<-src.started
	q.Cancel(id)

	got := rec.waitFinished(t, 1)
	require.Equal(t, jobs.Canceled, got[0].State)

	entries, err := os.ReadDir(dst)
	require.NoError(t, err)
	require.Empty(t, entries, "no partial or final file may remain")
}

func TestParallelLimitChangeOnlyAffectsWaiting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	for _, n := range []string{"a", "b", "c"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), make([]byte, 64<<10), 0o600))
	}

	rec := newRecorder()
	q := jobs.NewQueue(context.Background(), 1, rec.notify)

	defer q.Close()

	src := &slowFS{FS: local.New(), started: make(chan struct{})}

	names := []string{"a", "b", "c"}
	ids := make([]int, 0, len(names))

	for _, n := range names {
		ids = append(ids, q.Add(jobs.Spec{Op: jobs.OpCopy, Src: src, Paths: []string{filepath.Join(dir, n)}, Dst: local.New(), DstDir: filepath.Join(dir, "out-"+n)}))
	}

	<-src.started

	states := func() map[int]jobs.State {
		m := map[int]jobs.State{}

		for _, inf := range q.Snapshot() {
			m[inf.ID] = inf.State
		}

		return m
	}

	s := states()
	require.Equal(t, jobs.Running, s[ids[0]])
	require.Equal(t, jobs.Waiting, s[ids[1]])
	require.Equal(t, jobs.Waiting, s[ids[2]])

	q.SetParallel(2)

	s = states()
	require.Equal(t, jobs.Running, s[ids[0]], "running job must keep running")
	require.Equal(t, jobs.Running, s[ids[1]], "next waiting job starts")
	require.Equal(t, jobs.Waiting, s[ids[2]])

	got := rec.waitFinished(t, 3)
	for _, inf := range got {
		require.Equal(t, jobs.Done, inf.State)
	}
}

func TestPauseHoldsWaitingJob(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write(t, filepath.Join(dir, "a"), "x")

	rec := newRecorder()
	q := jobs.NewQueue(context.Background(), 1, rec.notify)

	defer q.Close()

	src := &slowFS{FS: local.New(), started: make(chan struct{})}

	write(t, filepath.Join(dir, "big"), string(make([]byte, 256<<10)))

	first := q.Add(jobs.Spec{Op: jobs.OpCopy, Src: src, Paths: []string{filepath.Join(dir, "big")}, Dst: local.New(), DstDir: filepath.Join(dir, "o1")})
	second := q.Add(jobs.Spec{Op: jobs.OpCopy, Src: local.New(), Paths: []string{filepath.Join(dir, "a")}, Dst: local.New(), DstDir: filepath.Join(dir, "o2")})

	q.Pause(second)

	got := rec.waitFinished(t, 1)
	require.Equal(t, first, got[0].ID)

	time.Sleep(50 * time.Millisecond)

	for _, inf := range q.Snapshot() {
		if inf.ID == second {
			require.Equal(t, jobs.Waiting, inf.State)
			require.True(t, inf.Paused)
		}
	}

	q.Resume(second)

	got = rec.waitFinished(t, 2)
	require.Equal(t, jobs.Done, got[1].State)
}

func TestDeleteDoesNotFollowSymlinks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	write(t, filepath.Join(dir, "keep", "precious"), "x")
	write(t, filepath.Join(dir, "victim", "f"), "y")
	require.NoError(t, os.Symlink(filepath.Join(dir, "keep"), filepath.Join(dir, "victim", "link")))

	rec := newRecorder()
	q := jobs.NewQueue(context.Background(), 1, rec.notify)

	defer q.Close()

	q.Add(jobs.Spec{Op: jobs.OpDelete, Src: local.New(), Paths: []string{filepath.Join(dir, "victim")}})

	got := rec.waitFinished(t, 1)
	require.Equal(t, jobs.Done, got[0].State, "%v", got[0].Err)

	require.NoDirExists(t, filepath.Join(dir, "victim"))
	require.FileExists(t, filepath.Join(dir, "keep", "precious"))
}

var _ vfs.FS = (*slowFS)(nil)
