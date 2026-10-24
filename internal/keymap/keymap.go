package keymap

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// DefaultTOML is the default keymap.toml content.
//
//go:embed default_keymap.toml
var DefaultTOML string

// Keymap resolves key sequences to actions.
type Keymap struct {
	bindings map[string]Action   // "space n d" → mkdir
	prefixes map[string]bool     // "space", "space n", "g"
	groups   map[string]string   // prefix → which-key label
	byAction map[Action][]string // action → sequences, in file order
}

// Hint is one row of the which-key popup.
type Hint struct {
	Key   string
	Label string
	Group bool
}

// Binding is one sequence → action pair, for the help overlay.
type Binding struct {
	Sequence string
	Action   Action
}

// Parse decodes a keymap.toml. Unknown actions, malformed keys, a sequence bound to
// two actions, or a sequence that is both a binding and a prefix are errors.
func Parse(data string) (*Keymap, error) {
	raw, err := decode(data)
	if err != nil {
		return nil, err
	}

	return parseRaw(raw)
}

func decode(data string) (map[string]any, error) {
	var raw map[string]any

	if _, err := toml.Decode(data, &raw); err != nil {
		return nil, fmt.Errorf("decode-keymap: %w", err)
	}

	return raw, nil
}

func parseRaw(raw map[string]any) (*Keymap, error) {
	km := &Keymap{
		bindings: map[string]Action{},
		prefixes: map[string]bool{},
		groups:   map[string]string{},
		byAction: map[Action][]string{},
	}

	if err := km.bindActions(raw); err != nil {
		return nil, err
	}

	if err := km.checkNames(raw); err != nil {
		return nil, err
	}

	for seq := range km.bindings {
		if km.prefixes[seq] {
			return nil, fmt.Errorf("keymap: %q is both a binding and a prefix", seq)
		}
	}

	return km, nil
}

// Default returns the builtin keymap.
func Default() *Keymap {
	km, err := Parse(DefaultTOML)
	if err != nil {
		panic(err) // embedded and covered by tests
	}

	return km
}

func stringList(v any) ([]string, error) {
	switch t := v.(type) {
	case string:
		return []string{t}, nil
	case []any:
		out := make([]string, 0, len(t))

		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("expected strings, got %T", item)
			}

			out = append(out, s)
		}

		return out, nil
	default:
		return nil, fmt.Errorf("expected a string or list of strings, got %T", v)
	}
}

// Lookup resolves a pressed sequence; the bool reports whether more keys may follow.
func (km *Keymap) Lookup(seq []string) (Action, bool) {
	key := strings.Join(seq, " ")

	if a, ok := km.bindings[key]; ok {
		return a, false
	}

	return "", km.prefixes[key]
}

// GroupLabel returns the which-key label of a prefix.
func (km *Keymap) GroupLabel(prefix []string) string {
	return km.groups[strings.Join(prefix, " ")]
}

// Children lists the keys that may follow prefix, sorted with groups last.
func (km *Keymap) Children(prefix []string) []Hint {
	base := strings.Join(prefix, " ")
	seen := map[string]Hint{}

	for seq, a := range km.bindings {
		next, rest, ok := childOf(base, seq)
		if !ok {
			continue
		}

		if rest {
			seen[next] = km.groupHint(base, next)

			continue
		}

		label := string(a)

		if info, ok := infoByAction[a]; ok {
			label = info.Label
		}

		seen[next] = Hint{Key: next, Label: label}
	}

	hints := make([]Hint, 0, len(seen))

	for _, h := range seen {
		hints = append(hints, h)
	}

	slices.SortFunc(hints, func(a, b Hint) int {
		if a.Group != b.Group {
			if a.Group {
				return 1
			}

			return -1
		}

		return strings.Compare(strings.ToLower(a.Key)+a.Key, strings.ToLower(b.Key)+b.Key)
	})

	return hints
}

// childOf reports the key after base in seq, and whether seq continues past it.
func childOf(base, seq string) (string, bool, bool) {
	rest := seq

	if base != "" {
		if !strings.HasPrefix(seq, base+" ") {
			return "", false, false
		}

		rest = seq[len(base)+1:]
	}

	next, after, found := strings.Cut(rest, " ")

	return next, found && after != "", true
}

// Keys returns the sequences bound to an action, in keymap order.
func (km *Keymap) Keys(a Action) []string {
	return km.byAction[a]
}

// Bindings returns every binding, ordered by action table then sequence.
func (km *Keymap) Bindings() []Binding {
	out := make([]Binding, 0, len(km.bindings))

	for _, info := range infos {
		for _, seq := range km.byAction[info.Action] {
			out = append(out, Binding{Sequence: seq, Action: info.Action})
		}
	}

	return out
}

// bindActions binds every action's sequences, in action table order.
func (km *Keymap) bindActions(raw map[string]any) error {
	for _, info := range infos {
		value, ok := raw[string(info.Action)]
		if !ok {
			continue
		}

		seqs, err := stringList(value)
		if err != nil {
			return fmt.Errorf("keymap %s: %w", info.Action, err)
		}

		for _, s := range seqs {
			if err := km.bind(info.Action, s); err != nil {
				return err
			}
		}
	}

	return nil
}

// checkNames parses the groups table and rejects unknown actions.
func (km *Keymap) checkNames(raw map[string]any) error {
	for name, value := range raw {
		if name == "groups" {
			if err := km.parseGroups(value); err != nil {
				return err
			}

			continue
		}

		if _, ok := infoByAction[Action(name)]; !ok {
			return fmt.Errorf("keymap: unknown action %q", name)
		}
	}

	return nil
}

func (km *Keymap) bind(a Action, seq string) error {
	keys, err := ParseSequence(seq)
	if err != nil {
		return fmt.Errorf("keymap %s: %w", a, err)
	}

	canon := strings.Join(keys, " ")

	if prev, ok := km.bindings[canon]; ok && prev != a {
		return fmt.Errorf("keymap: %q bound to both %s and %s", canon, prev, a)
	}

	km.bindings[canon] = a
	km.byAction[a] = append(km.byAction[a], canon)

	for i := 1; i < len(keys); i++ {
		km.prefixes[strings.Join(keys[:i], " ")] = true
	}

	return nil
}

func (km *Keymap) parseGroups(value any) error {
	table, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("keymap: groups must be a table")
	}

	for prefix, label := range table {
		keys, err := ParseSequence(prefix)
		if err != nil {
			return fmt.Errorf("keymap groups: %w", err)
		}

		s, ok := label.(string)
		if !ok {
			return fmt.Errorf("keymap groups: label for %q must be a string", prefix)
		}

		km.groups[strings.Join(keys, " ")] = s
	}

	return nil
}

func (km *Keymap) groupHint(base, next string) Hint {
	full := next
	if base != "" {
		full = base + " " + next
	}

	label := km.groups[full]
	if label == "" {
		label = "+more"
	}

	return Hint{Key: next, Label: label, Group: true}
}
