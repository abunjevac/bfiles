package ui

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/fsnotify/fsnotify"

	"github.com/abunjevac/bfiles/internal/vfs"
)

// watchDebounce coalesces bursts of filesystem events (e.g. during a copy).
const watchDebounce = 250 * time.Millisecond

// watcher turns fsnotify events for shown local directories and the config dir into
// debounced messages.
type watcher struct {
	w         *fsnotify.Watcher
	send      func(tea.Msg)
	configDir string

	mu      sync.Mutex
	dirs    map[string]bool
	pending map[string]*time.Timer
}

// startWatcher sets up watching; failures only disable live refresh.
func (m *Model) startWatcher() tea.Cmd {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return m.toast("live refresh disabled: "+err.Error(), levelWarn)
	}

	w := &watcher{w: fw, send: m.send, configDir: m.bundle.Dir, dirs: map[string]bool{}, pending: map[string]*time.Timer{}}

	_ = fw.Add(m.bundle.Dir)
	_ = fw.Add(filepath.Join(m.bundle.Dir, "themes"))

	m.watch = w

	go w.loop()

	return nil
}

func (w *watcher) loop() {
	for {
		select {
		case ev, ok := <-w.w.Events:
			if !ok {
				return
			}

			w.handle(ev)
		case _, ok := <-w.w.Errors:
			if !ok {
				return
			}
		}
	}
}

func (w *watcher) handle(ev fsnotify.Event) {
	dir := filepath.Dir(ev.Name)

	if dir == w.configDir || dir == filepath.Join(w.configDir, "themes") {
		if strings.HasSuffix(ev.Name, ".toml") {
			w.debounce("\x00config", configChangedMsg{})
		}
	}

	w.mu.Lock()
	watched := w.dirs[dir]
	selfWatched := w.dirs[ev.Name]
	w.mu.Unlock()

	if watched {
		w.debounce(dir, dirChangedMsg{dir: dir})
	}

	// the shown directory itself was removed or renamed
	if selfWatched && ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
		w.debounce(ev.Name, dirChangedMsg{dir: ev.Name})
	}
}

func (w *watcher) debounce(key string, msg tea.Msg) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if t, ok := w.pending[key]; ok {
		t.Reset(watchDebounce)

		return
	}

	w.pending[key] = time.AfterFunc(watchDebounce, func() {
		w.mu.Lock()
		delete(w.pending, key)
		w.mu.Unlock()

		w.send(msg)
	})
}

// set replaces the watched panel directories.
func (w *watcher) set(dirs []string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	want := map[string]bool{}

	for _, d := range dirs {
		want[d] = true
	}

	themes := filepath.Join(w.configDir, "themes")

	for d := range w.dirs {
		// the config dirs stay watched even when a panel leaves them
		if !want[d] && d != w.configDir && d != themes {
			_ = w.w.Remove(d)
		}
	}

	for d := range want {
		if !w.dirs[d] {
			_ = w.w.Add(d)
		}
	}

	w.dirs = want
}

func (w *watcher) close() {
	_ = w.w.Close()
}

// syncWatches watches exactly the local directories the panels show.
func (m *Model) syncWatches() {
	if m.watch == nil {
		return
	}

	var dirs []string

	for _, p := range m.panels {
		if p.fs != nil && p.fs.Kind() == vfs.KindLocal && p.search == nil {
			dirs = append(dirs, p.dir)
		}
	}

	m.watch.set(dirs)
}
