package theme

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Fallback is the theme used when the configured one cannot be loaded.
const Fallback = "tokyonight"

//go:embed themes/*.toml
var builtinFS embed.FS

// BuiltinNames returns the names of all embedded themes, sorted.
func BuiltinNames() []string {
	entries, err := builtinFS.ReadDir("themes")
	if err != nil {
		panic(err) // embedded FS is always readable
	}

	names := make([]string, 0, len(entries))

	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".toml"))
	}

	slices.Sort(names)

	return names
}

// BuiltinSource returns the raw TOML of an embedded theme.
func BuiltinSource(name string) ([]byte, error) {
	data, err := builtinFS.ReadFile("themes/" + name + ".toml")
	if err != nil {
		return nil, fmt.Errorf("unknown builtin theme %q", name)
	}

	return data, nil
}

// Builtin parses an embedded theme by name.
func Builtin(name string) (*Theme, error) {
	data, err := BuiltinSource(name)
	if err != nil {
		return nil, err
	}

	return Parse(name, string(data))
}

// Load returns a theme by name, preferring <dir>/themes/<name>.toml over the builtin.
// The error describes why the requested theme was not used; the returned theme is
// always usable (falls back to the builtin default).
func Load(dir, name string) (*Theme, error) {
	t, err := load(dir, name)
	if err == nil {
		return t, nil
	}

	fallback, ferr := Builtin(Fallback)
	if ferr != nil {
		panic(ferr) // the fallback theme is embedded and tested
	}

	return fallback, err
}

func load(dir, name string) (*Theme, error) {
	if dir != "" {
		data, err := os.ReadFile(filepath.Join(dir, "themes", name+".toml"))
		if err == nil {
			return Parse(name, string(data))
		}
	}

	return Builtin(name)
}

// List returns builtin and user theme names, merged and sorted.
func List(dir string) []string {
	names := BuiltinNames()

	if dir != "" {
		matches, _ := filepath.Glob(filepath.Join(dir, "themes", "*.toml"))

		for _, m := range matches {
			name := strings.TrimSuffix(filepath.Base(m), ".toml")

			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}

	slices.Sort(names)

	return names
}
