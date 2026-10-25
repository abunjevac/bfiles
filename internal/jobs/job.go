package jobs

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/abunjevac/bfiles/internal/vfs"
)

// Op is the kind of operation a job performs.
type Op int

// Operations.
const (
	OpCopy Op = iota
	OpMove
	OpDelete
)

func (o Op) String() string {
	switch o {
	case OpCopy:
		return "copy"
	case OpMove:
		return "move"
	case OpDelete:
		return "delete"
	}

	return "?"
}

// Spec describes a job to enqueue.
type Spec struct {
	Op    Op
	Src   vfs.FS
	Paths []string // absolute source paths
	Dst   vfs.FS
	// DstDir receives the copied/moved items.
	DstDir string
	// Rename gives the single source item a new name in DstDir.
	Rename string
	// Overwrite replaces existing targets without asking.
	Overwrite bool
	// Trash moves deleted items to the trash when the source supports it.
	Trash bool
	Title string
	// After runs exactly once when the job is over, whatever the outcome: after it
	// ran, when it is canceled while waiting, or when the queue is closed before it
	// started (e.g. to close a temporary archive filesystem).
	After func()
}

// State is a job's lifecycle state.
type State int

// Job states.
const (
	Waiting State = iota
	Running
	Done
	Failed
	Canceled
)

func (s State) String() string {
	return [...]string{"waiting", "running", "done", "failed", "canceled"}[s]
}

// Finished reports whether the job will not run anymore.
func (s State) Finished() bool { return s >= Done }

// Info is a snapshot of a job for display.
type Info struct {
	ID         int
	Title      string
	Op         Op
	State      State
	Paused     bool
	Done       int64
	Total      int64
	Files      int
	FilesTotal int
	Current    string
	Err        error
	Errors     int
	Speed      float64 // bytes per second
	Started    time.Time
	Finished   time.Time
	SrcID      string
	DstID      string
}

// Resolution answers a conflict.
type Resolution int

// Conflict resolutions.
const (
	Overwrite Resolution = iota
	Skip
	RenameNew
	OverwriteIfNewer
	CancelJob
)

// Answer is the user's reply to a conflict; All applies it to the rest of the job.
type Answer struct {
	Res Resolution
	All bool
}

// Conflict is raised when a target already exists.
type Conflict struct {
	JobID int
	Src   vfs.Entry
	Dst   vfs.Entry
}

// Event is delivered to the queue's notify callback.
type Event struct {
	Jobs     []Info
	Conflict *Conflict // set when a job waits for an Answer
	Finished *Info     // set when a job just finished
}

// ErrIntoItself is returned when a directory would be copied or moved into itself.
var ErrIntoItself = errors.New("cannot copy or move a directory into itself")

// ErrSameFile is returned when source and target are the same item.
var ErrSameFile = errors.New("source and target are the same")

type job struct {
	id   int
	spec Spec

	mu         sync.Mutex
	state      State
	paused     bool
	held       bool // waiting job held back by pause
	done       int64
	total      int64
	files      int
	filesTotal int
	current    string
	err        error
	errCount   int
	started    time.Time
	finished   time.Time
	policy     *Resolution
	decided    map[string]Resolution // answers per target path within this job
	resume     chan struct{}         // closed when unpaused
	answer     chan Answer
	cancel     context.CancelFunc
	canceled   bool
	afterOnce  sync.Once
}

func (j *job) info() Info {
	j.mu.Lock()
	defer j.mu.Unlock()

	inf := Info{
		ID:         j.id,
		Title:      j.spec.Title,
		Op:         j.spec.Op,
		State:      j.state,
		Paused:     j.paused || j.held,
		Done:       j.done,
		Total:      j.total,
		Files:      j.files,
		FilesTotal: j.filesTotal,
		Current:    j.current,
		Err:        j.err,
		Errors:     j.errCount,
		Started:    j.started,
		Finished:   j.finished,
	}

	if j.spec.Src != nil {
		inf.SrcID = j.spec.Src.ID()
	}

	if j.spec.Dst != nil {
		inf.DstID = j.spec.Dst.ID()
	}

	if !j.started.IsZero() && j.state == Running {
		if secs := time.Since(j.started).Seconds(); secs > 0.2 {
			inf.Speed = float64(j.done) / secs
		}
	}

	return inf
}

// runAfter runs Spec.After unless it already ran.
func (j *job) runAfter() {
	if j.spec.After != nil {
		j.afterOnce.Do(j.spec.After)
	}
}

// checkpoint blocks while the job is paused and reports cancellation.
func (j *job) checkpoint(ctx context.Context) error {
	j.mu.Lock()
	resume := j.resume
	paused := j.paused
	j.mu.Unlock()

	if paused {
		select {
		case <-resume:
		case <-ctx.Done():
		}
	}

	return ctx.Err()
}

func (j *job) addDone(n int64) {
	j.mu.Lock()
	j.done += n
	j.mu.Unlock()
}

func (j *job) setCurrent(p string) {
	j.mu.Lock()
	j.current = p
	j.mu.Unlock()
}

func (j *job) fileDone() {
	j.mu.Lock()
	j.files++
	j.mu.Unlock()
}

// fail records a per-item error; the job continues with the remaining items.
func (j *job) fail(err error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.err == nil {
		j.err = err
	}

	j.errCount++
}
