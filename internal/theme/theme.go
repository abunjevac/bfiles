package theme

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"

	"github.com/BurntSushi/toml"
)

var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Theme assigns a colour to every semantic UI role. A colour is "#rrggbb", an ANSI
// palette index "0".."255", or "default" for the terminal's own default colour, which
// lets a theme follow the terminal's palette.
type Theme struct {
	Name string `toml:"-"`
	Dark bool   `toml:"dark"`

	Bg       string `toml:"bg"`
	Fg       string `toml:"fg"`
	Muted    string `toml:"muted"`
	Accent   string `toml:"accent"`
	AccentFg string `toml:"accent_fg"`
	CursorBg string `toml:"cursor_bg"`
	Selected string `toml:"selected"`
	Border   string `toml:"border"`

	Dir     string `toml:"dir"`
	Symlink string `toml:"symlink"`
	Exec    string `toml:"exec"`
	Archive string `toml:"archive"`
	Image   string `toml:"image"`
	Code    string `toml:"code"`
	Doc     string `toml:"doc"`

	Error   string `toml:"error"`
	Warning string `toml:"warning"`
	Success string `toml:"success"`

	HeaderActiveBg   string `toml:"header_active_bg"`
	HeaderActiveFg   string `toml:"header_active_fg"`
	HeaderInactiveBg string `toml:"header_inactive_bg"`
	HeaderInactiveFg string `toml:"header_inactive_fg"`
	StatusBg         string `toml:"status_bg"`
	StatusFg         string `toml:"status_fg"`
	OverlayBg        string `toml:"overlay_bg"`
}

// Parse decodes a theme TOML and validates that every colour role is set and well-formed.
func Parse(name, data string) (*Theme, error) {
	t := Theme{Name: name}

	meta, err := toml.Decode(data, &t)
	if err != nil {
		return nil, fmt.Errorf("decode-theme %s: %w", name, err)
	}

	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return nil, fmt.Errorf("theme %s: unknown key %s", name, undecoded[0])
	}

	v := reflect.ValueOf(t)
	typ := v.Type()

	for i := range typ.NumField() {
		f := typ.Field(i)

		if f.Type.Kind() != reflect.String || f.Name == "Name" {
			continue
		}

		if c := v.Field(i).String(); !ValidColor(c) {
			return nil, fmt.Errorf("theme %s: %s %q is not a colour (expected #rrggbb, 0..255 or default)", name, f.Tag.Get("toml"), c)
		}
	}

	return &t, nil
}

// DefaultColor is the colour value meaning "the terminal's default".
const DefaultColor = "default"

// ValidColor reports whether c is "#rrggbb", an ANSI index 0..255 or "default".
func ValidColor(c string) bool {
	if c == DefaultColor || hexColorRe.MatchString(c) {
		return true
	}

	n, err := strconv.Atoi(c)

	return err == nil && n >= 0 && n <= 255
}
