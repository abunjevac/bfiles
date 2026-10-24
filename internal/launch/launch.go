package launch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// Vars holds placeholder values. A template element that is exactly "{name}" with a
// list value expands into several arguments (used for {cmd}).
type Vars struct {
	Values map[string]string
	Lists  map[string][]string
}

// ErrEmptyCommand reports an unset command template.
var ErrEmptyCommand = errors.New("command is not configured")

// Expand substitutes placeholders in a command template.
func Expand(tmpl []string, v Vars) []string {
	out := make([]string, 0, len(tmpl))

	for _, arg := range tmpl {
		if strings.HasPrefix(arg, "{") && strings.HasSuffix(arg, "}") {
			if list, ok := v.Lists[arg[1:len(arg)-1]]; ok {
				out = append(out, list...)

				continue
			}
		}

		for k, val := range v.Values {
			arg = strings.ReplaceAll(arg, "{"+k+"}", val)
		}

		out = append(out, arg)
	}

	return out
}

// Detached starts argv in its own session with no terminal attached, so it outlives
// bfiles and cannot write over the UI.
func Detached(argv []string, dir string) error {
	if len(argv) == 0 || argv[0] == "" {
		return ErrEmptyCommand
	}

	path, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("%s: not found", argv[0])
	}

	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}

	defer devnull.Close()

	// the child must outlive bfiles, so it is not bound to any cancellable context
	cmd := exec.CommandContext(context.Background(), path, argv[1:]...)
	cmd.Dir = dir
	cmd.Stdin = devnull
	cmd.Stdout = devnull
	cmd.Stderr = devnull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", argv[0], err)
	}

	// reap the child so it does not linger as a zombie
	go func() { _ = cmd.Wait() }()

	return nil
}

// Editor returns the editor command: the configured template, $VISUAL, $EDITOR, or vi.
func Editor(configured []string) []string {
	if len(configured) > 0 {
		return configured
	}

	for _, env := range []string{"VISUAL", "EDITOR"} {
		if v := strings.Fields(os.Getenv(env)); len(v) > 0 {
			return append(v, "{file}")
		}
	}

	return []string{"vi", "{file}"}
}

// HasPlaceholder reports whether any template element mentions {name}.
func HasPlaceholder(tmpl []string, name string) bool {
	for _, a := range tmpl {
		if strings.Contains(a, "{"+name+"}") {
			return true
		}
	}

	return false
}

// Available reports whether the program of a template is on $PATH.
func Available(tmpl []string) bool {
	if len(tmpl) == 0 {
		return false
	}

	_, err := exec.LookPath(tmpl[0])

	return err == nil
}
