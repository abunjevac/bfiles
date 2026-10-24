package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/abunjevac/bfiles/internal/keymap"
	"github.com/abunjevac/bfiles/internal/theme"
)

// Scaffold creates the config dir with a default config.toml, keymap.toml and the
// builtin theme files. Existing files are never overwritten.
func Scaffold(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "themes"), 0o755); err != nil {
		return fmt.Errorf("scaffold-config-dir: %w", err)
	}

	if err := writeIfMissing(filepath.Join(dir, "config.toml"), []byte(DefaultTOML)); err != nil {
		return err
	}

	if err := writeIfMissing(filepath.Join(dir, "keymap.toml"), []byte(keymap.Template())); err != nil {
		return err
	}

	for _, name := range theme.BuiltinNames() {
		src, err := theme.BuiltinSource(name)
		if err != nil {
			return err
		}

		if err := writeIfMissing(filepath.Join(dir, "themes", name+".toml"), src); err != nil {
			return err
		}
	}

	return nil
}

func writeIfMissing(path string, data []byte) error {
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return nil
	}

	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // config files are user-readable by design
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}

	return nil
}
