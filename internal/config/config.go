package config

import (
	"bytes"
	"cmp"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// configMode is the mode of new config files, which are user-readable by design.
const configMode = 0o644

// Icon styles.
const (
	IconsNerd    = "nerd"
	IconsUnicode = "unicode"
	IconsNone    = "none"
)

// Directory size modes.
const (
	DirSizeApparent = "apparent"
	DirSizeDisk     = "disk"
)

// fileVar is the command placeholder replaced by the selected file.
const fileVar = "{file}"

// Sort keys.
const (
	SortName  = "name"
	SortSize  = "size"
	SortMtime = "mtime"
	SortExt   = "ext"
)

// DefaultTOML is the commented config.toml written on first run. Parsing it must
// yield the same values as parsing an empty file.
//
//go:embed default_config.toml
var DefaultTOML string

// Config holds top-level bfiles settings (config.toml).
type Config struct {
	Theme         string            `toml:"theme"`
	Icons         string            `toml:"icons"`
	Editor        []string          `toml:"editor"`
	Opener        []string          `toml:"opener"`
	Terminal      []string          `toml:"terminal"`
	TerminalExec  []string          `toml:"terminal_exec"`
	Explorer      []string          `toml:"explorer"`
	ConfirmDelete bool              `toml:"confirm_delete"`
	UseTrash      bool              `toml:"use_trash"`
	ParallelJobs  int               `toml:"parallel_jobs"`
	WhichKeyDelay int               `toml:"which_key_delay_ms"`
	DirSize       string            `toml:"dir_size"`
	Panels        Panels            `toml:"panels"`
	Bookmarks     map[string]string `toml:"bookmarks"`
	Preview       Preview           `toml:"preview"`
	SSH           SSH               `toml:"ssh"`
}

// Panels holds the initial per-panel view settings.
type Panels struct {
	ShowHidden  bool     `toml:"show_hidden"`
	ShowSpecial bool     `toml:"show_special"`
	ShowMtime   bool     `toml:"show_mtime"`
	Sort        string   `toml:"sort"`
	SpecialDirs []string `toml:"special_dirs"`
}

// Preview configures the preview pipeline.
type Preview struct {
	MaxBytes ByteSize      `toml:"max_bytes"`
	Rules    []PreviewRule `toml:"rule"`
}

// PreviewRule maps file globs to an external previewer command.
type PreviewRule struct {
	Match   []string `toml:"match"`
	Command []string `toml:"command"`
}

// SSH configures remote connections.
type SSH struct {
	HistoryLimit int `toml:"history_limit"`
	KeepaliveSec int `toml:"keepalive_sec"`
}

// Parse decodes config.toml content, rejecting unknown keys, then applies defaults.
func Parse(data string) (*Config, error) {
	var cfg Config

	meta, err := toml.Decode(data, &cfg)
	if err != nil {
		return nil, fmt.Errorf("decode-config: %w", err)
	}

	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))

		for _, k := range undecoded {
			keys = append(keys, k.String())
		}

		return nil, fmt.Errorf("unknown config keys: %s", strings.Join(keys, ", "))
	}

	applyDefaults(&cfg, meta)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func applyDefaults(cfg *Config, meta toml.MetaData) {
	cfg.Theme = cmp.Or(cfg.Theme, "tokyonight")
	cfg.Icons = cmp.Or(cfg.Icons, IconsNerd)
	cfg.ParallelJobs = cmp.Or(cfg.ParallelJobs, 1)
	cfg.WhichKeyDelay = cmp.Or(cfg.WhichKeyDelay, 250)
	cfg.DirSize = cmp.Or(cfg.DirSize, DirSizeApparent)
	cfg.Panels.Sort = cmp.Or(cfg.Panels.Sort, SortName)
	cfg.Preview.MaxBytes = cmp.Or(cfg.Preview.MaxBytes, 1<<20)
	cfg.SSH.HistoryLimit = cmp.Or(cfg.SSH.HistoryLimit, 50)

	// list and bool settings distinguish "unset" from an explicit empty/false value
	defaultList(meta, &cfg.Editor, []string{}, "editor")
	defaultList(meta, &cfg.Opener, []string{"xdg-open", fileVar}, "opener")
	defaultList(meta, &cfg.Terminal, []string{"bterm", "-w", "{cwd}"}, "terminal")
	defaultList(meta, &cfg.TerminalExec, []string{"bterm", "-w", "{cwd}", "-e", "{cmd}"}, "terminal_exec")
	defaultList(meta, &cfg.Explorer, []string{"dolphin", "{url}"}, "explorer")
	defaultList(meta, &cfg.Panels.SpecialDirs, defaultSpecialDirs(), "panels", "special_dirs")

	if !meta.IsDefined("confirm_delete") {
		cfg.ConfirmDelete = true
	}

	if !meta.IsDefined("panels", "show_mtime") {
		cfg.Panels.ShowMtime = true
	}

	if !meta.IsDefined("ssh", "keepalive_sec") {
		cfg.SSH.KeepaliveSec = 30
	}

	if !meta.IsDefined("preview", "rule") {
		cfg.Preview.Rules = defaultPreviewRules()
	}

	if cfg.Bookmarks == nil {
		cfg.Bookmarks = map[string]string{}
	}
}

func defaultList(meta toml.MetaData, dst *[]string, def []string, key ...string) {
	if !meta.IsDefined(key...) {
		*dst = def
	}
}

func defaultSpecialDirs() []string {
	return []string{
		".git", "node_modules", ".idea", ".vscode", "__pycache__", ".venv",
		".cache", ".task", ".superpowers", "target", "dist", "vendor",
	}
}

func defaultPreviewRules() []PreviewRule {
	return []PreviewRule{
		{Match: []string{"*.md", "*.markdown"}, Command: []string{"glow", "-s", "dark", "-w", "{width}", fileVar}},
		{Match: []string{"*.pdf"}, Command: []string{"pdftotext", "-layout", "-l", "5", fileVar, "-"}},
	}
}

// Validate checks enumerated and ranged settings.
func (c *Config) Validate() error {
	if !slices.Contains([]string{IconsNerd, IconsUnicode, IconsNone}, c.Icons) {
		return fmt.Errorf("invalid icons %q (expected nerd, unicode or none)", c.Icons)
	}

	if !slices.Contains([]string{DirSizeApparent, DirSizeDisk}, c.DirSize) {
		return fmt.Errorf("invalid dir_size %q (expected apparent or disk)", c.DirSize)
	}

	if !slices.Contains([]string{SortName, SortSize, SortMtime, SortExt}, c.Panels.Sort) {
		return fmt.Errorf("invalid panels.sort %q (expected name, size, mtime or ext)", c.Panels.Sort)
	}

	if c.ParallelJobs < 1 || c.ParallelJobs > 16 {
		return fmt.Errorf("invalid parallel_jobs %d (expected 1..16)", c.ParallelJobs)
	}

	if c.WhichKeyDelay < 0 {
		return fmt.Errorf("invalid which_key_delay_ms %d", c.WhichKeyDelay)
	}

	for i, r := range c.Preview.Rules {
		if len(r.Match) == 0 || len(r.Command) == 0 {
			return fmt.Errorf("preview.rule[%d] needs both match and command", i)
		}
	}

	return nil
}

// Clone returns a deep copy, so editors can work on a draft without touching the live config.
func (c *Config) Clone() *Config {
	out := *c

	out.Editor = slices.Clone(c.Editor)
	out.Opener = slices.Clone(c.Opener)
	out.Terminal = slices.Clone(c.Terminal)
	out.TerminalExec = slices.Clone(c.TerminalExec)
	out.Explorer = slices.Clone(c.Explorer)
	out.Panels.SpecialDirs = slices.Clone(c.Panels.SpecialDirs)
	out.Bookmarks = make(map[string]string, len(c.Bookmarks))

	maps.Copy(out.Bookmarks, c.Bookmarks)

	out.Preview.Rules = make([]PreviewRule, len(c.Preview.Rules))

	for i, r := range c.Preview.Rules {
		out.Preview.Rules[i] = PreviewRule{Match: slices.Clone(r.Match), Command: slices.Clone(r.Command)}
	}

	return &out
}

// LoadFile reads and parses a config.toml at path.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read-config: %w", err)
	}

	return Parse(string(data))
}

// Save writes cfg to path as TOML via a temp file, so a crash never truncates the config.
// Comments in an existing file are not preserved.
func Save(path string, cfg *Config) error {
	var buf bytes.Buffer

	buf.WriteString("# bfiles configuration. Changes are picked up live while bfiles is running.\n\n")

	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return fmt.Errorf("encode-config: %w", err)
	}

	return writeFileAtomic(path, buf.Bytes(), configMode)
}

// ResolveDir returns the config dir, honoring an explicit override or ~/.config/bfiles.
func ResolveDir(override string) (string, error) {
	if override != "" {
		return filepath.Abs(override)
	}

	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user-config-dir: %w", err)
	}

	return filepath.Join(base, "bfiles"), nil
}

// Load ensures the config dir is scaffolded, then loads config.toml into a Bundle.
func Load(dir string) (*Bundle, error) {
	if err := Scaffold(dir); err != nil {
		return nil, err
	}

	cfg, err := LoadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		return nil, err
	}

	return &Bundle{Config: cfg, Dir: dir}, nil
}

// writeFileAtomic replaces path through a temp file in the same directory, so a crash
// never truncates it. A symlinked path is resolved so the link itself survives (e.g.
// dotfile setups), and an existing file keeps its mode; mode applies to new files.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	target, err := filepath.EvalSymlinks(path)

	switch {
	case err == nil:
		path = target
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("resolve %s: %w", filepath.Base(path), err)
	}

	if fi, statErr := os.Stat(path); statErr == nil {
		mode = fi.Mode().Perm()
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}

	if err := writeTemp(tmp, data, mode); err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}

	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}

	return nil
}

// writeTemp fills and closes a temp file created by os.CreateTemp (always 0600).
func writeTemp(f *os.File, data []byte, mode os.FileMode) error {
	_, err := f.Write(data)
	if err == nil {
		err = f.Chmod(mode)
	}

	if cerr := f.Close(); err == nil {
		err = cerr
	}

	return err
}
