package location_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/location"
)

const host = "nas"

func TestParseArg(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()

	require.NoError(t, os.Mkdir(filepath.Join(cwd, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "a.zip"), nil, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(cwd, "notes.txt"), nil, 0o600))

	tests := []struct {
		arg  string
		want location.Spec
	}{
		{"", location.Spec{Kind: location.Local, Path: cwd}},
		{"sub", location.Spec{Kind: location.Local, Path: filepath.Join(cwd, "sub")}},
		{"./sub/", location.Spec{Kind: location.Local, Path: filepath.Join(cwd, "sub")}},
		{cwd, location.Spec{Kind: location.Local, Path: cwd}},
		{"a.zip", location.Spec{Kind: location.Archive, Path: filepath.Join(cwd, "a.zip")}},
		{"notes.txt", location.Spec{Kind: location.Local, Path: cwd, Select: "notes.txt"}},
		{"ssh://pi@nas:2222/srv", location.Spec{Kind: location.SSH, User: "pi", Host: host, Port: 2222, Path: "/srv"}},
		{"sftp://nas", location.Spec{Kind: location.SSH, Host: host}},
		{"pi@nas:/srv/media", location.Spec{Kind: location.SSH, User: "pi", Host: host, Path: "/srv/media"}},
		{"nas:logs", location.Spec{Kind: location.SSH, Host: host, Path: "logs"}},
		{"pi@nas", location.Spec{Kind: location.SSH, User: "pi", Host: host}},
	}

	for _, tc := range tests {
		got, err := location.ParseArg(tc.arg, cwd)
		require.NoError(t, err, tc.arg)

		require.Equal(t, tc.want, got, tc.arg)
	}
}

func TestParseArgErrors(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()

	for _, arg := range []string{"missing-dir", "ssh://", "ssh://h:99999/x", "./x:y"} {
		_, err := location.ParseArg(arg, cwd)
		require.Error(t, err, arg)
	}
}

func TestTargetString(t *testing.T) {
	t.Parallel()

	s := location.Spec{Kind: location.SSH, User: "pi", Host: host, Port: 2222, Path: "/srv"}

	require.Equal(t, "pi@nas:2222", s.Target())
	require.Equal(t, "ssh://pi@nas:2222/srv", s.String())

	s.Port = 22

	require.Equal(t, "pi@nas", s.Target())
}
