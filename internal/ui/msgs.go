package ui

import (
	"github.com/abunjevac/bfiles/internal/config"
	"github.com/abunjevac/bfiles/internal/dirsize"
	"github.com/abunjevac/bfiles/internal/jobs"
	"github.com/abunjevac/bfiles/internal/keymap"
	"github.com/abunjevac/bfiles/internal/preview"
	"github.com/abunjevac/bfiles/internal/theme"
	"github.com/abunjevac/bfiles/internal/vfs"
)

// listedMsg delivers a directory listing for a panel navigation or reload.
type listedMsg struct {
	panel   int // Panel.id
	reqID   int
	fs      vfs.FS
	dir     string
	entries []vfs.Entry
	err     error
	// selectName puts the cursor on this entry; keepCursor preserves it (reloads).
	selectName string
	keepCursor bool
	history    bool // push the previous location onto the back stack
}

// openedFSMsg delivers a newly opened filesystem (archive or SSH) to navigate into.
type openedFSMsg struct {
	panel      int // Panel.id
	reqID      int // matches Panel.reqID unless the panel moved on meanwhile
	fs         vfs.FS
	dir        string
	selectName string
	err        error
}

// jobsMsg relays a queue event.
type jobsMsg jobs.Event

// sizeMsg delivers a computed directory size.
type sizeMsg struct {
	key    string
	result dirsize.Result
	err    error
}

// previewMsg delivers a quick-view or viewer preview.
type previewMsg struct {
	id     int
	path   string
	result preview.Result
	held   []vfs.FS
}

// previewTickMsg fires after the quick-view debounce delay.
type previewTickMsg struct{ id int }

// whichKeyTickMsg fires after the which-key delay for a pending key sequence.
type whichKeyTickMsg struct{ seq int }

// searchBatchMsg delivers search results.
type searchBatchMsg struct {
	panel   int // Panel.id
	id      int
	entries []vfs.Entry
}

// searchDoneMsg ends a search.
type searchDoneMsg struct {
	panel int // Panel.id
	id    int
	err   error
}

// toastExpireMsg removes expired toasts.
type toastExpireMsg struct{}

// opDoneMsg reports a quick synchronous operation (rename, mkdir, chmod) and the
// panels to reload.
type opDoneMsg struct {
	err        error
	fsID       string
	selectName string
	panel      int // Panel.id
	info       string
	held       []vfs.FS
}

// releaseMsg drops a filesystem reference from a background goroutine.
type releaseMsg struct{ fs vfs.FS }

// dirChangedMsg is sent by the watcher when a shown local directory changed.
type dirChangedMsg struct{ dir string }

// configChangedMsg is sent by the watcher when a file in the config dir changed.
type configChangedMsg struct{}

// configReloadedMsg delivers re-read configuration.
type configReloadedMsg struct {
	cfg     *config.Config
	km      *keymap.Keymap
	th      *theme.Theme
	errText string
}

// promptMsg asks the user for a secret or a confirmation on behalf of a goroutine.
type promptMsg struct {
	text    string
	confirm bool
	reply   chan promptReply
}

type promptReply struct {
	text string
	ok   bool
}

// editReadyMsg starts the editor on a local copy of a non-local file.
type editReadyMsg struct {
	panel  int
	fs     vfs.FS
	path   string
	local  string
	tmpDir string
	sum    string
	err    error
	held   []vfs.FS
}

// editDoneMsg reports that the editor exited.
type editDoneMsg struct {
	ready editReadyMsg
	err   error
}

// queueAutoCloseMsg hides the queue card a moment after the last job finished.
type queueAutoCloseMsg struct{}
