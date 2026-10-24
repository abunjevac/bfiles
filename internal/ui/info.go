package ui

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/abunjevac/bfiles/internal/vfs"
)

// typeTimeout bounds the `file` command used to describe local files.
const typeTimeout = 2 * time.Second

// labelSize is the size row of the info dialog, whose value depends on the entry kind.
const labelSize = "Size"

// infoLoadedMsg delivers the slow parts of the info dialog.
type infoLoadedMsg struct {
	details vfs.Details
	kind    string
	held    []vfs.FS
}

// infoDialog shows everything known about one entry.
type infoDialog struct {
	fs      vfs.FS
	entry   vfs.Entry
	where   string
	details *vfs.Details
	kind    string
}

func (m *Model) infoDialog() tea.Cmd {
	p := m.panel()

	e, ok := p.current()
	if !ok || isParent(e) {
		return nil
	}

	d := &infoDialog{fs: p.fs, entry: e, where: m.placeText(p.fs, path.Dir(e.Path))}

	m.push(d)

	var cmds []tea.Cmd

	if e.IsDir && !e.IsLink {
		if _, known := m.sizes[sizeKey(p.fs, e.Path)]; !known {
			cmds = append(cmds, m.calcSizes(p.fs, []string{e.Path}))
		}
	}

	f := p.fs
	held := m.hold(f)
	ctx := m.ctx

	cmds = append(cmds, func() tea.Msg {
		msg := infoLoadedMsg{kind: describeKind(ctx, f, e), held: held}

		if dt, ok := f.(vfs.Detailer); ok {
			msg.details, _ = dt.Details(ctx, e.Path)
		}

		return msg
	})

	return tea.Batch(cmds...)
}

// describeKind names the type of an entry: `file -b` for local files when available,
// otherwise a content sniff of the first bytes.
func describeKind(ctx context.Context, f vfs.FS, e vfs.Entry) string {
	switch {
	case e.IsLink:
		return "symbolic link"
	case e.IsDir:
		return "directory"
	case !e.Mode.IsRegular():
		return e.Mode.Type().String()
	}

	if lp, ok := localPathOf(f, e.Path); ok {
		if kind := fileCommand(ctx, lp); kind != "" {
			return kind
		}
	}

	r, err := f.Open(ctx, e.Path)
	if err != nil {
		return "file"
	}

	defer r.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(r, head)

	return http.DetectContentType(head[:n])
}

func fileCommand(ctx context.Context, p string) string {
	if _, err := exec.LookPath("file"); err != nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(ctx, typeTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "file", "-b", "--", p).Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}

func (d *infoDialog) place() placement { return placeCenter }

func (d *infoDialog) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	if loaded, ok := msg.(infoLoadedMsg); ok {
		for _, f := range loaded.held {
			m.release(f)
		}

		d.details = &loaded.details
		d.kind = loaded.kind

		return false, nil
	}

	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}

	switch key := k.String(); {
	case isCancel(key) || key == keyEnter || key == "q" || key == "i":
		return true, nil
	case key == "y":
		return true, tea.Batch(tea.SetClipboard(d.entry.Path), m.toast("copied "+d.entry.Path, levelInfo))
	}

	return false, nil
}

func (d *infoDialog) view(m *Model, w, _ int) string {
	bw := boxWidth(w, 76)
	rows := d.rows(m)
	lines := make([]string, 0, len(rows)+2)

	for _, r := range rows {
		lines = append(lines, m.st.boxMuted.Render(fmt.Sprintf("%-10s", r[0]))+m.st.boxText.Render(r[1]))
	}

	lines = append(lines, "", m.keyHint("y", "copy path", "Esc", "close"))

	return m.renderBox("Info · "+path.Base(d.entry.Name), lines, bw)
}

// rows lists label/value pairs; values still loading show a spinner.
func (d *infoDialog) rows(m *Model) [][2]string {
	e := d.entry
	loading := spinner()

	kind := d.kind
	if d.details == nil {
		kind = loading
	}

	rows := [][2]string{
		{"Name", path.Base(e.Name)},
		{"In", d.where},
		{"Type", kind},
	}

	rows = append(rows, d.sizeRows(m)...)
	rows = append(rows, [2]string{"Mode", fmt.Sprintf("%s  %04o", e.Mode.String(), toOctal(e.Mode))})

	if e.IsLink {
		rows = append(rows, [2]string{"Link to", e.Link})
	}

	rows = append(rows, [2]string{"Modified", fullTime(e.ModTime)})

	return append(rows, d.detailRows(loading)...)
}

func (d *infoDialog) sizeRows(m *Model) [][2]string {
	e := d.entry

	if !e.IsDir || e.IsLink {
		rows := [][2]string{{labelSize, exactAndHuman(e.Size)}}

		if e.Disk > 0 && e.Disk != e.Size {
			rows = append(rows, [2]string{"On disk", exactAndHuman(e.Disk)})
		}

		return rows
	}

	s, ok := m.sizes[sizeKey(d.fs, e.Path)]

	switch {
	case !ok || s.running:
		return [][2]string{{labelSize, spinner() + " computing"}}
	case s.err != nil:
		return [][2]string{{labelSize, s.err.Error()}}
	}

	return [][2]string{
		{labelSize, exactAndHuman(s.bytes)},
		{"Contents", fmt.Sprintf("%s files, %s directories", groupDigits(int64(s.files)), groupDigits(int64(max(s.dirs-1, 0))))},
	}
}

func (d *infoDialog) detailRows(loading string) [][2]string {
	if d.details == nil {
		return [][2]string{{"Owner", loading}}
	}

	dt := d.details

	var rows [][2]string

	if dt.Owner != "" {
		rows = append(rows, [2]string{"Owner", dt.Owner + " : " + dt.Group})
	}

	if !dt.Access.IsZero() {
		rows = append(rows, [2]string{"Accessed", fullTime(dt.Access)})
	}

	if !dt.Change.IsZero() {
		rows = append(rows, [2]string{"Changed", fullTime(dt.Change)})
	}

	if dt.Inode != 0 {
		links := "links"
		if dt.Links == 1 {
			links = "link"
		}

		rows = append(rows, [2]string{"Inode", fmt.Sprintf("%d · %d %s", dt.Inode, dt.Links, links)})
	}

	return rows
}

func exactAndHuman(n int64) string {
	if n < 1024 {
		return groupDigits(n) + " bytes"
	}

	return fmt.Sprintf("%s bytes (%s)", groupDigits(n), humanSize(n))
}

func fullTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}

	return t.Format("2006-01-02 15:04:05 MST")
}
