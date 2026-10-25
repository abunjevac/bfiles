package keymap_test

import (
	_ "embed"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/keymap"
)

const space = "space"

//go:embed testdata/conflict_keymap.toml
var conflictKeymapTOML string

//go:embed testdata/prefix_clash_keymap.toml
var prefixClashKeymapTOML string

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{"Ctrl+Q", "ctrl+q"},
		{"shift+ctrl+x", "ctrl+shift+x"},
		{"shift+g", "G"},
		{"G", "G"},
		{"g", "g"},
		{"Escape", "esc"},
		{"PageDown", "pgdown"},
		{"F5", "f5"},
		{"shift+F6", "shift+f6"},
		{"+", "+"},
		{"ctrl++", "ctrl++"},
		{"leader", space},
		{"alt+F7", "alt+f7"},
		{"alt+G", "alt+shift+g"},
		{"Alt+shift+G", "alt+shift+g"},
		{"meta+alt+X", "alt+shift+meta+x"},
		{"alt+g", "alt+g"},
		{"ctrl+alt+G", "ctrl+alt+g"},
	}

	for _, tc := range tests {
		got, err := keymap.Normalize(tc.in)
		require.NoError(t, err, tc.in)

		require.Equal(t, tc.want, got, tc.in)
	}
}

func TestNormalizeErrors(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"", "ctrl+shift", "ctrl+a+b"} {
		_, err := keymap.Normalize(in)
		require.Error(t, err, in)
	}
}

func TestDefaultLookup(t *testing.T) {
	t.Parallel()

	km := keymap.Default()

	a, prefix := km.Lookup([]string{space})
	require.Empty(t, a)
	require.True(t, prefix)

	a, prefix = km.Lookup([]string{space, "c"})
	require.Equal(t, keymap.Copy, a)
	require.False(t, prefix)

	a, _ = km.Lookup([]string{"f5"})
	require.Equal(t, keymap.Copy, a)

	a, _ = km.Lookup([]string{space, "n", "d"})
	require.Equal(t, keymap.Mkdir, a)

	a, prefix = km.Lookup([]string{"z"})
	require.Empty(t, a)
	require.False(t, prefix)
}

func TestDefaultCoversEveryAction(t *testing.T) {
	t.Parallel()

	km := keymap.Default()

	for _, info := range keymap.All() {
		require.NotEmpty(t, km.Keys(info.Action), info.Action)
	}
}

func TestChildren(t *testing.T) {
	t.Parallel()

	km := keymap.Default()
	hints := km.Children([]string{space})

	require.Contains(t, hints, keymap.Hint{Key: "c", Label: "copy"})
	require.Contains(t, hints, keymap.Hint{Key: "n", Label: "+new", Group: true})

	// groups come last
	require.True(t, hints[len(hints)-1].Group)
	require.Equal(t, "+settings", km.GroupLabel([]string{space, ","}))
}

func TestParseErrors(t *testing.T) {
	t.Parallel()

	_, err := keymap.Parse(conflictKeymapTOML)
	require.ErrorContains(t, err, "bound to both")

	_, err = keymap.Parse(prefixClashKeymapTOML)
	require.ErrorContains(t, err, "both a binding and a prefix")

	_, err = keymap.Parse(`no_such_action = ["x"]`)
	require.ErrorContains(t, err, "unknown action")
}

func TestDisplay(t *testing.T) {
	t.Parallel()

	require.Equal(t, "␣ n d", keymap.DisplaySequence("space n d"))
	require.Equal(t, "C-q", keymap.Display("ctrl+q"))
	require.Equal(t, "F5", keymap.Display("f5"))
	require.Equal(t, "f", keymap.Display("f"))
}
