package icons

import (
	"path"
	"strings"
)

// Role is the colour category of an entry.
type Role int

// Colour roles.
const (
	RoleFile Role = iota
	RoleDir
	RoleSymlink
	RoleExec
	RoleArchive
	RoleImage
	RoleCode
	RoleDoc
)

// Styles, mirroring config.Icons* (duplicated to keep this package dependency-free).
const (
	StyleNerd    = "nerd"
	StyleUnicode = "unicode"
	StyleNone    = "none"
)

type glyph struct {
	nerd string
	role Role
}

// Nerd Font codepoints are written as escapes: the glyphs live in the private use area
// and are invisible in most editors.
//
//nolint:gochecknoglobals // static lookup tables
var (
	dirGlyph     = ""
	fileGlyph    = ""
	linkGlyph    = ""
	linkDirGlyph = ""
	execGlyph    = ""
	parentGlyph  = ""

	dirNames = map[string]string{
		".git":         "",
		"node_modules": "",
		".config":      "",
		".github":      "",
		"Desktop":      "",
		"Documents":    "",
		"Downloads":    "",
		"Music":        "",
		"Pictures":     "",
		"Videos":       "",
	}

	fileNames = map[string]glyph{
		"makefile":           {"", RoleCode},
		"dockerfile":         {"", RoleCode},
		"license":            {"", RoleDoc},
		"readme":             {"", RoleDoc},
		"readme.md":          {"", RoleDoc},
		".gitignore":         {"", RoleFile},
		".gitmodules":        {"", RoleFile},
		".gitconfig":         {"", RoleFile},
		"go.mod":             {"", RoleCode},
		"go.sum":             {"", RoleCode},
		"taskfile.yml":       {"", RoleCode},
		"cargo.toml":         {"", RoleCode},
		"package.json":       {"", RoleCode},
		".zshrc":             {"", RoleCode},
		".bashrc":            {"", RoleCode},
		".profile":           {"", RoleCode},
		"compose.yml":        {"", RoleCode},
		"compose.yaml":       {"", RoleCode},
		"docker-compose.yml": {"", RoleCode},
	}

	extensions = map[string]glyph{
		// code
		"go": {"", RoleCode}, "rs": {"", RoleCode}, "py": {"", RoleCode},
		"js": {"", RoleCode}, "mjs": {"", RoleCode}, "cjs": {"", RoleCode},
		"ts": {"", RoleCode}, "tsx": {"", RoleCode}, "jsx": {"", RoleCode},
		"c": {"", RoleCode}, "h": {"", RoleCode}, "cpp": {"", RoleCode},
		"hpp": {"", RoleCode}, "cc": {"", RoleCode}, "java": {"", RoleCode},
		"kt": {"", RoleCode}, "rb": {"", RoleCode}, "php": {"", RoleCode},
		"lua": {"", RoleCode}, "vim": {"", RoleCode}, "sh": {"", RoleCode},
		"bash": {"", RoleCode}, "zsh": {"", RoleCode}, "fish": {"", RoleCode},
		"html": {"", RoleCode}, "css": {"", RoleCode}, "scss": {"", RoleCode},
		"sql": {"", RoleCode}, "swift": {"", RoleCode}, "dart": {"", RoleCode},
		"cs": {"", RoleCode}, "zig": {"", RoleCode}, "vue": {"", RoleCode},
		"svelte": {"", RoleCode}, "proto": {"", RoleCode},
		// data and config
		"json": {"", RoleCode}, "toml": {"", RoleCode}, "yaml": {"", RoleCode},
		"yml": {"", RoleCode}, "ini": {"", RoleCode}, "conf": {"", RoleCode},
		"xml": {"", RoleCode}, "csv": {"", RoleDoc}, "env": {"", RoleCode},
		"lock": {"", RoleFile},
		// documents
		"md": {"", RoleDoc}, "markdown": {"", RoleDoc}, "txt": {"", RoleDoc},
		"log": {"", RoleDoc}, "pdf": {"", RoleDoc}, "doc": {"", RoleDoc},
		"docx": {"", RoleDoc}, "odt": {"", RoleDoc}, "xls": {"", RoleDoc},
		"xlsx": {"", RoleDoc}, "ods": {"", RoleDoc}, "ppt": {"", RoleDoc},
		"pptx": {"", RoleDoc}, "epub": {"", RoleDoc},
		// media
		"png": {"", RoleImage}, "jpg": {"", RoleImage}, "jpeg": {"", RoleImage},
		"gif": {"", RoleImage}, "webp": {"", RoleImage}, "bmp": {"", RoleImage},
		"svg": {"", RoleImage}, "ico": {"", RoleImage}, "tiff": {"", RoleImage},
		"mp4": {"", RoleImage}, "mkv": {"", RoleImage}, "webm": {"", RoleImage},
		"avi": {"", RoleImage}, "mov": {"", RoleImage}, "mp3": {"", RoleImage},
		"flac": {"", RoleImage}, "ogg": {"", RoleImage}, "wav": {"", RoleImage},
		"m4a": {"", RoleImage},
		// archives
		"zip": {"", RoleArchive}, "tar": {"", RoleArchive}, "gz": {"", RoleArchive},
		"tgz": {"", RoleArchive}, "bz2": {"", RoleArchive}, "tbz2": {"", RoleArchive},
		"xz": {"", RoleArchive}, "txz": {"", RoleArchive}, "zst": {"", RoleArchive},
		"tzst": {"", RoleArchive}, "7z": {"", RoleArchive}, "rar": {"", RoleArchive},
		"jar": {"", RoleArchive}, "deb": {"", RoleArchive}, "rpm": {"", RoleArchive},
		"iso": {"", RoleArchive},
		// misc
		"pem": {"", RoleFile}, "key": {"", RoleFile}, "crt": {"", RoleFile},
		"pub": {"", RoleFile}, "ttf": {"", RoleFile}, "otf": {"", RoleFile},
		"woff": {"", RoleFile}, "woff2": {"", RoleFile}, "exe": {"", RoleExec},
		"so": {"", RoleFile}, "o": {"", RoleFile}, "bin": {"", RoleFile},
	}

	unicodeByRole = map[Role]string{
		RoleFile:    "·",
		RoleDir:     "▸",
		RoleSymlink: "↪",
		RoleExec:    "▷",
		RoleArchive: "◫",
		RoleImage:   "◩",
		RoleCode:    "λ",
		RoleDoc:     "≡",
	}
)

// For returns the glyph and colour role of an entry. The glyph is empty for StyleNone.
func For(name string, isDir, isLink, isExec bool, style string) (string, Role) {
	g, role := nerdFor(name, isDir, isLink, isExec)

	switch style {
	case StyleNone:
		return "", role
	case StyleUnicode:
		if isLink {
			return unicodeByRole[RoleSymlink], role
		}

		return unicodeByRole[role], role
	default:
		return g, role
	}
}

func nerdFor(name string, isDir, isLink, isExec bool) (string, Role) {
	if name == ".." {
		return parentGlyph, RoleDir
	}

	if isDir {
		if isLink {
			return linkDirGlyph, RoleDir
		}

		if g, ok := dirNames[name]; ok {
			return g, RoleDir
		}

		return dirGlyph, RoleDir
	}

	lower := strings.ToLower(name)

	if g, ok := fileNames[lower]; ok {
		return pick(g, isLink)
	}

	if g, ok := extensions[ext(lower)]; ok {
		return pick(g, isLink)
	}

	if isLink {
		return linkGlyph, RoleSymlink
	}

	if isExec {
		return execGlyph, RoleExec
	}

	return fileGlyph, RoleFile
}

func pick(g glyph, isLink bool) (string, Role) {
	if isLink {
		return g.nerd, RoleSymlink
	}

	return g.nerd, g.role
}

func ext(lowerName string) string {
	e := path.Ext(lowerName)
	if e == "" {
		return ""
	}

	return e[1:]
}

// archiveSuffixes lists names opened as browsable archives. Longer suffixes first.
//
//nolint:gochecknoglobals
var archiveSuffixes = []string{
	".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst", ".tar.lz4", ".tar.br",
	".tgz", ".tbz2", ".tbz", ".txz", ".tzst", ".tar", ".zip", ".jar", ".7z", ".rar",
}

// IsArchive reports whether name looks like an archive bfiles can open as a folder.
func IsArchive(name string) bool {
	lower := strings.ToLower(name)

	for _, s := range archiveSuffixes {
		if strings.HasSuffix(lower, s) {
			return true
		}
	}

	return false
}

// IsImage reports whether name has a common raster image extension.
func IsImage(name string) bool {
	switch ext(strings.ToLower(name)) {
	case "png", "jpg", "jpeg", "gif", "webp", "bmp", "tiff":
		return true
	}

	return false
}

// IsVideo reports whether name has a common video extension.
func IsVideo(name string) bool {
	switch ext(strings.ToLower(name)) {
	case "mp4", "mkv", "webm", "avi", "mov":
		return true
	}

	return false
}

// Backend glyphs for panel headers.
const (
	BackendLocal   = ""
	BackendSSH     = ""
	BackendArchive = ""
	BackendSearch  = ""
)
