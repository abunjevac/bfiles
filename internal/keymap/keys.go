package keymap

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

const keySpace = "space"

// modifierOrder matches Bubble Tea's keystroke rendering, so normalized bindings
// compare equal to KeyPressMsg.String().
//
//nolint:gochecknoglobals
var modifierOrder = []string{"ctrl", "alt", "shift", "meta", "hyper", "super"}

//nolint:gochecknoglobals
var keyAliases = map[string]string{
	"escape":   "esc",
	"return":   "enter",
	"del":      "delete",
	"ins":      "insert",
	"pageup":   "pgup",
	"pagedown": "pgdown",
	"pgdn":     "pgdown",
	"bs":       "backspace",
	"leader":   keySpace,
	" ":        keySpace,
}

// displaySymbols are the glyphs Display uses for named keys.
//
//nolint:gochecknoglobals
var displaySymbols = map[string]string{
	keySpace:    "␣",
	"enter":     "⏎",
	"esc":       "Esc",
	"backspace": "⌫",
	"tab":       "⇥",
	"up":        "↑",
	"down":      "↓",
	"left":      "←",
	"right":     "→",
}

// Normalize returns the canonical form of a single key ("ctrl+q", "space", "G", "f5").
// Single printable characters keep their case; "shift+g" becomes "G" because that is
// what terminals report.
func Normalize(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("normalize: key is empty")
	}

	// a lone "+" is a key, not a separator
	if key == "+" {
		return key, nil
	}

	mods, name, err := splitKey(key)
	if err != nil {
		return "", err
	}

	name = canonicalName(name, mods)

	var sb strings.Builder

	for _, m := range modifierOrder {
		if mods[m] {
			sb.WriteString(m)
			sb.WriteByte('+')
		}
	}

	sb.WriteString(name)

	return sb.String(), nil
}

// splitKey separates a key into its modifier set and key component.
func splitKey(key string) (map[string]bool, string, error) {
	parts := strings.Split(key, "+")
	if strings.HasSuffix(key, "++") {
		parts = append(parts[:len(parts)-2], "+")
	}

	mods := map[string]bool{}
	name := ""

	for _, p := range parts {
		lp := strings.ToLower(p)

		if isModifier(lp) {
			mods[lp] = true

			continue
		}

		if name != "" || p == "" {
			return nil, "", fmt.Errorf("normalize: malformed key %q", key)
		}

		name = p
	}

	if name == "" {
		return nil, "", fmt.Errorf("normalize: key %q has no key component", key)
	}

	return mods, name, nil
}

// canonicalName fixes the case of a key component and resolves aliases; it may
// drop "shift" from mods when the shift is folded into an uppercase letter.
func canonicalName(name string, mods map[string]bool) string {
	if utf8.RuneCountInString(name) != 1 {
		name = strings.ToLower(name)

		if alias, ok := keyAliases[name]; ok {
			name = alias
		}

		return name
	}

	// terminals report a shifted letter as its uppercase text, but keep the
	// shift modifier when combined with others ("ctrl+shift+x")
	if len(mods) == 1 && mods["shift"] && strings.ToUpper(name) != strings.ToLower(name) {
		delete(mods, "shift")

		return strings.ToUpper(name)
	}

	// with alt (or meta, ...) terminals report a shifted letter as shift plus the
	// lowercase letter ("alt+shift+g"); with ctrl, legacy terminals cannot report
	// shift at all, so "ctrl+Q" keeps meaning ctrl+q
	if len(mods) > 0 && !mods["ctrl"] && strings.ToLower(name) != name {
		mods["shift"] = true
	}

	if len(mods) > 0 {
		return strings.ToLower(name)
	}

	return name
}

// ParseSequence splits and normalizes a space-separated key sequence ("space n d").
func ParseSequence(seq string) ([]string, error) {
	fields := strings.Fields(seq)

	if len(fields) == 0 {
		return nil, fmt.Errorf("empty key sequence")
	}

	out := make([]string, 0, len(fields))

	for _, f := range fields {
		k, err := Normalize(f)
		if err != nil {
			return nil, err
		}

		out = append(out, k)
	}

	return out, nil
}

// Display renders a key for humans, e.g. "space" → "␣", "ctrl+q" → "C-q".
func Display(key string) string {
	if sym, ok := displaySymbols[key]; ok {
		return sym
	}

	out := key
	out = strings.ReplaceAll(out, "ctrl+", "C-")
	out = strings.ReplaceAll(out, "alt+", "M-")
	out = strings.ReplaceAll(out, "shift+", "S-")

	if strings.HasPrefix(out, "f") && len(out) <= 3 && len(out) > 1 && out[1] >= '0' && out[1] <= '9' {
		return strings.ToUpper(out)
	}

	return out
}

// DisplaySequence renders a whole sequence, e.g. "space n d" → "␣ n d".
func DisplaySequence(seq string) string {
	keys := strings.Fields(seq)

	for i, k := range keys {
		keys[i] = Display(k)
	}

	return strings.Join(keys, " ")
}

func isModifier(s string) bool {
	return slices.Contains(modifierOrder, s)
}
