package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/abunjevac/bfiles/internal/vfs"
)

// The grid has a row per class plus a row of special flags; the special flags line up
// with the execute bits they modify (setuid with owner, setgid with group, sticky
// with others), like the leading digit of an octal mode.
//
//nolint:gochecknoglobals // static labels
var (
	permRows = []string{"owner", "group", "others", "special"}
	permCols = []string{"read", "write", "exec"}
	specCols = []string{"setuid", "setgid", "sticky"}
)

// specialRow is the grid row of the setuid/setgid/sticky flags.
const specialRow = 3

// specialBits are the setuid, setgid and sticky flags.
const specialBits = fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky

// chmodDialog edits permission bits as a grid of checkboxes or as typed octal digits.
type chmodDialog struct {
	targets   []vfs.Entry
	mode      fs.FileMode // permission and special bits only
	row, col  int         // the row after the grid is the recursive checkbox
	hasDirs   bool
	recursive bool
	typed     string // octal digits being typed; empty when not typing
	onApply   func(m *Model, mode fs.FileMode, recursive bool) tea.Cmd
}

func (m *Model) chmodDialog() tea.Cmd {
	p := m.panel()
	targets := p.targets()

	if len(targets) == 0 {
		return nil
	}

	d := &chmodDialog{targets: targets, mode: targets[0].Mode & (fs.ModePerm | specialBits)}

	for _, e := range targets {
		d.hasDirs = d.hasDirs || (e.IsDir && !e.IsLink)
	}

	f := p.fs

	d.onApply = func(m *Model, mode fs.FileMode, recursive bool) tea.Cmd {
		return m.quickOp(f, "", func(ctx context.Context) error {
			return chmodAll(ctx, f, targets, mode, recursive)
		})
	}

	m.push(d)

	return nil
}

// toOctal and fromOctal convert between FileMode and the familiar 4-digit octal form.
func toOctal(mode fs.FileMode) uint32 {
	o := uint32(mode.Perm())

	if mode&fs.ModeSetuid != 0 {
		o |= 0o4000
	}

	if mode&fs.ModeSetgid != 0 {
		o |= 0o2000
	}

	if mode&fs.ModeSticky != 0 {
		o |= 0o1000
	}

	return o
}

func fromOctal(o uint32) fs.FileMode {
	mode := fs.FileMode(o & 0o777)

	if o&0o4000 != 0 {
		mode |= fs.ModeSetuid
	}

	if o&0o2000 != 0 {
		mode |= fs.ModeSetgid
	}

	if o&0o1000 != 0 {
		mode |= fs.ModeSticky
	}

	return mode
}

// chmodAll sets mode (permission and special bits) on every target and, if recursive,
// on everything below directory targets. Symlinks are skipped: changing them would
// change their target.
func chmodAll(ctx context.Context, f vfs.FS, targets []vfs.Entry, mode fs.FileMode, recursive bool) error {
	var errs []error

	apply := func(e vfs.Entry) {
		if e.IsLink {
			return
		}

		if err := f.Chmod(ctx, e.Path, mode); err != nil {
			errs = append(errs, err)
		}
	}

	for _, t := range targets {
		if !recursive || !t.IsDir || t.IsLink {
			apply(t)

			continue
		}

		err := vfs.Walk(ctx, f, t.Path, func(e vfs.Entry, err error) error {
			if err != nil {
				errs = append(errs, err)

				return nil
			}

			apply(e)

			return nil
		})
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (d *chmodDialog) place() placement { return placeCenter }

func (d *chmodDialog) lastRow() int {
	if d.hasDirs {
		return specialRow + 1
	}

	return specialRow
}

func (d *chmodDialog) bit(row, col int) fs.FileMode {
	if row == specialRow {
		return []fs.FileMode{fs.ModeSetuid, fs.ModeSetgid, fs.ModeSticky}[col]
	}

	return 1 << (8 - row*3 - col)
}

func (d *chmodDialog) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}

	key := k.String()

	if d.typingKey(key) {
		return false, nil
	}

	switch {
	case isCancel(key):
		return true, nil
	case key == keyEnter:
		return true, d.onApply(m, d.mode, d.recursive)
	case key == keySpace || key == "x":
		d.typed = ""
		d.toggle()
	default:
		d.moveCursor(key)
	}

	return false, nil
}

// typingKey handles octal digits and the keys that edit or abandon them.
func (d *chmodDialog) typingKey(key string) bool {
	switch {
	case len(key) == 1 && key[0] >= '0' && key[0] <= '7':
		d.typeDigit(key[0])
	case d.typed != "" && isCancel(key):
		d.typed = ""
	case d.typed != "" && key == keyBackspace:
		d.typed = d.typed[:len(d.typed)-1]
	default:
		return false
	}

	return true
}

// typeDigit collects up to four octal digits; three set the permissions, a fourth
// (leading) digit sets setuid/setgid/sticky as well.
func (d *chmodDialog) typeDigit(c byte) {
	if len(d.typed) == 4 {
		d.typed = ""
	}

	d.typed += string(c)

	if len(d.typed) < 3 {
		return
	}

	o, err := strconv.ParseUint(d.typed, 8, 32)
	if err != nil {
		return
	}

	if len(d.typed) == 3 {
		d.mode = d.mode&specialBits | fromOctal(uint32(o))
	} else {
		d.mode = fromOctal(uint32(o))
	}
}

func (d *chmodDialog) toggle() {
	if d.row > specialRow {
		d.recursive = !d.recursive

		return
	}

	d.mode ^= d.bit(d.row, d.col)
}

func (d *chmodDialog) moveCursor(key string) {
	switch key {
	case keyUp, "k":
		d.row = max(d.row-1, 0)
	case keyDown, "j", keyTab:
		d.row = min(d.row+1, d.lastRow())
	case keyLeft, "h":
		d.col = max(d.col-1, 0)
	case keyRight, "l":
		d.col = min(d.col+1, len(permCols)-1)
	}
}

func (d *chmodDialog) view(m *Model, w, _ int) string {
	st := m.st
	lines := []string{d.headerLine(m, permCols)}

	for r := range permRows {
		if r == specialRow {
			lines = append(lines, "", d.headerLine(m, specCols))
		}

		lines = append(lines, d.gridRow(m, r))
	}

	if d.hasDirs {
		label := checkbox(d.recursive) + " apply to directory contents"

		if d.row > specialRow {
			lines = append(lines, "", st.boxCursor.Render(label))
		} else {
			lines = append(lines, "", st.boxText.Render(label))
		}
	}

	lines = append(lines, "", d.modeLine(m), "",
		m.keyHint("←↑↓→", "move", "␣", "toggle", "0-7", "octal", "⏎", "apply", "Esc", "cancel"))

	return m.renderBox("Permissions · "+describe(d.targets), lines, boxWidth(w, 56))
}

func (d *chmodDialog) headerLine(m *Model, cols []string) string {
	var sb strings.Builder

	sb.WriteString(m.st.boxMuted.Render(fmt.Sprintf("%-9s", "")))

	for _, c := range cols {
		sb.WriteString(m.st.boxMuted.Render(fmt.Sprintf("%-8s", c)))
	}

	return sb.String()
}

func (d *chmodDialog) gridRow(m *Model, r int) string {
	var sb strings.Builder

	name := permRows[r]
	if r == specialRow {
		name = ""
	}

	sb.WriteString(m.st.boxText.Render(fmt.Sprintf("%-9s", name)))

	for c := range permCols {
		box := " " + checkbox(d.mode&d.bit(r, c) != 0) + " "

		if d.row == r && d.col == c {
			sb.WriteString(m.st.boxCursor.Render(box) + m.st.boxText.Render("   "))
		} else {
			sb.WriteString(m.st.boxText.Render(box + "   "))
		}
	}

	return sb.String()
}

// modeLine shows the octal mode, or the digits being typed.
func (d *chmodDialog) modeLine(m *Model) string {
	octal := fmt.Sprintf("%04o", toOctal(d.mode))

	if d.typed != "" {
		octal = m.st.boxInput.Render(fmt.Sprintf("%-4s", d.typed))
	}

	return m.st.boxText.Render("mode ") + octal + m.st.boxMuted.Render("  "+d.mode.String())
}
