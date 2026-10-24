package location

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/abunjevac/bfiles/internal/icons"
)

// Kind is the kind of location a Spec describes.
type Kind int

// Location kinds.
const (
	Local Kind = iota
	Archive
	SSH
)

// Spec is a parsed location, not yet opened.
type Spec struct {
	Kind Kind
	// Path is the local directory, the local archive file, or the remote path
	// ("" means the remote home directory).
	Path string
	// Select names an entry to place the cursor on once the directory is listed.
	Select string
	// SSH target parts; Host may be an ssh_config alias.
	User string
	Host string
	Port int
}

// Target renders the SSH part as "[user@]host[:port]".
func (s Spec) Target() string {
	var sb strings.Builder

	if s.User != "" {
		sb.WriteString(s.User)
		sb.WriteByte('@')
	}

	sb.WriteString(s.Host)

	if s.Port != 0 && s.Port != 22 {
		sb.WriteByte(':')
		sb.WriteString(strconv.Itoa(s.Port))
	}

	return sb.String()
}

// String renders the spec in a form ParseArg accepts.
func (s Spec) String() string {
	if s.Kind != SSH {
		return s.Path
	}

	return "ssh://" + s.Target() + s.Path
}

// ParseArg parses a command-line location. Relative local paths resolve against cwd.
//
//	/dir, dir, ./x.zip                   local directory or archive file
//	ssh://[user@]host[:port][/path]      SSH
//	[user@]host:path                     scp-style SSH, when no such local path exists
//	user@host                            SSH home directory
func ParseArg(arg, cwd string) (Spec, error) {
	if arg == "" {
		return Spec{Kind: Local, Path: cwd}, nil
	}

	if strings.HasPrefix(arg, "ssh://") || strings.HasPrefix(arg, "sftp://") {
		return parseURL(arg)
	}

	local := expandHome(arg)

	if !filepath.IsAbs(local) {
		local = filepath.Join(cwd, local)
	}

	if fi, err := os.Stat(local); err == nil {
		return localSpec(filepath.Clean(local), fi.IsDir()), nil
	}

	if s, ok := parseSCP(arg); ok {
		return s, nil
	}

	return Spec{}, fmt.Errorf("location %q: no such file or directory", arg)
}

func localSpec(p string, isDir bool) Spec {
	switch {
	case isDir:
		return Spec{Kind: Local, Path: p}
	case icons.IsArchive(p):
		return Spec{Kind: Archive, Path: p}
	default:
		return Spec{Kind: Local, Path: filepath.Dir(p), Select: filepath.Base(p)}
	}
}

func parseURL(arg string) (Spec, error) {
	u, err := url.Parse(arg)
	if err != nil {
		return Spec{}, fmt.Errorf("location %q: %w", arg, err)
	}

	if u.Hostname() == "" {
		return Spec{}, fmt.Errorf("location %q: missing host", arg)
	}

	s := Spec{Kind: SSH, Host: u.Hostname(), User: u.User.Username(), Path: u.Path}

	if p := u.Port(); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil || port <= 0 || port > 65535 {
			return Spec{}, fmt.Errorf("location %q: invalid port", arg)
		}

		s.Port = port
	}

	return s, nil
}

// parseSCP accepts "[user@]host:path" and "user@host".
func parseSCP(arg string) (Spec, bool) {
	hostPart, p, hasColon := strings.Cut(arg, ":")

	if !hasColon && !strings.Contains(arg, "@") {
		return Spec{}, false
	}

	if hostPart == "" || strings.ContainsAny(hostPart, "/ ") {
		return Spec{}, false
	}

	s := Spec{Kind: SSH, Path: p}

	if user, host, ok := strings.Cut(hostPart, "@"); ok {
		s.User = user
		s.Host = host
	} else {
		s.Host = hostPart
	}

	if s.Host == "" {
		return Spec{}, false
	}

	return s, true
}

func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}

	return filepath.Join(home, p[1:])
}

// AbbrevHome replaces the home directory prefix of a local path with "~".
func AbbrevHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}

	if p == home {
		return "~"
	}

	if strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}

	return p
}
