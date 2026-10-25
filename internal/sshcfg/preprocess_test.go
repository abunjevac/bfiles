package sshcfg_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abunjevac/bfiles/internal/sshcfg"
)

func loadText(t *testing.T, dir, text string) *sshcfg.Config {
	t.Helper()

	path := filepath.Join(dir, "config")

	require.NoError(t, os.WriteFile(path, []byte(text), 0o600))

	cfg, err := sshcfg.Load(path)
	require.NoError(t, err)

	return cfg
}

func TestUnsupportedMatchIgnored(t *testing.T) {
	t.Parallel()

	cfg := loadText(t, t.TempDir(), "Host nas\n  User pi\n\n"+
		"Match host * exec \"true\"\n  User evil\n  Port 1\n\n"+
		"Match exec \"true\"\n  User evil\n\n"+
		"Match user bob host x\n  User evil\n\n"+
		"Match host web,db # app servers\n  User app\n\n"+
		"Match all\n  Port 2200\n")

	require.Equal(t, []string{nasAlias, "web", "db"}, cfg.Aliases())

	other := cfg.Resolve("", "other", 0)
	require.NotEqual(t, "evil", other.User)
	require.Equal(t, 2200, other.Port)

	require.Equal(t, "pi", cfg.Resolve("", nasAlias, 0).User)
	require.Equal(t, "app", cfg.Resolve("", "db", 0).User)
}

func TestIncludedHosts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	require.NoError(t, os.Mkdir(filepath.Join(dir, "conf.d"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "conf.d", "a.conf"), []byte("Host alpha\n  User a\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "conf.d", "b.conf"), []byte("Host beta\n  Port 2022\n"), 0o600))

	cfg := loadText(t, dir, "Include conf.d/*.conf\nUser global\n\nHost nas\n  Port 2222\n")

	require.Equal(t, []string{"alpha", "beta", nasAlias}, cfg.Aliases())
	require.Equal(t, "a", cfg.Resolve("", "alpha", 0).User)
	require.Equal(t, 2022, cfg.Resolve("", "beta", 0).Port)
	require.Equal(t, "global", cfg.Resolve("", "beta", 0).User, "settings after an Include stay global")
	require.Equal(t, 2222, cfg.Resolve("", nasAlias, 0).Port)
}

func TestIncludeLoopFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config")

	require.NoError(t, os.WriteFile(path, []byte("Include config\n"), 0o600))

	_, err := sshcfg.Load(path)
	require.Error(t, err)
}

func TestCaseInsensitiveHostsAndTokens(t *testing.T) {
	t.Parallel()

	cfg := loadText(t, t.TempDir(), "Host NAS !Skip\n  HostName 10.0.0.5\n  User pi\n  IdentityFile /keys/%h-%r-%p-%n-%%\n")

	require.Equal(t, []string{nasAlias}, cfg.Aliases())

	tg := cfg.Resolve("", "Nas", 0)
	require.Equal(t, "10.0.0.5", tg.Host)
	require.Equal(t, []string{"/keys/10.0.0.5-pi-22-Nas-%"}, tg.IdentityFiles)
}
