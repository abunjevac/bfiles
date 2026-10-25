package ui

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/abunjevac/bfiles/internal/config"
	"github.com/abunjevac/bfiles/internal/dirsize"
	"github.com/abunjevac/bfiles/internal/jobs"
	"github.com/abunjevac/bfiles/internal/keymap"
	"github.com/abunjevac/bfiles/internal/location"
	"github.com/abunjevac/bfiles/internal/preview"
	"github.com/abunjevac/bfiles/internal/sshcfg"
	"github.com/abunjevac/bfiles/internal/theme"
	"github.com/abunjevac/bfiles/internal/vfs"
	"github.com/abunjevac/bfiles/internal/vfs/archive"
	"github.com/abunjevac/bfiles/internal/vfs/local"
	"github.com/abunjevac/bfiles/internal/vfs/sftpfs"
)

// Options are per-run settings from the command line.
type Options struct {
	Theme  string // overrides the configured theme for this run
	Hidden bool
}

// sizeState is a cached or running directory size.
type sizeState struct {
	bytes   int64
	files   int
	dirs    int
	running bool
	err     error
}

// Model is the root Bubble Tea model.
type Model struct {
	ctx    context.Context //nolint:containedctx // root context of the program run
	send   func(tea.Msg)
	bundle *config.Bundle
	cfg    *config.Config
	theme  *theme.Theme
	st     styles
	km     *keymap.Keymap
	opts   Options

	width, height int

	panels [2]*Panel
	active int

	local *local.FS
	pool  *sftpfs.Pool
	refs  map[vfs.FS]int
	sshc  *sshcfg.Config

	queue      *jobs.Queue
	jobInfos   []jobs.Info
	queueOpen  bool
	queueFocus bool
	queueSel   int
	conflicts  []jobs.Conflict
	queueIdle  time.Time

	actions  map[keymap.Action]actionFunc
	overlays []overlay
	toasts   []toast

	seq        []string
	seqID      int
	whichKey   bool
	lastClick  time.Time
	lastClickY int

	sizes map[string]sizeState

	lastSearch []field // the search dialog's last values, reused next time

	quickView  bool
	previewID  int
	previewFor string
	previewRes *preview.Result
	previewCnl context.CancelFunc

	watch *watcher

	initial     [2]location.Spec
	startupErrs []string

	quitAsked    bool
	quitWhenIdle bool
	searchSeq    int
	startedAt    time.Time
}

// Run starts the UI on the given panel locations and blocks until the user quits.
func Run(ctx context.Context, bundle *config.Bundle, left, right location.Spec, opts Options) error {
	m := newModel(ctx, bundle, opts)

	p := tea.NewProgram(m, tea.WithContext(ctx))
	box := newMailbox(p.Send)
	m.send = box.post

	go box.run()

	m.panels[0].loading = true
	m.panels[1].loading = true
	m.initial = [2]location.Spec{left, right}

	_, err := p.Run()

	box.close()
	m.shutdown()

	if errors.Is(err, tea.ErrProgramKilled) {
		return nil
	}

	return err
}

func newModel(ctx context.Context, bundle *config.Bundle, opts Options) *Model {
	cfg := bundle.Config

	themeName := cfg.Theme
	if opts.Theme != "" {
		themeName = opts.Theme
	}

	th, themeErr := bundle.LoadTheme(themeName)
	km, kmErr := bundle.LoadKeymap()

	m := &Model{
		ctx:       ctx,
		send:      func(tea.Msg) {},
		bundle:    bundle,
		cfg:       cfg,
		theme:     th,
		st:        newStyles(th),
		km:        km,
		opts:      opts,
		local:     local.New(),
		refs:      map[vfs.FS]int{},
		actions:   actionTable(),
		sizes:     map[string]sizeState{},
		startedAt: time.Now(),
	}

	m.pool = sftpfs.NewPool(sftpfs.Options{Keepalive: time.Duration(cfg.SSH.KeepaliveSec) * time.Second})

	for i := range m.panels {
		m.panels[i] = newPanel(cfg)
		m.panels[i].id = i
		m.panels[i].showHidden = m.panels[i].showHidden || opts.Hidden
	}

	m.queue = jobs.NewQueue(ctx, cfg.ParallelJobs, func(ev jobs.Event) { m.send(jobsMsg(ev)) })

	if themeErr != nil {
		m.startupErrs = append(m.startupErrs, themeErr.Error())
	}

	if kmErr != nil {
		m.startupErrs = append(m.startupErrs, "keymap: "+kmErr.Error()+" (using defaults)")
	}

	return m
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, 1+len(m.initial)+len(m.startupErrs))
	cmds = append(cmds, m.startWatcher())

	for i, spec := range m.initial {
		cmds = append(cmds, m.openSpec(i, spec))
	}

	for _, e := range m.startupErrs {
		cmds = append(cmds, m.toast(e, levelError))
	}

	return tea.Batch(cmds...)
}

// openSpec navigates panel i to a parsed command-line location.
func (m *Model) openSpec(i int, spec location.Spec) tea.Cmd {
	switch spec.Kind {
	case location.SSH:
		return m.connectCmd(i, spec)
	case location.Archive:
		return m.openArchiveCmd(i, m.local, spec.Path, "")
	default:
		return m.navigate(i, m.local, spec.Path, spec.Select, false)
	}
}

// shutdown releases everything once the program has exited.
func (m *Model) shutdown() {
	m.queue.Close()

	if m.watch != nil {
		m.watch.close()
	}

	for fs := range m.refs {
		if fs.Kind() == vfs.KindArchive {
			_ = fs.Close()
		}
	}

	m.pool.CloseAll()
}

func (m *Model) panel() *Panel { return m.panels[m.active] }

// panelIndex finds where the panel with the given id currently is.
func (m *Model) panelIndex(id int) int {
	if m.panels[1].id == id {
		return 1
	}

	return 0
}

func (m *Model) other() *Panel { return m.panels[1-m.active] }

// retain and release reference-count filesystems shared by panels and jobs, so an
// archive or SSH connection stays open exactly as long as something uses it.
func (m *Model) retain(f vfs.FS) {
	if f == nil || f.Kind() == vfs.KindLocal {
		return
	}

	m.refs[f]++

	if a, ok := f.(*archive.FS); ok && m.refs[f] == 1 {
		outer, _ := a.Outer()
		m.retain(outer)
	}
}

func (m *Model) release(f vfs.FS) {
	if f == nil || f.Kind() == vfs.KindLocal {
		return
	}

	m.refs[f]--

	if m.refs[f] > 0 {
		return
	}

	delete(m.refs, f)
	m.forgetFS(f)

	switch t := f.(type) {
	case *archive.FS:
		outer, _ := t.Outer()
		_ = t.Close()

		m.release(outer)
	case *sftpfs.FS:
		m.pool.Release(t)
	}
}

// forgetFS drops back/forward history entries on a filesystem that is being closed,
// so history never leads to a dead archive or connection.
func (m *Model) forgetFS(f vfs.FS) {
	gone := func(pl place) bool { return pl.fs == f }

	for _, p := range m.panels {
		p.back = slices.DeleteFunc(p.back, gone)
		p.fwd = slices.DeleteFunc(p.fwd, gone)
	}
}

// sizeKey identifies a directory across filesystems for the size cache.
func sizeKey(f vfs.FS, p string) string {
	// preview rows have no filesystem behind them; their key matches no cached size
	if f == nil {
		return "\x00" + p
	}

	return f.ID() + "\x00" + p
}

func (m *Model) knownSize(f vfs.FS) func(vfs.Entry) (int64, bool) {
	return func(e vfs.Entry) (int64, bool) {
		s, ok := m.sizes[sizeKey(f, e.Path)]

		return s.bytes, ok && !s.running && s.err == nil
	}
}

func (m *Model) rebuild(i int) {
	p := m.panels[i]

	if p.fs == nil {
		return
	}

	p.rebuild(m.knownSize(p.fs))
}

func sizeWorkers(f vfs.FS) int {
	if f.Kind() == vfs.KindLocal {
		return runtime.GOMAXPROCS(0)
	}

	return 2
}

// calcSizes computes directory sizes one after another in a goroutine, streaming each
// result, so "all sizes" on a big directory cannot spawn unbounded walkers.
func (m *Model) calcSizes(f vfs.FS, dirs []string) tea.Cmd {
	if len(dirs) == 0 {
		return nil
	}

	for _, d := range dirs {
		m.sizes[sizeKey(f, d)] = sizeState{running: true}
	}

	m.retain(f)

	disk := m.cfg.DirSize == config.DirSizeDisk
	send := m.send
	ctx := m.ctx

	go func() {
		defer send(releaseMsg{fs: f})

		for _, d := range dirs {
			r, err := dirsize.Calc(ctx, f, d, sizeWorkers(f), disk)
			send(sizeMsg{key: sizeKey(f, d), result: r, err: err})
		}
	}()

	return nil
}

func (m *Model) errorToast(err error) tea.Cmd {
	return m.toast(err.Error(), levelError)
}

func (m *Model) infof(format string, args ...any) tea.Cmd {
	return m.toast(fmt.Sprintf(format, args...), levelInfo)
}
