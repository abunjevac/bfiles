package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestSelectedInputReplacedByTyping(t *testing.T) {
	t.Parallel()

	in := newSelectedInput("/home/x")
	in.update(keyMsg("a"))
	require.Equal(t, "a", in.String())

	in = newSelectedInput("/home/x")
	in.update(keyMsg("backspace"))
	require.Empty(t, in.String())

	in = newSelectedInput("/home/x")
	in.update(keyMsg("end"))
	in.update(keyMsg("/"))
	require.Equal(t, "/home/x/", in.String(), "moving the cursor keeps the text")

	in = newSelectedInput("/home/x")
	in.update(keyMsg("home"))
	in.update(keyMsg("~"))
	require.Equal(t, "~/home/x", in.String())

	in = newSelectedInput("old")
	in.update(tea.PasteMsg{Content: "new"})
	require.Equal(t, "new", in.String())
}
