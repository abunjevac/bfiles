package launch_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/launch"
)

const (
	host    = "nas"
	fileVar = "{file}"
)

func TestExpand(t *testing.T) {
	t.Parallel()

	got := launch.Expand(
		[]string{"konsole", "--workdir", "{cwd}", "-e", "{cmd}", "--title={name}"},
		launch.Vars{
			Values: map[string]string{"cwd": "/tmp/x y", "name": host},
			Lists:  map[string][]string{"cmd": {"ssh", "-t", host}},
		},
	)

	require.Equal(t, []string{"konsole", "--workdir", "/tmp/x y", "-e", "ssh", "-t", host, "--title=nas"}, got)
}

func TestEditorFallbacks(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "nvim -p")

	require.Equal(t, []string{"nvim", "-p", fileVar}, launch.Editor(nil))
	require.Equal(t, []string{"code", fileVar}, launch.Editor([]string{"code", fileVar}))

	t.Setenv("EDITOR", "")

	require.Equal(t, []string{"vi", fileVar}, launch.Editor(nil))
}

func TestDetachedErrors(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, launch.Detached(nil, "/"), launch.ErrEmptyCommand)
	require.Error(t, launch.Detached([]string{"definitely-not-a-real-binary-xyz"}, "/"))
}
