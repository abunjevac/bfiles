package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/abunjevac/bfiles/internal/keymap"
	"github.com/abunjevac/bfiles/internal/theme"
)

// Bundle is the loaded configuration plus the directory it came from.
type Bundle struct {
	Config *Config
	Dir    string
}

// ConfigPath returns the path of config.toml.
func (b *Bundle) ConfigPath() string {
	return filepath.Join(b.Dir, "config.toml")
}

// KeymapPath returns the path of keymap.toml.
func (b *Bundle) KeymapPath() string {
	return filepath.Join(b.Dir, "keymap.toml")
}

// LoadKeymap applies the overrides in keymap.toml to the default keymap. A missing
// file yields the defaults; a broken one yields the defaults plus the parse error, so
// the app always stays usable.
func (b *Bundle) LoadKeymap() (*keymap.Keymap, error) {
	data, err := os.ReadFile(b.KeymapPath())
	if os.IsNotExist(err) {
		return keymap.Default(), nil
	}

	if err != nil {
		return keymap.Default(), fmt.Errorf("read-keymap: %w", err)
	}

	km, err := keymap.Overlay(string(data))
	if err != nil {
		return keymap.Default(), err
	}

	return km, nil
}

// LoadTheme loads the named theme from the config dir or the builtins.
func (b *Bundle) LoadTheme(name string) (*theme.Theme, error) {
	return theme.Load(b.Dir, name)
}
