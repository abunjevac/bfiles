package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/urfave/cli/v3"

	"github.com/abunjevac/bfiles/internal/config"
	"github.com/abunjevac/bfiles/internal/keymap"
	"github.com/abunjevac/bfiles/internal/location"
	"github.com/abunjevac/bfiles/internal/theme"
	"github.com/abunjevac/bfiles/internal/ui"
	"github.com/abunjevac/bfiles/internal/version"
)

func main() {
	configFlag := &cli.StringFlag{
		Name:    "config",
		Aliases: []string{"c"},
		Usage:   "config directory (default: ~/.config/bfiles)",
	}

	cmd := &cli.Command{
		Name:      "bfiles",
		Version:   version.Version,
		Usage:     "a fast two-panel terminal file manager",
		ArgsUsage: "[LEFT] [RIGHT]",
		Description: "LEFT and RIGHT are local directories, archive files, or SSH targets\n" +
			"(ssh://user@host:port/path, user@host:/path, host:path). With one location,\n" +
			"both panels open there; with none, both open in the current directory.",
		Flags: []cli.Flag{
			configFlag,
			&cli.StringFlag{Name: "theme", Aliases: []string{"t"}, Usage: "theme for this run (not saved)"},
			&cli.BoolFlag{Name: "hidden", Usage: "start with hidden files shown"},
		},
		Action: run,
		Commands: []*cli.Command{
			{
				Name:  "keymap",
				Usage: "print the default keymap, or reset keymap.toml to the override template",
				Flags: []cli.Flag{
					configFlag,
					&cli.BoolFlag{Name: "write", Aliases: []string{"w"}, Usage: "overwrite keymap.toml with the template (all defaults, commented out)"},
				},
				Action: keymapCommand,
			},
			{
				Name:   "themes",
				Usage:  "list available themes",
				Flags:  []cli.Flag{configFlag},
				Action: themesCommand,
			},
			{
				Name:   "config",
				Usage:  "print the config directory (creating it if needed)",
				Flags:  []cli.Flag{configFlag},
				Action: configCommand,
			},
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "bfiles: %v\n", err)

		os.Exit(1)
	}
}

func run(ctx context.Context, cmd *cli.Command) error {
	args := cmd.Args().Slice()

	if len(args) > 2 {
		return fmt.Errorf("at most two locations can be given, got %d", len(args))
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("working directory: %w", err)
	}

	specs := make([]location.Spec, 0, 2)

	for _, a := range args {
		s, parseErr := location.ParseArg(a, cwd)
		if parseErr != nil {
			return parseErr
		}

		specs = append(specs, s)
	}

	switch len(specs) {
	case 0:
		here := location.Spec{Kind: location.Local, Path: cwd}

		specs = append(specs, here, here)
	case 1:
		specs = append(specs, specs[0])
	}

	bundle, err := loadBundle(cmd)
	if err != nil {
		return err
	}

	if name := cmd.String("theme"); name != "" && !slices.Contains(theme.List(bundle.Dir), name) {
		return fmt.Errorf("unknown theme %q (see: bfiles themes)", name)
	}

	return ui.Run(ctx, bundle, specs[0], specs[1], ui.Options{Theme: cmd.String("theme"), Hidden: cmd.Bool("hidden")})
}

func loadBundle(cmd *cli.Command) (*config.Bundle, error) {
	dir, err := config.ResolveDir(cmd.String("config"))
	if err != nil {
		return nil, err
	}

	b, err := config.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("%w (fix %s or run with another --config)", err, filepath.Join(dir, "config.toml"))
	}

	return b, nil
}

func keymapCommand(_ context.Context, cmd *cli.Command) error {
	if !cmd.Bool("write") {
		_, _ = fmt.Fprint(os.Stdout, keymap.DefaultTOML)

		return nil
	}

	dir, err := config.ResolveDir(cmd.String("config"))
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	dst := filepath.Join(dir, "keymap.toml")

	if err := os.WriteFile(dst, []byte(keymap.Template()), 0o644); err != nil { //nolint:gosec // config files are user-readable
		return fmt.Errorf("write keymap: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "keymap written to %s\n", dst)

	return nil
}

func themesCommand(_ context.Context, cmd *cli.Command) error {
	dir, err := config.ResolveDir(cmd.String("config"))
	if err != nil {
		return err
	}

	for _, n := range theme.List(dir) {
		_, _ = fmt.Fprintln(os.Stdout, n)
	}

	return nil
}

func configCommand(_ context.Context, cmd *cli.Command) error {
	b, err := loadBundle(cmd)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintln(os.Stdout, b.Dir)

	return nil
}
