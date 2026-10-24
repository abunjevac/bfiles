package ui

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/abunjevac/bfiles/internal/config"
)

// fieldKind is the editor type of a form field.
type fieldKind int

const (
	fieldText fieldKind = iota
	fieldBool
	fieldEnum
	fieldInt
	fieldList // a list of words, edited as shell-quoted text
	fieldSize // a byte size with an optional unit (512K, 1M)
)

// field is one row of a formDialog. Values are kept as strings for editing.
type field struct {
	key     string
	label   string
	kind    fieldKind
	value   string
	options []string // fieldEnum
	min     int
	max     int
	help    string
	section string
}

// Form values of boolean fields.
const (
	valueTrue  = "true"
	valueFalse = "false"
)

// Form section names shared by dialogs.
const (
	sectionOptions = "Options"
	sectionPanels  = "Panels"
	sectionBehave  = "Behaviour"
	sectionProgs   = "Programs"
	sectionSSH     = "SSH"
)

func (f *field) boolValue() bool { return f.value == valueTrue }

// formDialog edits a set of typed fields.
type formDialog struct {
	title       string
	fields      []field
	sel         int
	offset      int
	editing     bool
	input       textInput
	err         string
	width       int
	submitLabel string // label of the submit key hint
	onSubmit    func(m *Model, fields []field) (cmd tea.Cmd, errText string)
	onChange    func(m *Model, f *field)
	onCancel    func(m *Model) tea.Cmd
	extraKey    func(m *Model, key string) (handled bool, cmd tea.Cmd)
}

func (d *formDialog) place() placement { return placeCenter }

func (d *formDialog) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	if d.editing {
		d.updateEditing(m, msg)

		return false, nil
	}

	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}

	key := k.String()

	if d.extraKey != nil {
		if handled, cmd := d.extraKey(m, key); handled {
			return true, cmd
		}
	}

	switch {
	case isCancel(key) || key == "q":
		if d.onCancel != nil {
			return true, d.onCancel(m)
		}

		return true, nil
	case key == "ctrl+s" || key == "s":
		return d.submit(m)
	}

	d.navigate(m, key)

	return false, nil
}

// updateEditing feeds the inline editor of the selected text field.
func (d *formDialog) updateEditing(m *Model, msg tea.Msg) {
	k, ok := msg.(tea.KeyPressMsg)

	switch {
	case ok && k.String() == keyEnter:
		f := &d.fields[d.sel]

		if err := d.commitEdit(f); err != "" {
			d.err = err

			return
		}

		d.editing = false
		d.err = ""

		if d.onChange != nil {
			d.onChange(m, f)
		}
	case ok && k.String() == keyEsc:
		d.editing = false
		d.err = ""
	default:
		d.input.update(msg)
	}
}

func (d *formDialog) submit(m *Model) (bool, tea.Cmd) {
	cmd, errText := d.onSubmit(m, d.fields)
	if errText != "" {
		d.err = errText

		return false, nil
	}

	return true, cmd
}

// navigate moves between fields and changes the selected one.
func (d *formDialog) navigate(m *Model, key string) {
	f := &d.fields[d.sel]

	switch key {
	case keyUp, "k", keyShiftTab:
		d.sel = (d.sel + len(d.fields) - 1) % len(d.fields)
	case keyDown, "j", keyTab:
		d.sel = (d.sel + 1) % len(d.fields)
	case keyLeft, "h":
		d.cycle(m, f, -1)
	case keyRight, "l", keySpace:
		d.cycle(m, f, 1)
	case keyEnter:
		if f.kind == fieldBool || f.kind == fieldEnum {
			d.cycle(m, f, 1)

			return
		}

		d.editing = true
		d.input = newInput(f.value)
	}
}

func (d *formDialog) cycle(m *Model, f *field, delta int) {
	switch f.kind {
	case fieldBool:
		f.value = strconv.FormatBool(!f.boolValue())
	case fieldEnum:
		i := slices.Index(f.options, f.value)
		f.value = f.options[(i+delta+len(f.options))%len(f.options)]
	case fieldInt:
		n, _ := strconv.Atoi(f.value)
		f.value = strconv.Itoa(max(min(n+delta, f.max), f.min))
	default:
		return
	}

	if d.onChange != nil {
		d.onChange(m, f)
	}
}

func (d *formDialog) commitEdit(f *field) string {
	v := strings.TrimSpace(d.input.String())

	switch f.kind { //nolint:exhaustive // only typed text fields need validation
	case fieldInt:
		n, err := strconv.Atoi(v)
		if err != nil || n < f.min || n > f.max {
			return fmt.Sprintf("%s must be a number between %d and %d", f.label, f.min, f.max)
		}
	case fieldList:
		if _, err := splitWords(v); err != nil {
			return err.Error()
		}
	case fieldSize:
		size, err := config.ParseByteSize(v)
		if err != nil {
			return err.Error()
		}

		v = size.String()
	}

	f.value = v

	return ""
}

func (d *formDialog) view(m *Model, w, h int) string {
	bw := boxWidth(w, max(d.width, 76))
	inner := bw - 4
	rows := max(min(h-10, len(d.fields)+countSections(d.fields)), 3)

	body, selLine := d.body(m, inner)

	if selLine < d.offset {
		d.offset = selLine
	}

	if selLine >= d.offset+rows {
		d.offset = selLine - rows + 1
	}

	end := min(d.offset+rows, len(body))
	lines := append([]string{}, body[d.offset:end]...)

	lines = append(lines, "")
	lines = append(lines, d.footer(m)...)

	return m.renderBox(d.title, lines, bw)
}

// body renders all fields with section headings and reports the selected line.
func (d *formDialog) body(m *Model, inner int) ([]string, int) {
	const labelW = 22

	var (
		body    []string
		section string
		selLine int
	)

	for i := range d.fields {
		f := &d.fields[i]

		if f.section != section {
			section = f.section

			if len(body) > 0 {
				body = append(body, "")
			}

			body = append(body, m.st.boxKey.Render(section))
		}

		if i == d.sel {
			selLine = len(body)
		}

		body = append(body, d.renderField(m, f, i == d.sel, labelW, inner))
	}

	return body, selLine
}

// footer always has one line for the field help or an error, so the dialog keeps
// its height while moving between fields.
func (d *formDialog) footer(m *Model) []string {
	var lines []string

	switch {
	case d.err != "":
		lines = append(lines, m.st.errorText.Background(m.st.overlay).Render(d.err))
	default:
		lines = append(lines, m.st.boxMuted.Render(d.fields[d.sel].help))
	}

	if d.editing {
		return append(lines, m.keyHint("⏎", "apply", "Esc", "discard"))
	}

	label := d.submitLabel
	if label == "" {
		label = "save"
	}

	return append(lines, m.keyHint("↑↓", "move", "⏎/←→", "change", "s", label, "Esc", "cancel"))
}

func (d *formDialog) renderField(m *Model, f *field, selected bool, labelW, inner int) string {
	label := fit("  "+f.label, labelW)
	valW := inner - labelW

	var val string

	switch {
	case selected && d.editing:
		// the leading space keeps the editor where the value is shown
		return m.st.boxText.Render(label+" ") + d.input.render(valW-1, m.st.boxInput, m.st.segAccent)
	case f.kind == fieldBool:
		if f.boolValue() {
			val = "● on"
		} else {
			val = "○ off"
		}
	case f.kind == fieldEnum:
		val = "‹ " + f.value + " ›"
	default:
		val = f.value
		if val == "" {
			val = "—"
		}
	}

	if selected {
		return m.st.boxCursor.Render(label) + m.st.boxCursor.Render(fit(" "+val, valW))
	}

	return m.st.boxText.Render(label) + m.st.boxMuted.Render(fit(" "+val, valW))
}

func countSections(fields []field) int {
	n, s := 0, ""

	for _, f := range fields {
		if f.section != s {
			s = f.section
			n += 2
		}
	}

	return n
}

// splitWords splits shell-style text into words, honouring single and double quotes.
func splitWords(s string) ([]string, error) {
	var sp wordSplitter

	for _, r := range s {
		sp.feed(r)
	}

	if sp.quote != 0 {
		return nil, errors.New("unterminated quote")
	}

	sp.flush()

	return sp.words, nil
}

type wordSplitter struct {
	words []string
	cur   strings.Builder
	quote rune
	have  bool // distinguishes an empty quoted word from no word
}

func (sp *wordSplitter) feed(r rune) {
	switch {
	case sp.quote != 0 && r == sp.quote:
		sp.quote = 0
	case sp.quote != 0:
		sp.cur.WriteRune(r)
	case r == '\'' || r == '"':
		sp.quote = r
		sp.have = true
	case r == ' ' || r == '\t':
		sp.flush()
	default:
		sp.cur.WriteRune(r)
		sp.have = true
	}
}

func (sp *wordSplitter) flush() {
	if !sp.have {
		return
	}

	sp.words = append(sp.words, sp.cur.String())
	sp.cur.Reset()
	sp.have = false
}

// joinWords is the inverse of splitWords.
func joinWords(words []string) string {
	out := make([]string, len(words))

	for i, w := range words {
		if w == "" || strings.ContainsAny(w, " \t'\"") {
			out[i] = "'" + strings.ReplaceAll(w, "'", `'"'"'`) + "'"
		} else {
			out[i] = w
		}
	}

	return strings.Join(out, " ")
}

func itoa(n int) string { return strconv.Itoa(n) }
