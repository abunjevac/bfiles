package keymap

import (
	"fmt"
	"maps"
	"strings"
)

// templateHeader introduces the scaffolded keymap.toml, which only holds overrides.
const templateHeader = `# bfiles keymap — overrides on top of the builtin defaults.
#
# Only actions listed here change; every other action keeps its default keys, so new
# bindings in future versions reach you automatically. To change an action, uncomment
# its line and edit the keys. A key you take over is removed from the action that had
# it by default. Sequences are space-separated keys ("space n d"); the first key of a
# multi-key sequence is a prefix that opens the which-key popup.
#
# The defaults are listed below for reference.

`

// Template returns the scaffolded keymap.toml: the defaults, commented out.
func Template() string {
	var sb strings.Builder

	sb.WriteString(templateHeader)

	// the defaults' own header comment would repeat the template header
	_, body, _ := strings.Cut(DefaultTOML, "\n\n")

	for line := range strings.SplitSeq(strings.TrimRight(body, "\n"), "\n") {
		switch {
		case line == "":
			sb.WriteString("\n")
		case strings.HasPrefix(line, "#"):
			sb.WriteString("#" + line + "\n")
		default:
			sb.WriteString("# " + line + "\n")
		}
	}

	return sb.String()
}

// Overlay builds the keymap from the defaults with the actions in data replacing
// theirs. Default keys claimed by an override are dropped from their default action,
// so overriding never conflicts with a binding the user did not write.
func Overlay(data string) (*Keymap, error) {
	user, err := decode(data)
	if err != nil {
		return nil, err
	}

	defaults, err := decode(DefaultTOML)
	if err != nil {
		return nil, err
	}

	claimed, err := claimedSequences(user)
	if err != nil {
		return nil, err
	}

	merged := map[string]any{}

	for name, v := range defaults {
		if _, overridden := user[name]; overridden || name == groupsKey {
			continue
		}

		merged[name] = withoutClaimed(v, claimed)
	}

	for name, v := range user {
		if name != groupsKey {
			merged[name] = v
		}
	}

	merged[groupsKey] = mergeGroups(defaults[groupsKey], user[groupsKey])

	return parseRaw(merged)
}

const groupsKey = "groups"

// claimedSequences collects the normalized sequences bound in the user file.
func claimedSequences(user map[string]any) (map[string]bool, error) {
	claimed := map[string]bool{}

	for name, v := range user {
		if name == groupsKey {
			continue
		}

		if _, ok := infoByAction[Action(name)]; !ok {
			return nil, fmt.Errorf("keymap: unknown action %q", name)
		}

		seqs, err := stringList(v)
		if err != nil {
			return nil, fmt.Errorf("keymap %s: %w", name, err)
		}

		for _, s := range seqs {
			keys, err := ParseSequence(s)
			if err != nil {
				return nil, fmt.Errorf("keymap %s: %w", name, err)
			}

			claimed[strings.Join(keys, " ")] = true
		}
	}

	return claimed, nil
}

// withoutClaimed removes default sequences that equal a claimed one or would clash
// with it as a prefix.
func withoutClaimed(v any, claimed map[string]bool) []any {
	seqs, _ := stringList(v)
	out := make([]any, 0, len(seqs))

	for _, s := range seqs {
		keys, err := ParseSequence(s)
		if err != nil {
			continue
		}

		if !clashes(strings.Join(keys, " "), claimed) {
			out = append(out, s)
		}
	}

	return out
}

func clashes(seq string, claimed map[string]bool) bool {
	for c := range claimed {
		if seq == c || strings.HasPrefix(c, seq+" ") || strings.HasPrefix(seq, c+" ") {
			return true
		}
	}

	return false
}

func mergeGroups(def, user any) map[string]any {
	out := map[string]any{}

	if d, ok := def.(map[string]any); ok {
		maps.Copy(out, d)
	}

	if u, ok := user.(map[string]any); ok {
		maps.Copy(out, u)
	}

	return out
}
