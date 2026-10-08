package launcher

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/shellinit"
	"github.com/yorch/ccshelf/internal/ui"
	"github.com/yorch/ccshelf/internal/version"
)

func evalSymlinks(p string) (string, error) { return filepath.EvalSymlinks(p) }

func (l *launcher) versionCmd() *cobra.Command {
	c := &cobra.Command{Use: "version", Short: "Print the ccshelf version", Args: cobra.NoArgs}
	c.RunE = l.do(func(_ context.Context, cc *clicore.Context, _ *cobra.Command, _ []string) error {
		if cc.Mode.JSON {
			return ui.WriteJSON(cc.Streams.Out, "version", version.Info())
		}
		_, err := fmt.Fprintln(cc.Streams.Out, version.String())
		return err
	})
	return c
}

func (l *launcher) completionCmd() *cobra.Command {
	c := &cobra.Command{
		Use:       "completion <bash|zsh|fish|powershell>",
		Short:     "Print a shell completion script",
		Long:      "Print a completion script generated from the command definitions, for bash, zsh, fish or PowerShell.",
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		Args:      cobra.MaximumNArgs(1),
	}
	c.RunE = l.do(func(_ context.Context, cc *clicore.Context, cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return ui.Usage(withHint(errors.New("missing argument <shell>"), "one of: bash, zsh, fish, powershell"))
		}
		root := cmd.Root()
		out := cc.Streams.Out
		var err error
		switch args[0] {
		case "bash":
			err = root.GenBashCompletionV2(out, true)
		case "zsh":
			err = root.GenZshCompletion(out)
		case "fish":
			err = root.GenFishCompletion(out, true)
		case "powershell", "pwsh":
			err = root.GenPowerShellCompletionWithDesc(out)
		default:
			return ui.Usage(fmt.Errorf("unsupported shell %q: use bash, zsh, fish or powershell", ui.Sanitize(args[0])))
		}
		if err != nil {
			return ui.Failure(fmt.Errorf("generating completion: %w", err))
		}
		return nil
	})
	return c
}

func (l *launcher) shellInitCmd() *cobra.Command {
	var extra []string
	var shims string
	c := &cobra.Command{
		Use:   "shell-init [bash|zsh|fish|pwsh|cmd]",
		Short: "Print shell functions cs-<profile> that run each profile",
		Long: `Print one function per profile (cs-<profile>) that runs "ccshelf run <profile>"
with the remaining arguments passed through. Load it with:

  eval "$(ccshelf shell-init zsh)"          bash, zsh
  ccshelf shell-init fish | source          fish
  ccshelf shell-init pwsh | Out-String | Invoke-Expression

Only profiles from local directories are listed, so starting a shell never
touches the network. Add others with --profile. For cmd.exe, --write-cmd-shims
<dir> writes cs-<profile>.cmd files into a directory on PATH.`,
		Args: cobra.MaximumNArgs(1),
	}
	c.Flags().StringArrayVar(&extra, "profile", nil, "also define a function for this profile name (repeatable)")
	c.Flags().StringVar(&shims, "write-cmd-shims", "", "write cs-<profile>.cmd files into this directory (cmd.exe)")
	c.RunE = l.do(func(ctx context.Context, cc *clicore.Context, _ *cobra.Command, args []string) error {
		sh := first(args)
		if sh == "" && shims == "" {
			sh = detectShell(cc)
			if sh == "" {
				return ui.Usage(withHint(errors.New("missing argument <shell>"), "one of: %s", strings.Join(shellinit.Shells(), ", ")))
			}
		}
		s, err := l.open(ctx, cc, false, false)
		if err != nil {
			return err
		}
		names := map[string]bool{}
		for _, n := range extra {
			names[n] = true
		}
		for _, src := range s.sources {
			if src.Kind() == profile.KindProject {
				continue
			}
			ns, err := src.Names()
			if err != nil {
				return ui.Failure(fmt.Errorf("listing profiles in %s: %w", ui.Sanitize(profile.PortableSourceID(src)), err))
			}
			for _, n := range ns {
				names[n] = true
			}
		}
		var list []string
		for n := range names {
			if !shellinit.ValidProfileName(n) {
				warnf(cc, "skipping %q: not usable as a shell function name", n)
				continue
			}
			list = append(list, n)
		}
		exe, err := l.opt.Executable()
		if err != nil {
			return ui.Failure(fmt.Errorf("locating the ccshelf executable: %w", err))
		}
		if !filepath.IsAbs(exe) && !(len(exe) > 2 && exe[1] == ':') && !strings.HasPrefix(exe, "/") {
			if exe, err = filepath.Abs(exe); err != nil {
				return ui.Failure(fmt.Errorf("locating the ccshelf executable: %w", err))
			}
		}
		if shims != "" {
			written, err := shellinit.WriteCmdShims(shims, list, exe)
			if err != nil {
				return ui.Failure(err)
			}
			for _, w := range written {
				okf(cc, "wrote %s", w)
			}
			return nil
		}
		script, err := shellinit.Generate(sh, cc.GOOS, list, exe)
		if err != nil {
			return ui.Usage(fmt.Errorf("shell-init: %w", err))
		}
		_, err = fmt.Fprint(cc.Streams.Out, script)
		return err
	})
	return c
}

// detectShell guesses the shell from $SHELL (Unix) or says pwsh on Windows.
func detectShell(cc *clicore.Context) string {
	if cc.GOOS == "windows" {
		if cc.Getenv("PSModulePath") != "" {
			return shellinit.Pwsh
		}
		return ""
	}
	base := strings.ToLower(filepath.Base(cc.Getenv("SHELL")))
	for _, s := range []string{shellinit.Bash, shellinit.Zsh, shellinit.Fish} {
		if base == s {
			return s
		}
	}
	return ""
}
