package ui

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/abunjevac/bfiles/internal/icons"
	"github.com/abunjevac/bfiles/internal/theme"
)

// styles holds every lipgloss style derived from the active theme, rebuilt on theme change.
type styles struct {
	bg, fg  color.Color
	overlay color.Color

	text       lipgloss.Style
	muted      lipgloss.Style
	accent     lipgloss.Style
	errorText  lipgloss.Style
	warnText   lipgloss.Style
	okText     lipgloss.Style
	selected   lipgloss.Style
	divider    lipgloss.Style
	cursor     lipgloss.Style
	cursorDim  lipgloss.Style
	headActive lipgloss.Style
	headIdle   lipgloss.Style
	status     lipgloss.Style
	statusKey  lipgloss.Style
	segAccent  lipgloss.Style
	segWarn    lipgloss.Style
	segError   lipgloss.Style
	segOK      lipgloss.Style

	box        lipgloss.Style
	boxTitle   lipgloss.Style
	boxText    lipgloss.Style
	boxMuted   lipgloss.Style
	boxKey     lipgloss.Style
	boxCursor  lipgloss.Style
	boxInput   lipgloss.Style
	boxButton  lipgloss.Style
	boxButtonA lipgloss.Style

	roles map[icons.Role]lipgloss.Style
}

func newStyles(t *theme.Theme) styles {
	c := lipgloss.Color
	fg := lipgloss.NewStyle().Foreground

	s := styles{
		bg:      c(t.Bg),
		fg:      c(t.Fg),
		overlay: c(t.OverlayBg),

		text:      fg(c(t.Fg)),
		muted:     fg(c(t.Muted)),
		accent:    fg(c(t.Accent)).Bold(true),
		errorText: fg(c(t.Error)),
		warnText:  fg(c(t.Warning)),
		okText:    fg(c(t.Success)),
		selected:  fg(c(t.Selected)).Bold(true),
		divider:   fg(c(t.Border)),

		cursor:     lipgloss.NewStyle().Background(c(t.CursorBg)),
		cursorDim:  lipgloss.NewStyle().Background(c(t.HeaderInactiveBg)),
		headActive: lipgloss.NewStyle().Background(c(t.HeaderActiveBg)).Foreground(c(t.HeaderActiveFg)).Bold(true),
		headIdle:   lipgloss.NewStyle().Background(c(t.HeaderInactiveBg)).Foreground(c(t.HeaderInactiveFg)),
		status:     lipgloss.NewStyle().Background(c(t.StatusBg)).Foreground(c(t.StatusFg)),
		statusKey:  lipgloss.NewStyle().Background(c(t.StatusBg)).Foreground(c(t.Accent)).Bold(true),
		segAccent:  lipgloss.NewStyle().Background(c(t.Accent)).Foreground(c(t.AccentFg)).Bold(true),
		segWarn:    lipgloss.NewStyle().Background(c(t.Warning)).Foreground(c(t.AccentFg)).Bold(true),
		segError:   lipgloss.NewStyle().Background(c(t.Error)).Foreground(c(t.AccentFg)).Bold(true),
		segOK:      lipgloss.NewStyle().Background(c(t.Success)).Foreground(c(t.AccentFg)).Bold(true),
	}

	ob := c(t.OverlayBg)

	s.box = lipgloss.NewStyle().
		Background(ob).
		Foreground(c(t.Fg)).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(c(t.Accent)).
		BorderBackground(ob).
		Padding(0, 1)
	s.boxTitle = lipgloss.NewStyle().Background(ob).Foreground(c(t.Accent)).Bold(true)
	s.boxText = lipgloss.NewStyle().Background(ob).Foreground(c(t.Fg))
	s.boxMuted = lipgloss.NewStyle().Background(ob).Foreground(c(t.Muted))
	s.boxKey = lipgloss.NewStyle().Background(ob).Foreground(c(t.Warning)).Bold(true)
	s.boxCursor = lipgloss.NewStyle().Background(c(t.CursorBg)).Foreground(c(t.Fg))
	s.boxInput = lipgloss.NewStyle().Background(c(t.CursorBg)).Foreground(c(t.Fg))
	s.boxButton = lipgloss.NewStyle().Background(c(t.HeaderInactiveBg)).Foreground(c(t.HeaderInactiveFg)).Padding(0, 1)
	s.boxButtonA = lipgloss.NewStyle().Background(c(t.Accent)).Foreground(c(t.AccentFg)).Bold(true).Padding(0, 1)

	s.roles = map[icons.Role]lipgloss.Style{
		icons.RoleFile:    fg(c(t.Fg)),
		icons.RoleDir:     fg(c(t.Dir)).Bold(true),
		icons.RoleSymlink: fg(c(t.Symlink)).Italic(true),
		icons.RoleExec:    fg(c(t.Exec)),
		icons.RoleArchive: fg(c(t.Archive)),
		icons.RoleImage:   fg(c(t.Image)),
		icons.RoleCode:    fg(c(t.Code)),
		icons.RoleDoc:     fg(c(t.Doc)),
	}

	return s
}
