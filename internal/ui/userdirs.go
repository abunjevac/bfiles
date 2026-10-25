package ui

import (
	"os"
	"path/filepath"
	"strings"
)

// Go-to shortcut keys and the user directories they open.
const (
	keyGotoHome      = "alt+h"
	keyGotoDesktop   = "alt+d"
	keyGotoDownloads = "alt+w"
)

// userDir resolves a go-to shortcut key to a local directory; ok is false for other keys.
func userDir(key string) (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}

	switch key {
	case keyGotoHome:
		return home, true
	case keyGotoDesktop:
		return xdgDir("XDG_DESKTOP_DIR", home, "Desktop"), true
	case keyGotoDownloads:
		return xdgDir("XDG_DOWNLOAD_DIR", home, "Downloads"), true
	}

	return "", false
}

// xdgDir reads name from the user-dirs.dirs file, which holds localized or relocated
// folders, and falls back to home/fallback when it is not set.
func xdgDir(name, home, fallback string) string {
	conf := os.Getenv("XDG_CONFIG_HOME")
	if conf == "" {
		conf = filepath.Join(home, ".config")
	}

	data, err := os.ReadFile(filepath.Join(conf, "user-dirs.dirs")) //nolint:gosec // the user's own XDG config
	if err != nil {
		return filepath.Join(home, fallback)
	}

	for line := range strings.Lines(string(data)) {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found || key != name {
			continue
		}

		value = strings.ReplaceAll(strings.Trim(value, `"`), "$HOME", home)
		if filepath.IsAbs(value) {
			return value
		}
	}

	return filepath.Join(home, fallback)
}

// tildePath abbreviates a path under the home directory with ~, which the go-to
// dialog always reads as a local path, even when the panel shows an SSH remote.
func tildePath(dir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return dir
	}

	if dir == home {
		return "~"
	}

	if rest, ok := strings.CutPrefix(dir, home+"/"); ok {
		return "~/" + rest
	}

	return dir
}

func isGotoShortcut(key string) bool {
	return key == keyGotoHome || key == keyGotoDesktop || key == keyGotoDownloads
}
