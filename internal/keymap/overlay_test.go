package keymap_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/keymap"
)

func TestTemplateIsAllDefaults(t *testing.T) {
	t.Parallel()

	km, err := keymap.Overlay(keymap.Template())
	require.NoError(t, err)

	for _, info := range keymap.All() {
		require.Equal(t, keymap.Default().Keys(info.Action), km.Keys(info.Action), info.Action)
	}
}

func TestOverlayReplacesOnlyListedActions(t *testing.T) {
	t.Parallel()

	km, err := keymap.Overlay(`copy = ["ctrl+w"]` + "\n[groups]\n\"space n\" = \"+create\"\n")
	require.NoError(t, err)

	a, _ := km.Lookup([]string{"ctrl+w"})
	require.Equal(t, keymap.Copy, a, "user binding wins")

	require.Equal(t, []string{"space g d"}, km.Keys(keymap.Disconnect), "default loses the claimed key only")
	require.Empty(t, km.Keys(keymap.Copy)[1:], "copy keeps only the user's keys")

	a, _ = km.Lookup([]string{"f6"})
	require.Equal(t, keymap.Move, a, "untouched actions keep their defaults")

	require.Equal(t, "+create", km.GroupLabel([]string{"space", "n"}))
	require.Equal(t, "+view", km.GroupLabel([]string{"space", "v"}))
}

func TestOverlayPrefixClashDropsDefault(t *testing.T) {
	t.Parallel()

	// binding "g" alone takes the "g" prefix away from "g g" (top)
	km, err := keymap.Overlay(`reload = ["g"]`)
	require.NoError(t, err)

	a, prefix := km.Lookup([]string{"g"})
	require.Equal(t, keymap.Reload, a)
	require.False(t, prefix)
	require.Equal(t, []string{"home"}, km.Keys(keymap.Top))
}

func TestOverlayErrors(t *testing.T) {
	t.Parallel()

	_, err := keymap.Overlay(`nope = ["x"]`)
	require.ErrorContains(t, err, "unknown action")

	_, err = keymap.Overlay("copy = [\"x\"]\nmove = [\"x\"]\n")
	require.ErrorContains(t, err, "bound to both")
}
