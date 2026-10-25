package location_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/location"
)

const (
	srvPath  = "/srv"
	logsPath = "logs"
)

func TestSSHStringRoundTrips(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()

	for _, user := range []string{"", "pi", "first.last"} {
		for _, h := range []string{host, "nas.example.com", "10.0.0.1", "::1"} {
			for _, port := range []int{0, 22, 2222} {
				for _, p := range []string{"", "/", srvPath, "/srv/a b/#x?y%z", logsPath, "a/b c", "~x", "/~x"} {
					spec := location.Spec{Kind: location.SSH, User: user, Host: h, Port: port, Path: p}

					got, err := location.ParseArg(spec.String(), cwd)
					require.NoError(t, err, spec.String())

					require.Equal(t, spec, got, spec.String())
				}
			}
		}
	}
}

func TestSSHStringForm(t *testing.T) {
	t.Parallel()

	tests := map[string]location.Spec{
		"ssh://pi@nas:2222/srv": {Kind: location.SSH, User: "pi", Host: host, Port: 2222, Path: srvPath},
		"ssh://nas/~/logs":      {Kind: location.SSH, Host: host, Path: logsPath},
		"ssh://pi@nas/~":        {Kind: location.SSH, User: "pi", Host: host},
	}

	for want, spec := range tests {
		require.Equal(t, want, spec.String())
	}
}
