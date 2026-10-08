package launcher

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/ui"
)

func (l *launcher) editCmd() *cobra.Command {
	var printPath bool
	c := &cobra.Command{
		Use:   "edit [profile]",
		Short: "Open a personal profile in $VISUAL or $EDITOR",
		Long: `Open one of your personal profiles in the editor named by $VISUAL or $EDITOR
(the command is split on spaces, quotes group words, and it is never run through
a shell), then check the result. Profiles from shared sources are read-only here: copy one with
"ccshelf new <name> --from <profile>" and edit the copy.

--path prints the file name instead of opening it, for scripts and for editors
you start yourself.`,
		Args: cobra.MaximumNArgs(1),
	}
	c.Flags().BoolVar(&printPath, "path", false, "print the profile file path instead of opening it")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, _ *cobra.Command, args []string) error {
		name, picked, err := pickProfile(ctx, cc, first(args), "edit", personalOptions)
		if err != nil {
			return err
		}
		if !profile.ValidName(name) {
			return ui.Usage(fmt.Errorf("invalid profile name %q", ui.Sanitize(name)))
		}
		dir, err := profile.PersonalDir()
		if err != nil {
			return fmt.Errorf("personal profiles directory: %w", err)
		}
		path := filepath.Join(dir, name+".toml")
		fi, err := os.Lstat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return ui.Usage(withHint(fmt.Errorf("no personal profile %q", ui.Sanitize(name)),
				"only your own profiles can be edited. Create one with: ccshelf new %s [--from <profile>]", name))
		case err != nil:
			return ui.Failure(err)
		case !fi.Mode().IsRegular():
			return ui.Failure(fmt.Errorf("%s is not a regular file", path))
		}
		if printPath {
			fmt.Fprintln(cc.Streams.Out, path)
			return nil
		}
		if picked {
			printEquivalent(cc, ui.NewRecorder("edit", name))
		}
		if err := l.runEditor(ctx, cc, path, "ccshelf edit "+name+" --path"); err != nil {
			return err
		}
		raw, err := os.ReadFile(path) //nolint:gosec // path is built from the validated name in the personal profiles directory
		if err != nil {
			return ui.Failure(fmt.Errorf("reading %s back: %w", path, err))
		}
		if _, err := profile.Parse(raw, name+".toml"); err != nil {
			return ui.Failure(withHint(fmt.Errorf("the edited profile is invalid: %w", err), "fix it with: ccshelf edit %s", name))
		}
		okf(cc, "%s is valid", name)
		l.warnUnresolved(ctx, cc, name)
		return nil
	})
	return c
}

// runEditor opens path in $VISUAL or $EDITOR and waits for it. pathCmd is the
// command shown in hints that prints the file name instead. It needs a
// terminal: an editor started for a script or a pipe could hang or write into
// the wrong stream.
func (l *launcher) runEditor(ctx context.Context, cc *clicore.Context, path, pathCmd string) error {
	if !canPrompt(cc) {
		return ui.Usage(withHint(errors.New("edit opens an editor and needs a terminal, but none is available"),
			"use: %s, and open the printed file yourself", pathCmd))
	}
	editor := cc.Getenv("VISUAL")
	if editor == "" {
		editor = cc.Getenv("EDITOR")
	}
	if editor == "" {
		return ui.Usage(withHint(errors.New("no editor configured"),
			"set $VISUAL or $EDITOR, or use: %s", pathCmd))
	}
	fields, err := parseEditor(editor)
	if err != nil {
		return ui.Usage(fmt.Errorf("$VISUAL or $EDITOR: %w", err))
	}
	if len(fields) == 0 {
		return ui.Usage(errors.New("$VISUAL and $EDITOR are blank"))
	}
	argv := append(append([]string(nil), fields[1:]...), path)
	// The editor shares the terminal, so Ctrl+C reaches it as well as us:
	// it must not cancel the editor (unsaved edits would be lost), so the
	// editor runs on a context that cannot be canceled and an interrupt
	// is swallowed here while it runs.
	release := ignoreInterrupt()
	code, err := l.opt.Spawn(context.WithoutCancel(ctx), fields[0], argv, cc.Environ(), cc.Streams.In, cc.Streams.Out, cc.Streams.Err)
	release()
	if err != nil {
		return ui.Failure(fmt.Errorf("running the editor %q: %w", ui.Sanitize(fields[0]), err))
	}
	if code != 0 {
		return ui.Failure(fmt.Errorf("the editor exited with status %d, and the file may be unchanged", code))
	}
	return nil
}

// warnUnresolved resolves the edited profile across all sources and warns when
// it cannot be resolved (a missing parent, an unknown MCP server). The edit
// itself is kept: this is a check, not a gate.
func (l *launcher) warnUnresolved(ctx context.Context, cc *clicore.Context, name string) {
	s, err := l.open(ctx, cc, true)
	if err != nil {
		warnf(cc, "could not check that %s resolves: %v", name, err)
		return
	}
	if _, err := s.resolve(name); err != nil {
		warnf(cc, "%s does not resolve yet: %v", name, err)
	}
}

// personalOptions lists the personal profiles for the picker.
func personalOptions() ([]ui.Option, error) {
	dir, err := profile.PersonalDir()
	if err != nil {
		return nil, fmt.Errorf("personal profiles directory: %w", err)
	}
	list, err := profile.List([]profile.Source{profile.DirSource(profile.KindPersonal, dir)})
	if err != nil {
		return nil, ui.Failure(err)
	}
	var opts []ui.Option
	for _, p := range list {
		opts = append(opts, ui.Option{Label: ui.Sanitize(p.Name), Detail: ui.Sanitize(p.Description), Value: p.Name})
	}
	sort.Slice(opts, func(i, j int) bool { return opts[i].Value < opts[j].Value })
	return opts, nil
}
