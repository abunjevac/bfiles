package sshcfg

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// maxIncludeDepth bounds nested Include directives, like OpenSSH.
const maxIncludeDepth = 16

// neverHeader starts a block that matches no host; it replaces Match blocks whose
// criteria cannot be evaluated, so their settings never leak onto every host.
const neverHeader = "Host !*"

// errIncludeDepth reports an Include loop or too deep nesting.
var errIncludeDepth = errors.New("ssh config: Include nested too deeply")

// preprocessor rewrites an ssh_config into the subset the parser library handles
// correctly: Include directives are inlined, Match blocks become Host blocks (or
// never-matching blocks when their criteria are not plain host/all), and host
// patterns are lower-cased so matching is case-insensitive like OpenSSH.
type preprocessor struct {
	out     bytes.Buffer
	aliases []string
}

// file rewrites one config file; path locates relative Include directives.
func (p *preprocessor) file(path string, data []byte, depth int) error {
	// the block in effect, re-opened after inlined files that may start their own
	current := "Host *"

	for line := range strings.Lines(string(data)) {
		kw, rest := directive(line)

		switch strings.ToLower(kw) {
		case "host":
			current = "Host " + strings.ToLower(rest)
			p.addAliases(strings.Fields(strings.ToLower(rest)))
			p.out.WriteString(current + "\n")
		case "match":
			current = p.match(rest)
			p.out.WriteString(current + "\n")
		case "include":
			if err := p.include(path, rest, depth); err != nil {
				return err
			}

			p.out.WriteString(current + "\n")
		default:
			p.out.WriteString(strings.TrimRight(line, "\r\n") + "\n")
		}
	}

	return nil
}

// match converts a Match line into an equivalent Host header.
func (p *preprocessor) match(rest string) string {
	fields := strings.Fields(rest)

	switch {
	case len(fields) == 1 && strings.EqualFold(fields[0], "all"):
		return "Host *"
	case len(fields) == 2 && strings.EqualFold(fields[0], "host"):
		patterns := strings.Fields(strings.ReplaceAll(strings.ToLower(fields[1]), ",", " "))

		p.addAliases(patterns)

		return "Host " + strings.Join(patterns, " ")
	default:
		return neverHeader
	}
}

// include inlines the files an Include directive names. Relative paths resolve
// against the directory of the including file (~/.ssh for the user config).
func (p *preprocessor) include(path, rest string, depth int) error {
	if depth >= maxIncludeDepth {
		return errIncludeDepth
	}

	for pattern := range strings.FieldsSeq(rest) {
		pattern = expandTilde(pattern)

		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(filepath.Dir(path), pattern)
		}

		matches, err := filepath.Glob(pattern)
		if err != nil {
			return fmt.Errorf("include %s: %w", pattern, err)
		}

		for _, m := range matches {
			data, err := os.ReadFile(m)
			if err != nil {
				return fmt.Errorf("include %s: %w", m, err)
			}

			if err := p.file(m, data, depth+1); err != nil {
				return err
			}
		}
	}

	return nil
}

// addAliases records concrete host patterns, skipping wildcards and negations.
func (p *preprocessor) addAliases(patterns []string) {
	for _, s := range patterns {
		if strings.ContainsAny(s, "*?!") || slices.Contains(p.aliases, s) {
			continue
		}

		p.aliases = append(p.aliases, s)
	}
}

// directive splits a config line into its keyword and arguments; keyword and
// arguments may be separated by whitespace or '='.
func directive(line string) (string, string) {
	t := strings.TrimSpace(line)

	if t == "" || t[0] == '#' {
		return "", ""
	}

	end := strings.IndexAny(t, " \t=")
	if end < 0 {
		return t, ""
	}

	rest := strings.TrimSpace(t[end:])
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "="))

	kw := t[:end]

	// host patterns never contain '#', so it can only start a trailing comment
	if strings.EqualFold(kw, "host") || strings.EqualFold(kw, "match") {
		rest, _, _ = strings.Cut(rest, "#")
		rest = strings.TrimSpace(rest)
	}

	return kw, rest
}
