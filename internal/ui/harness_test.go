package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/config"
	"github.com/abunjevac/bfiles/internal/location"
)

// harness drives a Model without a terminal: commands run in goroutines and their
// messages are fed back through Update until the model settles.
type harness struct {
	t    *testing.T
	m    *Model
	msgs chan tea.Msg
	quit bool
}

func newHarness(t *testing.T, left, right string) *harness {
	t.Helper()

	bundle, err := config.Load(t.TempDir())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	h := &harness{t: t, msgs: make(chan tea.Msg)}
	h.m = newModel(ctx, bundle, Options{})
	// the same mailbox as in Run, delivering into an unbuffered channel like a
	// tea.Program does
	box := newMailbox(func(msg tea.Msg) { h.msgs <- msg })
	h.m.send = box.post

	go box.run()

	t.Cleanup(box.close)

	h.m.initial = [2]location.Spec{{Kind: location.Local, Path: left}, {Kind: location.Local, Path: right}}

	t.Cleanup(h.m.shutdown)

	h.update(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.run(h.m.Init())
	h.settle()

	return h
}

func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}

	go func() {
		if msg := cmd(); msg != nil {
			h.msgs <- msg
		}
	}()
}

func (h *harness) update(msg tea.Msg) {
	switch msg := msg.(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}

		return
	case tea.QuitMsg:
		h.quit = true

		return
	}

	_, cmd := h.m.Update(msg)
	h.run(cmd)
}

// settle processes messages until none arrive for a short while.
func (h *harness) settle() {
	h.settleFor(150 * time.Millisecond)
}

func (h *harness) settleFor(idle time.Duration) {
	deadline := time.After(10 * time.Second)

	for {
		select {
		case msg := <-h.msgs:
			if _, spin := msg.(spinTickMsg); spin {
				continue
			}

			h.update(msg)
		case <-time.After(idle):
			return
		case <-deadline:
			h.t.Fatal("model did not settle")
		}
	}
}

// waitFor processes messages until cond holds.
func (h *harness) waitFor(cond func() bool, what string) {
	h.t.Helper()

	deadline := time.After(10 * time.Second)

	for !cond() {
		select {
		case msg := <-h.msgs:
			h.update(msg)
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			h.t.Fatalf("timed out waiting for %s\n%s", what, h.screen())
		}
	}

	h.settle()
}

//nolint:gochecknoglobals // static test key table
var specialKeys = map[string]rune{
	"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "space": tea.KeySpace,
	"backspace": tea.KeyBackspace, "up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft,
	"right": tea.KeyRight, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "home": tea.KeyHome,
	"end": tea.KeyEnd, "insert": tea.KeyInsert, "delete": tea.KeyDelete,
	"f1": tea.KeyF1, "f2": tea.KeyF2, "f3": tea.KeyF3, "f4": tea.KeyF4, "f5": tea.KeyF5,
	"f6": tea.KeyF6, "f7": tea.KeyF7, "f8": tea.KeyF8, "f9": tea.KeyF9, "f10": tea.KeyF10,
}

func keyMsg(s string) tea.KeyPressMsg {
	var mod tea.KeyMod

	for {
		switch {
		case strings.HasPrefix(s, "ctrl+") && len(s) > 5:
			mod |= tea.ModCtrl
			s = s[5:]

			continue
		case strings.HasPrefix(s, "alt+") && len(s) > 4:
			mod |= tea.ModAlt
			s = s[4:]

			continue
		case strings.HasPrefix(s, "shift+") && len(s) > 6:
			mod |= tea.ModShift
			s = s[6:]

			continue
		}

		break
	}

	if code, ok := specialKeys[s]; ok {
		return tea.KeyPressMsg{Code: code, Mod: mod}
	}

	r, _ := utf8.DecodeRuneInString(s)

	if mod != 0 {
		return tea.KeyPressMsg{Code: r, Mod: mod}
	}

	return tea.KeyPressMsg{Code: r, Text: s}
}

// keys presses each key in turn, letting the model settle in between.
func (h *harness) keys(ks ...string) {
	for _, k := range ks {
		h.update(keyMsg(k))
		h.settle()
	}
}

// typeText sends each rune as a key press.
func (h *harness) typeText(s string) {
	for _, r := range s {
		h.update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	h.settle()
}

func (h *harness) screen() string {
	return ansi.Strip(h.m.render())
}

func (h *harness) cursorName() string {
	e, _ := h.m.panel().current()

	return e.Name
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		p := filepath.Join(root, name)

		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
}
