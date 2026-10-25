package jobs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/abunjevac/bfiles/internal/vfs"
)

// progressInterval throttles progress events so a fast copy cannot flood the UI.
const progressInterval = 100 * time.Millisecond

// keepFinished is how many finished jobs stay visible in the queue.
const keepFinished = 20

// Queue schedules jobs. All methods are safe for concurrent use.
type Queue struct {
	ctx    context.Context //nolint:containedctx // base context for job goroutines
	notify func(Event)

	mu       sync.Mutex
	jobs     []*job
	nextID   int
	parallel int
	running  int
	closed   bool
	wg       sync.WaitGroup
	stop     chan struct{}
}

// NewQueue creates a queue running at most parallel jobs at once; jobs run under
// contexts derived from ctx. notify is called from queue goroutines and must not
// block for long.
func NewQueue(ctx context.Context, parallel int, notify func(Event)) *Queue {
	q := &Queue{ctx: ctx, notify: notify, parallel: max(parallel, 1), stop: make(chan struct{})}

	go q.ticker()

	return q
}

// Add enqueues a job and returns its ID.
func (q *Queue) Add(spec Spec) int {
	q.mu.Lock()

	if q.closed {
		q.mu.Unlock()

		return 0
	}

	q.nextID++

	j := &job{
		id:     q.nextID,
		spec:   spec,
		state:  Waiting,
		resume: make(chan struct{}),
		answer: make(chan Answer, 1),
	}

	if j.spec.Title == "" {
		j.spec.Title = defaultTitle(spec)
	}

	q.jobs = append(q.jobs, j)
	q.scheduleLocked()
	q.mu.Unlock()

	q.emit(Event{})

	return j.id
}

// Snapshot returns the state of all jobs in queue order.
func (q *Queue) Snapshot() []Info {
	q.mu.Lock()
	jobs := slices.Clone(q.jobs)
	q.mu.Unlock()

	out := make([]Info, len(jobs))

	for i, j := range jobs {
		out[i] = j.info()
	}

	return out
}

// Pending returns how many jobs are running or waiting.
func (q *Queue) Pending() int {
	n := 0

	for _, inf := range q.Snapshot() {
		if !inf.State.Finished() {
			n++
		}
	}

	return n
}

// Parallel returns the current parallelism limit.
func (q *Queue) Parallel() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.parallel
}

// SetParallel changes the limit. Running jobs are unaffected; the new value only
// governs how many waiting jobs may start.
func (q *Queue) SetParallel(n int) {
	q.mu.Lock()
	q.parallel = min(max(n, 1), 16)
	q.scheduleLocked()
	q.mu.Unlock()

	q.emit(Event{})
}

// Pause pauses a running job at its next checkpoint, or holds a waiting job back.
func (q *Queue) Pause(id int) {
	q.withJob(id, func(j *job) {
		switch j.state {
		case Running:
			if !j.paused {
				j.paused = true
				j.resume = make(chan struct{})
			}
		case Waiting:
			j.held = true
		default:
			// finished jobs cannot be paused
		}
	})
}

// Resume continues a paused or held job.
func (q *Queue) Resume(id int) {
	q.withJob(id, func(j *job) {
		if j.paused {
			j.paused = false
			close(j.resume)
		}

		j.held = false
	})

	q.mu.Lock()
	q.scheduleLocked()
	q.mu.Unlock()

	q.emit(Event{})
}

// TogglePause pauses or resumes a job.
func (q *Queue) TogglePause(id int) {
	paused := false

	q.withJob(id, func(j *job) { paused = j.paused || j.held })

	if paused {
		q.Resume(id)

		return
	}

	q.Pause(id)
	q.emit(Event{})
}

// Cancel stops a running job or removes a waiting one.
func (q *Queue) Cancel(id int) {
	var (
		info    *Info
		removed *job
	)

	q.withJob(id, func(j *job) {
		j.canceled = true

		switch j.state {
		case Running:
			j.cancel()
		case Waiting:
			j.state = Canceled
			j.finished = time.Now()

			inf := Info{ID: j.id, Title: j.spec.Title, Op: j.spec.Op, State: Canceled}
			info = &inf
			removed = j
		default:
			// finished jobs stay as they are
		}
	})

	if info != nil {
		// a job that never started still owes its cleanup
		removed.runAfter()
		q.emit(Event{Finished: info})

		return
	}

	q.emit(Event{})
}

// Dismiss removes a finished job from the list.
func (q *Queue) Dismiss(id int) {
	q.mu.Lock()

	q.jobs = slices.DeleteFunc(q.jobs, func(j *job) bool {
		j.mu.Lock()
		defer j.mu.Unlock()

		return j.id == id && j.state.Finished()
	})

	q.mu.Unlock()

	q.emit(Event{})
}

// ClearFinished removes all finished jobs from the list.
func (q *Queue) ClearFinished() {
	q.mu.Lock()

	q.jobs = slices.DeleteFunc(q.jobs, func(j *job) bool {
		j.mu.Lock()
		defer j.mu.Unlock()

		return j.state.Finished()
	})

	q.mu.Unlock()

	q.emit(Event{})
}

// MoveUp moves a waiting job one place earlier in the queue.
func (q *Queue) MoveUp(id int) { q.move(id, -1) }

// MoveDown moves a waiting job one place later in the queue.
func (q *Queue) MoveDown(id int) { q.move(id, 1) }

// Answer resolves a pending conflict of job id.
func (q *Queue) Answer(id int, a Answer) {
	q.mu.Lock()
	j := q.find(id)
	q.mu.Unlock()

	if j == nil {
		return
	}

	select {
	case j.answer <- a:
	default:
	}
}

// Close cancels all jobs and waits for them to stop.
func (q *Queue) Close() {
	q.mu.Lock()

	if q.closed {
		q.mu.Unlock()

		return
	}

	q.closed = true

	var unstarted []*job

	for _, j := range q.jobs {
		j.mu.Lock()

		j.canceled = true

		if j.cancel != nil {
			j.cancel()
		}

		if j.state == Waiting {
			j.state = Canceled
			j.finished = time.Now()
			unstarted = append(unstarted, j)
		}

		j.mu.Unlock()
	}

	q.mu.Unlock()

	for _, j := range unstarted {
		j.runAfter()
	}

	q.wg.Wait()
	close(q.stop)
}

// ticker publishes progress snapshots while jobs run.
func (q *Queue) ticker() {
	t := time.NewTicker(progressInterval)
	defer t.Stop()

	for {
		select {
		case <-q.stop:
			return
		case <-t.C:
			q.mu.Lock()
			active := q.running > 0
			q.mu.Unlock()

			if active {
				q.emit(Event{})
			}
		}
	}
}

func (q *Queue) emit(ev Event) {
	ev.Jobs = q.Snapshot()

	if q.notify != nil {
		q.notify(ev)
	}
}

// scheduleLocked starts waiting jobs, in queue order, while slots are free. A
// lower limit never stops running jobs; it only delays new ones.
func (q *Queue) scheduleLocked() {
	for _, j := range q.jobs {
		if q.running >= q.parallel || q.closed {
			return
		}

		j.mu.Lock()
		startable := j.state == Waiting && !j.held
		j.mu.Unlock()

		if startable {
			q.start(j)
		}
	}
}

func (q *Queue) start(j *job) {
	ctx, cancel := context.WithCancel(q.ctx)

	j.mu.Lock()
	j.state = Running
	j.started = time.Now()
	j.cancel = cancel
	j.mu.Unlock()

	q.running++

	q.wg.Go(func() {
		defer cancel()

		q.run(ctx, j)
	})
}

func (q *Queue) run(ctx context.Context, j *job) {
	err := q.execute(ctx, j)

	// commit even when the job was canceled, see commitAll
	commitErr := commitAll(context.WithoutCancel(ctx), j.spec.Src, j.spec.Dst)

	j.runAfter()

	j.mu.Lock()

	switch {
	case j.canceled || errors.Is(err, context.Canceled):
		j.state = Canceled
	case err != nil:
		j.state = Failed
		j.err = err
	case commitErr != nil:
		j.state = Failed
		j.err = commitErr
	case j.err != nil:
		j.state = Failed
	default:
		j.state = Done
	}

	j.finished = time.Now()
	j.current = ""
	j.mu.Unlock()

	info := j.info()

	q.mu.Lock()
	q.running--
	q.pruneLocked()
	q.scheduleLocked() //nolint:contextcheck // new jobs derive from the queue's base context, not this job's
	q.mu.Unlock()

	q.emit(Event{Finished: &info})
}

func (q *Queue) pruneLocked() {
	finished := 0

	for i, j := range slices.Backward(q.jobs) {
		j.mu.Lock()
		done := j.state.Finished()
		j.mu.Unlock()

		if !done {
			continue
		}

		finished++

		if finished > keepFinished {
			q.jobs = slices.Delete(q.jobs, i, i+1)
		}
	}
}

func (q *Queue) find(id int) *job {
	for _, j := range q.jobs {
		if j.id == id {
			return j
		}
	}

	return nil
}

func (q *Queue) move(id, delta int) {
	q.mu.Lock()

	i := slices.IndexFunc(q.jobs, func(j *job) bool { return j.id == id })
	k := i + delta

	if i >= 0 && k >= 0 && k < len(q.jobs) {
		q.jobs[i], q.jobs[k] = q.jobs[k], q.jobs[i]
	}

	q.mu.Unlock()

	q.emit(Event{})
}

func (q *Queue) withJob(id int, fn func(*job)) {
	q.mu.Lock()
	j := q.find(id)
	q.mu.Unlock()

	if j == nil {
		return
	}

	j.mu.Lock()
	fn(j)
	j.mu.Unlock()
}

func defaultTitle(s Spec) string {
	what := fmt.Sprintf("%d items", len(s.Paths))

	if len(s.Paths) == 1 {
		what = baseName(s.Paths[0])
	}

	if s.Op == OpDelete {
		return "delete " + what
	}

	return fmt.Sprintf("%s %s → %s", s.Op, what, s.DstDir)
}

// commitAll persists buffered archive changes even after a failure or cancel, so the
// archive reflects what was actually done, like a local directory would.
func commitAll(ctx context.Context, fss ...vfs.FS) error {
	var errs []error

	for i, f := range fss {
		c, ok := f.(vfs.Committer)
		if !ok || !c.Dirty() || (i > 0 && vfs.Same(f, fss[0])) {
			continue
		}

		if err := c.Commit(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
