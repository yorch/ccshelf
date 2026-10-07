package updatecmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/yorch/ccshelf/internal/cache"
	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/ui"
	"github.com/yorch/ccshelf/internal/update"
	"github.com/yorch/ccshelf/internal/version"
)

// Options are the seams tests replace. The zero value is production
// behavior.
type Options struct {
	// Executable returns the path of the running binary (default
	// os.Executable).
	Executable func() (string, error)
	// Client performs HTTP requests (default: a client built for the source
	// by update.NewClient).
	Client *http.Client
	// GOOS and GOARCH select the release archive (default: the running
	// platform; they are NOT cc.GOOS, which only tests the output shape).
	GOOS, GOARCH string
	// StateDir is the directory of the lock, the state file and the downloads
	// (default: the ccshelf cache directory).
	StateDir string
	// LookCosign finds cosign (default: PATH from the Context's environment).
	LookCosign func() (string, bool)
	// CurrentVersion returns the running version (default version.Info).
	CurrentVersion func() string
	// Repo is the repository releases come from (default version.Repo).
	Repo string
	// AllowLoopbackHTTP lets a plain-http loopback base URL through
	// (default config.LoopbackHTTPAllowed, false in release builds).
	AllowLoopbackHTTP bool
	// InContainer reports a container image (default: marker files and
	// variables of the common runtimes).
	InContainer func(cc *clicore.Context) bool

	// StderrIsTerminal reports whether the error stream is a terminal
	// (default: it is an *os.File that is one). The automatic update does
	// nothing otherwise.
	StderrIsTerminal func(cc *clicore.Context) bool

	// Test seams passed through to the Updater.
	VerifyCosign func(ctx context.Context, cosignPath string, environ []string, bundle, checksums, identity, issuer string) error
	RunVersion   func(ctx context.Context, path string, environ []string) (string, error)
	InstallFn    func(newPath, exe string) error
	SwapFn       func(exe string) error
}

type command struct {
	get clicore.Provider
	opt Options
}

// Commands returns the update command.
func Commands(get clicore.Provider, opt Options) []*cobra.Command {
	c := &command{get: get, opt: opt}
	return []*cobra.Command{c.updateCmd()}
}

type flags struct {
	check, dryRun, prerelease, requireSignature bool
	rollback, yes, force, allowDowngrade        bool
	version                                     string
}

func (c *command) updateCmd() *cobra.Command {
	var f flags
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update ccshelf to the latest release (verified), or roll back",
		Long: `Download the newest ccshelf release, verify it and replace this binary.

The archive's SHA-256 must match checksums.txt of the same release, fetched
over HTTPS without credentials. If cosign is on PATH the keyless signature of
checksums.txt is verified as well, against the project's release workflow, and
a mismatch stops the update; --require-signature makes a missing cosign an
error. The previous binary is kept as <name>.old; --rollback restores it.

Nothing contacts the network unless you run this command or set [update] mode
in config.toml (off by default). A copy installed by Homebrew, Scoop, WinGet,
"go install" or a system package, and development builds, are not replaced
unless you pass --force; the right command is printed instead.`,
		Example: `  ccshelf update --check
  ccshelf update
  ccshelf update --version v0.2.0 --yes
  ccshelf update --rollback`,
		Args: cobra.NoArgs,
	}
	fl := cmd.Flags()
	fl.BoolVar(&f.check, "check", false, "only report the current and latest version (exit 0 either way)")
	fl.StringVar(&f.version, "version", "", "install this release (for example v0.2.0) instead of the latest")
	fl.BoolVar(&f.prerelease, "prerelease", false, "consider pre-releases when looking for the latest")
	fl.BoolVar(&f.dryRun, "dry-run", false, "show what would be downloaded, verified and replaced; change nothing")
	fl.BoolVar(&f.requireSignature, "require-signature", false, "fail unless cosign is available to verify the release signature")
	fl.BoolVar(&f.rollback, "rollback", false, "restore the previous binary kept by the last update")
	fl.BoolVar(&f.yes, "yes", false, "do not ask for confirmation")
	fl.BoolVar(&f.force, "force", false, "also replace a package-managed or development build, or reinstall the same version")
	fl.BoolVar(&f.allowDowngrade, "allow-downgrade", false, "allow --version to install an older release")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		cc, err := c.get()
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		cmd.SilenceUsage = true
		return c.run(ctx, cc, &f)
	}
	return cmd
}

// ---- wiring ------------------------------------------------------------

// loadConfig reads the user's configuration for the [update] section. A
// configuration that cannot be read is reported as a warning and replaced by
// the defaults: "ccshelf update" is how a broken installation gets fixed, so
// it must not depend on the file being valid.
func loadConfig(cc *clicore.Context) (*config.Config, string) {
	path := cc.G.ConfigPath
	if path == "" {
		p, err := config.Path()
		if err != nil {
			return config.Default(), ""
		}
		path = p
	}
	cfg, err := config.Load(path)
	if err != nil {
		return config.Default(), err.Error()
	}
	return cfg, ""
}

func (c *command) current() string {
	if c.opt.CurrentVersion != nil {
		return c.opt.CurrentVersion()
	}
	return version.Info().Version
}

func (c *command) repo() string {
	if c.opt.Repo != "" {
		return c.opt.Repo
	}
	return version.Repo
}

// newUpdater builds the Updater for this invocation.
func (c *command) newUpdater(cc *clicore.Context, cfg *config.Config) (*update.Updater, error) {
	loop := c.opt.AllowLoopbackHTTP || config.LoopbackHTTPAllowed
	src, err := update.NewSource(c.repo(), cfg.Update.BaseURL, loop)
	if err != nil {
		return nil, err
	}
	// The signer identity is the compiled-in repository unless the user named
	// another one; base_url only moves bytes and never changes who signs.
	if src, err = src.WithSigner(cfg.Update.CosignIdentityRepo); err != nil {
		return nil, err
	}
	if src, err = src.WithAssetHosts(cfg.Update.AssetHosts); err != nil {
		return nil, err
	}
	stateDir := c.opt.StateDir
	if stateDir == "" {
		if stateDir, err = cache.Dir(); err != nil {
			return nil, fmt.Errorf("preparing the cache directory: %w", err)
		}
	}
	goos, goarch := c.opt.GOOS, c.opt.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	cur := c.current()
	client := c.opt.Client
	if client == nil {
		client = update.NewClient(src, update.DownloadTimeout)
	}
	look := c.opt.LookCosign
	if look == nil {
		look = func() (string, bool) {
			return update.LookPath(cc.Getenv("PATH"), cc.Getenv("PATHEXT"), goos, "cosign")
		}
	}
	inContainer := c.opt.InContainer
	if inContainer == nil {
		inContainer = defaultInContainer
	}
	home, _ := os.UserHomeDir()
	return &update.Updater{
		Source:     src,
		Client:     client,
		UserAgent:  "ccshelf/" + strings.TrimPrefix(cur, "v"),
		Current:    cur,
		GOOS:       goos,
		GOARCH:     goarch,
		Executable: c.opt.Executable,
		Detect: update.DetectInput{
			Home:        home,
			GOBIN:       cc.Getenv("GOBIN"),
			GOPATH:      cc.Getenv("GOPATH"),
			InContainer: inContainer(cc),
		},
		LookCosign:   look,
		Environ:      cc.Environ,
		StateDir:     stateDir,
		Now:          cc.Now,
		VerifyCosign: c.opt.VerifyCosign,
		RunVersion:   c.opt.RunVersion,
		InstallFn:    c.opt.InstallFn,
		SwapFn:       c.opt.SwapFn,
	}, nil
}

// defaultInContainer recognizes the common container runtimes by their marker
// files and variables. It only reads; a false negative just means the
// container is treated like any other machine.
func defaultInContainer(cc *clicore.Context) bool {
	if cc.GOOS != "linux" {
		return false
	}
	for _, p := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return cc.Getenv("KUBERNETES_SERVICE_HOST") != "" || cc.Getenv("container") != ""
}

// canPrompt reports whether a person can be asked: the prompter is a real one
// and neither --no-interactive nor --json was given.
func canPrompt(cc *clicore.Context) bool {
	if cc.G.NoInteractive || cc.Mode.JSON {
		return false
	}
	_, non := cc.Prompt.(ui.NonInteractive)
	return !non
}

func status(cc *clicore.Context, level ui.Level, format string, a ...any) {
	fmt.Fprintln(cc.Streams.Err, ui.Status(cc.Mode, level, ui.Sanitize(fmt.Sprintf(format, a...))))
}

// info writes a plain progress line to the error stream.
func info(cc *clicore.Context, format string, a ...any) {
	fmt.Fprintln(cc.Streams.Err, ui.Sanitize(fmt.Sprintf(format, a...)))
}

func line(cc *clicore.Context, format string, a ...any) {
	fmt.Fprintln(cc.Streams.Out, ui.Sanitize(fmt.Sprintf(format, a...)))
}

// ---- command -----------------------------------------------------------

// report is the data of the "update" JSON envelope.
type report struct {
	// Action is check, up-to-date, dry-run, updated or rolled-back.
	Action          string `json:"action"`
	Current         string `json:"current"`
	Latest          string `json:"latest,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable"`
	ReleaseURL      string `json:"releaseUrl,omitempty"`
	InstallMethod   string `json:"installMethod,omitempty"`
	// Command is what to run instead when a package manager owns the binary.
	Command    string `json:"command,omitempty"`
	Executable string `json:"executable,omitempty"`
	Backup     string `json:"backup,omitempty"`
	// Signature is how checksums.txt was authenticated: cosign or none.
	Signature string `json:"signature,omitempty"`
}

func (c *command) run(ctx context.Context, cc *clicore.Context, f *flags) error {
	if err := checkFlags(f); err != nil {
		return err
	}
	cfg, cfgWarn := loadConfig(cc)
	if cfgWarn != "" {
		status(cc, ui.LevelWarn, "ignoring the configuration file: %s", cfgWarn)
	}
	u, err := c.newUpdater(cc, cfg)
	if err != nil {
		return ui.Failure(withHint(err, "check [update] base_url in config.toml"))
	}
	if f.rollback {
		return c.rollback(ctx, cc, u, f)
	}
	req := update.Request{
		Version: f.version, Prerelease: f.prerelease, AllowDowngrade: f.allowDowngrade,
		RequireSignature: f.requireSignature, Force: f.force,
	}
	// A signature that cannot be checked as required stops everything: before
	// the network, the dry run and the confirmation (a --check only reports).
	if !f.check {
		if err := u.CheckSignatureRequirement(req); err != nil {
			return mapError(cc, err, f, nil)
		}
	}
	dctx, cancel := context.WithTimeout(ctx, 2*update.MetadataTimeout)
	plan, err := u.Discover(dctx, req)
	cancel()
	if err != nil {
		return mapError(cc, err, f, nil)
	}
	rep := reportOf(plan)

	if f.check {
		rep.Action = "check"
		return c.printCheck(cc, plan, rep)
	}
	if plan.UpToDate && !f.force {
		rep.Action = "up-to-date"
		if cc.Mode.JSON {
			return ui.WriteJSON(cc.Streams.Out, "update", rep)
		}
		status(cc, ui.LevelOK, "ccshelf %s is up to date (latest release: %s)", plan.CurrentRaw, plan.Target.Version)
		return nil
	}
	if plan.Downgrade && !f.allowDowngrade {
		return mapError(cc, newDowngrade(plan), f, plan)
	}
	if err := u.Guard(plan, req); err != nil {
		return mapError(cc, err, f, plan)
	}
	if plan.ExeErr != nil || plan.Exe == "" {
		return ui.Failure(withHint(fmt.Errorf("cannot replace the running binary: %w", plan.ExeErr), "download the release by hand from %s", plan.Target.URL))
	}

	if f.dryRun {
		rep.Action = "dry-run"
		return c.printDryRun(cc, u, plan, rep)
	}
	if !f.yes {
		ok, err := c.confirm(ctx, cc, plan)
		if err != nil {
			return err
		}
		if !ok {
			info(cc, "not updated")
			return nil
		}
	}
	progress := func(s string) { info(cc, "%s", s) }
	if cc.Mode.JSON {
		progress = func(string) {}
	}
	actx, acancel := context.WithTimeout(ctx, 2*update.DownloadTimeout)
	defer acancel()
	res, err := u.Apply(actx, plan, req, progress)
	if err != nil {
		return mapError(cc, err, f, plan)
	}
	rep.Action = "updated"
	rep.Executable, rep.Backup, rep.Signature = res.Exe, res.Backup, res.Signature
	if cc.Mode.JSON {
		return ui.WriteJSON(cc.Streams.Out, "update", rep)
	}
	status(cc, ui.LevelOK, "updated ccshelf %s -> %s", res.From, res.To)
	if res.Signature == "cosign" {
		info(cc, "verified: SHA-256 and the cosign signature of checksums.txt")
	} else {
		info(cc, "verified: SHA-256 (cosign was not found, so the signature was not checked)")
	}
	info(cc, "the previous version is kept as %s; undo with: ccshelf update --rollback", filepath.Base(res.Backup))
	return nil
}

func checkFlags(f *flags) error {
	if f.rollback {
		for name, set := range map[string]bool{
			"--check": f.check, "--version": f.version != "", "--prerelease": f.prerelease,
			"--allow-downgrade": f.allowDowngrade, "--require-signature": f.requireSignature,
		} {
			if set {
				return ui.Usage(fmt.Errorf("--rollback cannot be combined with %s", name))
			}
		}
	}
	if f.version != "" {
		if _, err := update.ParseVersion(f.version); err != nil {
			return ui.Usage(withHint(fmt.Errorf("--version %q: %w", ui.Sanitize(f.version), err), "use a release version such as v0.2.0"))
		}
		if f.prerelease {
			return ui.Usage(errors.New("--version and --prerelease cannot be combined: a named version is installed as it is"))
		}
	}
	return nil
}

func reportOf(p *update.Plan) report {
	r := report{
		Current:         p.CurrentRaw,
		Latest:          p.Target.Version.String(),
		UpdateAvailable: !p.UpToDate && !p.Downgrade,
		ReleaseURL:      p.Target.URL,
		InstallMethod:   p.Method.Kind,
		Command:         p.Method.Command,
	}
	if p.Dev {
		r.UpdateAvailable = true
	}
	return r
}

func newDowngrade(p *update.Plan) error {
	return &update.Error{
		Kind: update.KindUsage, HintText: "pass --allow-downgrade to install an older version",
		Msg: fmt.Sprintf("%s is older than the running %s", p.Target.Tag, p.CurrentRaw),
	}
}

func (c *command) printCheck(cc *clicore.Context, p *update.Plan, rep report) error {
	if cc.Mode.JSON {
		return ui.WriteJSON(cc.Streams.Out, "update", rep)
	}
	line(cc, "current: %s", p.CurrentRaw)
	line(cc, "latest:  %s", p.Target.Version)
	switch {
	case p.Dev:
		line(cc, "this is a development build; the latest release is shown for reference")
	case rep.UpdateAvailable:
		line(cc, "an update is available: %s", p.Target.URL)
		if p.Method.SelfUpdatable() {
			line(cc, "run: ccshelf update")
		} else {
			line(cc, "installed with %s; run: %s", p.Method.Kind, p.Method.Command)
		}
	case p.Downgrade:
		line(cc, "%s is older than the running version", p.Target.Tag)
	default:
		line(cc, "ccshelf is up to date")
	}
	return nil
}

func (c *command) printDryRun(cc *clicore.Context, u *update.Updater, p *update.Plan, rep report) error {
	cosign, haveCosign := u.LookCosign()
	if haveCosign {
		rep.Signature = "cosign"
	} else {
		rep.Signature = "none"
	}
	rep.Executable, rep.Backup = p.Exe, p.Exe+update.BackupSuffix
	if cc.Mode.JSON {
		return ui.WriteJSON(cc.Streams.Out, "update", rep)
	}
	line(cc, "dry run: ccshelf %s -> %s (nothing was downloaded or changed)", p.CurrentRaw, p.Target.Version)
	line(cc, "  release:  %s", p.Target.URL)
	line(cc, "  download: %s", u.Source.AssetURL(p.Target.Tag, p.Archive.Name))
	if haveCosign {
		line(cc, "  verify:   SHA-256 against checksums.txt, and the cosign signature of checksums.txt (%s)", cosign)
		line(cc, "  signer:   %s", u.Source.CosignIdentity(p.Target.Tag))
	} else {
		line(cc, "  verify:   SHA-256 against checksums.txt (cosign was not found: the signature would not be checked)")
	}
	if extra := u.Source.ExtraAssetHosts(); len(extra) > 0 {
		line(cc, "  also trusting these download hosts from [update] asset_hosts: %s", strings.Join(extra, ", "))
	}
	line(cc, "  replace:  %s (the previous version is kept as %s)", p.Exe, filepath.Base(rep.Backup))
	return nil
}

// confirm shows current -> new and asks. Without a terminal the missing flag is
// named (exit 2). In a terminal the equivalent flag command is printed (R6).
func (c *command) confirm(ctx context.Context, cc *clicore.Context, p *update.Plan) (bool, error) {
	if !canPrompt(cc) {
		return false, ui.MissingFlags("confirm the update", "--yes")
	}
	fmt.Fprintf(cc.Streams.Err, "ccshelf %s -> %s\n", ui.Sanitize(p.CurrentRaw), p.Target.Version)
	fmt.Fprintf(cc.Streams.Err, "release notes: %s\n", ui.Sanitize(p.Target.URL))
	fmt.Fprintf(cc.Streams.Err, "replaces:      %s\n", ui.Sanitize(p.Exe))
	ok, err := cc.Prompt.Confirm(ctx, fmt.Sprintf("Update ccshelf to %s?", p.Target.Version), false)
	if err != nil {
		return false, err
	}
	if ok {
		rec := ui.NewRecorder("update")
		rec.Flag("--version", p.Target.Tag)
		rec.Bool("--yes")
		if err := rec.Print(cc.Streams.Err, cc.GOOS); err != nil {
			status(cc, ui.LevelWarn, "cannot print the equivalent command: %v", err)
		}
	}
	return ok, nil
}

func (c *command) rollback(ctx context.Context, cc *clicore.Context, u *update.Updater, f *flags) error {
	rp, err := u.PrepareRollback(ctx)
	if err != nil {
		return mapError(cc, err, f, nil)
	}
	if err := u.GuardRollback(rp, f.force); err != nil {
		return mapError(cc, err, f, nil)
	}
	rep := report{Action: "rolled-back", Current: c.current(), Latest: rp.BackupVersion, Executable: rp.Exe, Backup: rp.Backup, InstallMethod: rp.Method.Kind}
	if f.dryRun {
		rep.Action = "dry-run"
		if cc.Mode.JSON {
			return ui.WriteJSON(cc.Streams.Out, "update", rep)
		}
		line(cc, "dry run: would restore %s (%s) over %s; nothing was changed", filepath.Base(rp.Backup), orUnknown(rp.BackupVersion), rp.Exe)
		return nil
	}
	if !f.yes {
		if !canPrompt(cc) {
			return ui.MissingFlags("confirm the rollback", "--yes")
		}
		var ok bool
		if rp.Verified {
			fmt.Fprintf(cc.Streams.Err, "ccshelf %s -> %s (the previous version kept by the last update)\n", ui.Sanitize(c.current()), ui.Sanitize(rp.BackupVersion))
			ok, err = cc.Prompt.Confirm(ctx, "Restore the previous version?", false)
		} else {
			status(cc, ui.LevelWarn, "%s could not be checked: %v", filepath.Base(rp.Backup), rp.VerifyErr)
			ok, err = ui.ConfirmRisky(ctx, cc.Prompt, "Restore it anyway?")
		}
		if err != nil {
			return err
		}
		if !ok {
			info(cc, "not rolled back")
			return nil
		}
		rec := ui.NewRecorder("update")
		rec.Bool("--rollback")
		rec.Bool("--yes")
		_ = rec.Print(cc.Streams.Err, cc.GOOS)
	}
	if err := u.Rollback(rp); err != nil {
		return mapError(cc, err, f, nil)
	}
	if cc.Mode.JSON {
		return ui.WriteJSON(cc.Streams.Out, "update", rep)
	}
	status(cc, ui.LevelOK, "restored %s; the replaced version is kept as %s", orUnknown(rp.BackupVersion), filepath.Base(rp.Backup))
	return nil
}

func orUnknown(s string) string {
	if s == "" {
		return "version not checked"
	}
	return s
}

// ---- errors ------------------------------------------------------------

type hintError struct {
	err  error
	hint string
}

func (e *hintError) Error() string { return e.err.Error() }
func (e *hintError) Unwrap() error { return e.err }

// Hint returns the advice shown after the error.
func (e *hintError) Hint() string { return e.hint }

func withHint(err error, format string, a ...any) error {
	return &hintError{err: err, hint: fmt.Sprintf(format, a...)}
}

// mapError turns an update error into the exit code and hint the user sees.
// A directory the user cannot write gets the exact elevated command.
func mapError(cc *clicore.Context, err error, f *flags, p *update.Plan) error {
	var nw *update.NotWritableError
	switch update.KindOf(err) {
	case update.KindUsage:
		return ui.Usage(err)
	case update.KindNotWritable:
		if errors.As(err, &nw) {
			return ui.Failure(withHint(err, "%s", elevatedAdvice(cc, nw.Dir, f, p)))
		}
	case update.KindVerify:
		return ui.Failure(err)
	}
	if errors.Is(err, fs.ErrPermission) {
		return ui.Failure(withHint(err, "run it with the permissions that can write to the directory of ccshelf"))
	}
	return ui.Failure(err)
}

// elevatedAdvice is the exact command to run with higher privileges.
func elevatedAdvice(cc *clicore.Context, dir string, f *flags, p *update.Plan) string {
	exe := filepath.Join(dir, "ccshelf")
	if p != nil && p.Exe != "" {
		exe = p.Exe
	}
	sh := ui.ShellForGOOS(cc.GOOS)
	words := []string{exe, "update", "--yes"}
	switch {
	case f.rollback:
		words = []string{exe, "update", "--rollback", "--yes"}
	case f.version != "":
		words = append(words, "--version", f.version)
	case p != nil:
		words = append(words, "--version", p.Target.Tag)
	}
	cmdline, err := ui.Join(sh, words)
	if err != nil {
		cmdline = strings.Join(words, " ")
	}
	if cc.GOOS == "windows" {
		return fmt.Sprintf("%s is not writable by you; run this in a terminal started as Administrator: %s", dir, cmdline)
	}
	return fmt.Sprintf("%s is not writable by you; run: sudo %s", dir, cmdline)
}
