package ui

import (
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	writeFiles(t, dir, map[string]string{
		"alpha.txt":          "alpha",
		"beta.go":            "package beta\n",
		".hidden":            "secret",
		"docs/readme.md":     "# docs",
		".git/config":        "[core]",
		"node_modules/x.js":  "x",
		"zeta/deep/file.log": "log",
	})

	return dir
}

func TestStartupListsBothPanels(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	screen := h.screen()

	require.Contains(t, screen, "alpha.txt")
	require.Contains(t, screen, "docs")
	require.NotContains(t, screen, ".hidden", "dotfiles hidden by default")
	require.NotContains(t, screen, "node_modules", "special dirs hidden by default")
	require.Equal(t, dir, h.m.panels[1].dir, "one location opens in both panels")
}

func TestNavigateAndBack(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.m.panel().selectName("docs")
	h.keys("l")

	require.Equal(t, filepath.Join(dir, "docs"), h.m.panel().dir)
	require.Contains(t, h.screen(), "readme.md")

	h.keys("h")

	require.Equal(t, dir, h.m.panel().dir)
	require.Equal(t, "docs", h.cursorName(), "cursor returns to the dir we came from")
}

func TestToggleHiddenAndSpecial(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.keys(".")
	require.Contains(t, h.screen(), ".hidden")
	require.NotContains(t, h.screen(), ".git ", ".git stays hidden: it is a special dir")

	h.keys(",")
	require.Contains(t, h.screen(), "node_modules")
	require.Contains(t, h.screen(), ".git")
}

func TestStaleListingIgnored(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)
	p := h.m.panel()

	stale := listedMsg{panel: h.m.active, reqID: p.reqID - 1, fs: p.fs, dir: "/nowhere"}
	h.m.retain(p.fs)
	h.update(stale)

	require.Equal(t, dir, p.dir)
}

func TestSelectionKeys(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)
	p := h.m.panel()

	p.selectName("alpha.txt")
	h.keys("v")
	require.Len(t, p.selected, 1)
	require.Equal(t, "beta.go", h.cursorName(), "toggle moves down")

	h.keys("shift+down")
	require.Len(t, p.selected, 2)

	h.keys("esc")
	require.Empty(t, p.selected)

	h.keys("*")
	require.Len(t, p.selected, len(p.view)-1, "invert selects everything but ..")
}

func TestQuickFilter(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.keys("/")
	h.typeText("bet")

	p := h.m.panel()

	require.Equal(t, "bet", p.filter)
	require.Equal(t, []string{"..", "beta.go"}, viewNames(p))

	h.keys("esc")
	require.Contains(t, viewNames(p), "alpha.txt")
}

func TestWhichKeyPopup(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.keys("space")
	h.waitFor(func() bool { return h.m.whichKey }, "which-key popup")

	screen := h.screen()
	require.Contains(t, screen, "copy")
	require.Contains(t, screen, "+archive")

	h.keys("esc")
	require.False(t, h.m.whichKey)
	require.Empty(t, h.m.seq)
}

func TestCopyToOtherPanel(t *testing.T) {
	t.Parallel()

	src := fixture(t)
	dst := t.TempDir()
	h := newHarness(t, src, dst)

	h.m.panel().selectName("alpha.txt")
	h.keys("f5")
	require.IsType(t, &inputDialog{}, h.m.top())

	h.keys("enter")
	h.waitFor(func() bool { return exists(filepath.Join(dst, "alpha.txt")) }, "copied file")
	h.waitFor(func() bool { return h.m.queue.Pending() == 0 }, "queue idle")

	h.waitFor(func() bool {
		for _, e := range h.m.panels[1].view {
			if e.Name == "alpha.txt" {
				return true
			}
		}

		return false
	}, "destination panel reloaded")
}

func TestDeleteWithConfirm(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.m.panel().selectName("zeta")
	h.keys("f8")
	require.IsType(t, &confirmDialog{}, h.m.top())

	h.keys("d")
	h.waitFor(func() bool { return !exists(filepath.Join(dir, "zeta")) }, "deleted dir")
}

func TestMkdirAndRename(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.keys("f7")
	h.typeText("newdir")
	h.keys("enter")
	h.waitFor(func() bool { return h.cursorName() == "newdir" }, "cursor on new dir")

	h.keys("shift+f6")
	h.keys("ctrl+u")
	h.typeText("renamed")
	h.keys("enter")
	h.waitFor(func() bool { return h.cursorName() == "renamed" }, "cursor on renamed dir")

	require.DirExists(t, filepath.Join(dir, "renamed"))
}

func TestRenameRefusesOverwrite(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.m.panel().selectName("alpha.txt")
	h.keys("shift+f6", "ctrl+e", "ctrl+u")
	h.typeText("beta.go")
	h.keys("enter")
	h.settle()

	data, err := os.ReadFile(filepath.Join(dir, "beta.go"))
	require.NoError(t, err)
	require.Equal(t, "package beta\n", string(data))
	require.FileExists(t, filepath.Join(dir, "alpha.txt"))
}

func TestSearchPanel(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.keys("space", "f")
	h.typeText("*.log")
	h.keys("enter", "s")

	h.waitFor(func() bool { return searchDone(h.m.panel()) }, "search done")
	require.Contains(t, h.screen(), "zeta/deep/file.log")

	h.keys("enter")
	require.Equal(t, filepath.Join(dir, "zeta", "deep"), h.m.panel().dir)
	require.Equal(t, "file.log", h.cursorName())
	require.Nil(t, h.m.panel().search)
}

func TestQuickView(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.m.panel().selectName("beta.go")
	h.keys("p")
	h.waitFor(func() bool { return h.m.previewRes != nil }, "preview")

	require.Contains(t, h.screen(), "package beta")
}

func TestQuitWithPendingJobsAsks(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.keys("ctrl+q")
	require.True(t, h.quit, "no jobs: quit immediately")
}

func TestSettingsSavePreservesComments(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.keys("f9")
	require.IsType(t, &formDialog{}, h.m.top())

	h.keys("right") // theme → next
	h.keys("s")
	h.waitFor(func() bool { return h.m.cfg.Theme != "tokyonight" }, "config reloaded")

	data, err := os.ReadFile(h.m.bundle.ConfigPath())
	require.NoError(t, err)
	require.Contains(t, string(data), "# Theme name")
}

func viewNames(p *Panel) []string {
	out := make([]string, len(p.view))

	for i, e := range p.view {
		out[i] = e.Name
	}

	return out
}

func TestWordsRoundTrip(t *testing.T) {
	t.Parallel()

	for _, words := range [][]string{
		{"konsole", "-e", "{cmd}"},
		{"my editor", "it's", ""},
		{`say "hi"`},
	} {
		got, err := splitWords(joinWords(words))
		require.NoError(t, err)
		require.Equal(t, words, got)
	}

	_, err := splitWords(`'open`)
	require.Error(t, err)
}

func exists(p string) bool {
	_, err := os.Stat(p)

	return err == nil
}

func searchDone(p *Panel) bool {
	return p.search != nil && !p.search.running
}

func TestQuitFromDialog(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.keys("f7")
	require.NotNil(t, h.m.top())

	h.keys("ctrl+q")
	require.True(t, h.quit, "ctrl+q quits even with a dialog open")
}

func TestBrokenKeymapKeepsCurrent(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)
	before := h.m.km

	require.NoError(t, os.WriteFile(h.m.bundle.KeymapPath(), []byte(`copy = ["x"]`+"\n"+`move = ["x"]`), 0o600))

	h.run(h.m.reloadConfigCmd())
	h.waitFor(func() bool { return len(h.m.toasts) > 0 }, "error toast")

	require.Same(t, before, h.m.km)
}

func TestQueueWindowKeys(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, t.TempDir())

	h.keys("f2")
	require.True(t, h.m.queueOpen)
	require.True(t, h.m.queueFocus)

	h.keys("+", "+")
	require.Equal(t, 3, h.m.queue.Parallel())
	require.Nil(t, h.m.top(), "+ goes to the queue, not to select-by-pattern")

	h.keys("-")
	require.Equal(t, 2, h.m.queue.Parallel())

	h.keys("esc")
	require.False(t, h.m.queueOpen, "esc closes the queue card")
}

func TestChmodDialog(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	require.NoError(t, os.Chmod(filepath.Join(dir, "alpha.txt"), 0o644))
	h.keys("ctrl+r")
	h.m.panel().selectName("alpha.txt")

	h.keys("space", "p")
	require.IsType(t, &chmodDialog{}, h.m.top())

	// owner exec on, others read off
	h.keys("right", "right", "space", "down", "down", "left", "left", "space", "enter")
	h.waitFor(func() bool {
		fi, err := os.Stat(filepath.Join(dir, "alpha.txt"))

		return err == nil && fi.Mode().Perm() == 0o740
	}, "mode 0740")
}

func TestChmodRecursive(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.m.panel().selectName("zeta")
	h.keys("space", "p")

	d, ok := h.m.top().(*chmodDialog)
	require.True(t, ok)
	require.True(t, d.hasDirs)

	d.mode = 0o750

	h.keys("down", "down", "down", "down", "space", "enter")
	h.waitFor(func() bool {
		fi, err := os.Stat(filepath.Join(dir, "zeta", "deep", "file.log"))

		return err == nil && fi.Mode().Perm() == 0o750
	}, "recursive mode")
}

func TestHoldOnFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	ok := holdOnFailure([]string{"true"})
	require.NoError(t, exec.CommandContext(ctx, ok[0], ok[1:]...).Run()) //nolint:gosec // fixed test command

	// a failing command prints its status and waits for Enter (stdin closed here)
	failing := holdOnFailure([]string{"sh", "-c", "exit 7"})
	out, _ := exec.CommandContext(ctx, failing[0], failing[1:]...).CombinedOutput() //nolint:gosec // fixed test command
	require.Contains(t, string(out), "sh exited with status 7")
}

func TestSearchSettingsRemembered(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.keys("space", "f")
	h.typeText("*.log")
	h.keys("enter", "down", "down", "space", "s") // case sensitive on
	h.waitFor(func() bool { return searchDone(h.m.panel()) }, "search done")

	h.keys("space", "f")

	d, ok := h.m.top().(*formDialog)
	require.True(t, ok)
	require.Equal(t, "*.log", d.input.String(), "pattern prefilled")
	require.Equal(t, valueTrue, d.fields[2].value, "options kept")
}

func TestSettingsSizeField(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.keys("f9")

	d, ok := h.m.top().(*formDialog)
	require.True(t, ok)

	d.sel = len(d.fields) - 1
	require.Equal(t, "1M", d.fields[d.sel].value)

	h.keys("enter", "ctrl+u")
	h.typeText("512k")
	h.keys("enter", "s")
	h.waitFor(func() bool { return h.m.cfg.Preview.MaxBytes == 512<<10 }, "max size applied")

	data, err := os.ReadFile(h.m.bundle.ConfigPath())
	require.NoError(t, err)
	require.Contains(t, string(data), `max_bytes = "512K"`)
}

func TestChmodOctalEntry(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.m.panel().selectName("zeta")
	h.keys("space", "p")
	h.keys("1", "7", "5", "5")

	d, ok := h.m.top().(*chmodDialog)
	require.True(t, ok)
	require.Equal(t, fs.ModeSticky|0o755, d.mode, "4 digits set the sticky bit too")

	h.keys("enter")
	h.waitFor(func() bool {
		fi, err := os.Stat(filepath.Join(dir, "zeta"))

		return err == nil && fi.Mode()&(fs.ModePerm|fs.ModeSticky) == fs.ModeSticky|0o755
	}, "sticky 1755")
}

func TestQuickViewOfDirectoryRenders(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)
	p := h.m.panel()

	p.selectName("alpha.txt")
	h.keys("v")
	p.selectName("zeta") // has a subdirectory, whose size cell needs the filesystem
	h.keys("p")
	h.waitFor(func() bool { return h.m.previewRes != nil }, "preview")

	require.NotPanics(t, func() { _ = h.screen() })
	require.Contains(t, h.screen(), "deep")

	h.keys("space")
	h.waitFor(func() bool { return h.m.whichKey }, "which-key over quick view")
	require.NotPanics(t, func() { _ = h.screen() })

	h.keys("esc", "f3")
	require.NotPanics(t, func() { _ = h.screen() })
}

func TestInfoDialog(t *testing.T) {
	t.Parallel()

	dir := fixture(t)
	h := newHarness(t, dir, dir)

	h.m.panel().selectName("zeta")
	h.keys("i")
	h.waitFor(func() bool {
		d, ok := h.m.top().(*infoDialog)

		return ok && d.details != nil && !h.m.sizes[sizeKey(h.m.panel().fs, filepath.Join(dir, "zeta"))].running
	}, "info loaded")

	screen := h.screen()

	require.Contains(t, screen, "directory")
	require.Contains(t, screen, "3 bytes")
	require.Contains(t, screen, "1 files, 1 directories")
	require.Contains(t, screen, "Owner")
	require.Contains(t, screen, "Inode")

	h.keys("esc")
	require.Nil(t, h.m.top())
}

func TestExactSizesToggle(t *testing.T) {
	t.Parallel()

	dir := fixture(t)

	writeFiles(t, dir, map[string]string{"big.bin": strings.Repeat("x", 1234567)})

	h := newHarness(t, dir, dir)

	require.Contains(t, h.screen(), "1.2 M")

	h.keys("space", "v", "b")
	require.True(t, h.m.panel().exactSizes)
	require.Contains(t, h.screen(), "1 234 567")
	require.False(t, h.m.other().exactSizes, "toggle is per panel")
}
