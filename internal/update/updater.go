package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Updater performs updates. Every field that reaches outside the process is a
// seam; the zero value of an optional seam means the production behavior.
type Updater struct {
	Source Source
	// Client performs HTTP requests (default [NewClient]).
	Client *http.Client
	// UserAgent is sent with every request ("ccshelf/<version>").
	UserAgent string
	// Current is the running version as printed by --version ("0.1.0" or
	// "dev").
	Current string
	// GOOS and GOARCH select the archive; they default to the running
	// platform's, set by the caller.
	GOOS, GOARCH string
	// Executable returns the path of the running binary (os.Executable).
	Executable func() (string, error)
	// Detect carries the environment the install-method detector needs
	// (home, GOBIN, GOPATH, container flag); GOOS, Paths and Repo are filled
	// in by the Updater.
	Detect DetectInput
	// LookCosign finds cosign (nil: not found).
	LookCosign func() (string, bool)
	// Environ is the environment passed (scrubbed) to cosign and to the
	// downloaded binary's version check.
	Environ func() []string
	// StateDir is the private cache directory holding the lock, the state file
	// and the download area.
	StateDir string
	// Now is the clock (default time.Now).
	Now func() time.Time

	// VerifyCosign, RunVersion, InstallFn and SwapFn replace the real steps in
	// tests.
	VerifyCosign func(ctx context.Context, cosignPath string, environ []string, bundle, checksums, identity, issuer string) error
	RunVersion   func(ctx context.Context, path string, environ []string) (string, error)
	InstallFn    func(newPath, exe string) error
	SwapFn       func(exe string) error
}

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func (u *Updater) fetcher() *Fetcher {
	return &Fetcher{Client: u.Client, Source: u.Source, UserAgent: u.UserAgent}
}

func (u *Updater) environ() []string {
	if u.Environ != nil {
		return u.Environ()
	}
	return os.Environ()
}

// Request is what the user asked for.
type Request struct {
	// Version pins an exact release ("v0.2.0"); empty means the latest.
	Version string
	// Prerelease lets the latest-release search include pre-releases.
	Prerelease bool
	// AllowDowngrade permits installing an older version than the running one.
	AllowDowngrade bool
	// RequireSignature makes a missing cosign an error.
	RequireSignature bool
	// Force overrides the package-manager and development-build refusals, and
	// reinstalls the same version. It never allows a downgrade.
	Force bool
}

// Plan is the outcome of looking for a release, before anything is changed.
type Plan struct {
	// CurrentRaw is the running version string; Current is parsed from it
	// (zero when Dev).
	CurrentRaw string
	Current    Version
	// Dev is true for a build that did not come from a release.
	Dev bool
	// Target is the release that would be installed.
	Target  Release
	Archive Archive
	// Exe is the file that would be replaced (symlinks resolved); ExeErr is
	// why it could not be determined or may not be replaced.
	Exe    string
	ExeErr error
	// Method is how this copy was installed.
	Method Method
	// UpToDate is true when Target is not newer than the running version.
	UpToDate bool
	// Downgrade is true whenever Target is older than the running version.
	Downgrade bool
}

// Discover finds the release to install and gathers what is needed to judge
// it. It changes nothing.
func (u *Updater) Discover(ctx context.Context, req Request) (*Plan, error) {
	if err := u.Source.Valid(); err != nil {
		return nil, newErr(KindFailure, "", err, "update source")
	}
	f := u.fetcher()
	var (
		rel Release
		err error
	)
	if req.Version != "" {
		if _, perr := ParseVersion(req.Version); perr != nil {
			return nil, newErr(KindUsage, "use a release version such as v0.2.0", perr, "--version %q", req.Version)
		}
		rel, err = f.ByTag(ctx, req.Version)
	} else {
		rel, err = f.Latest(ctx, req.Prerelease)
	}
	if err != nil {
		return nil, networkError(err, req.Version, u.Source)
	}
	return u.PlanFor(rel, req)
}

// PlanFor builds the Plan for an already discovered release.
func (u *Updater) PlanFor(rel Release, req Request) (*Plan, error) {
	p := &Plan{CurrentRaw: u.Current, Target: rel}
	if IsDevVersion(u.Current) {
		p.Dev = true
	} else if v, err := ParseVersion(u.Current); err != nil {
		p.Dev = true
	} else {
		p.Current = v
	}
	ar, aerr := ArchiveFor(rel.Version, u.GOOS, u.GOARCH)
	if aerr != nil {
		return nil, newErr(KindFailure, "build ccshelf from source for this platform, or use another machine", aerr, "no download for this platform")
	}
	p.Archive = ar
	if !p.Dev {
		switch c := rel.Version.Compare(p.Current); {
		case c == 0:
			p.UpToDate = true
		case c < 0:
			// An older release is a downgrade whether it was asked for by
			// name or is merely the newest one a (mirror or pre-release
			// feed) offers; only --allow-downgrade installs it, and --force
			// never implies it. Without a named version the plain answer is
			// still "up to date".
			p.Downgrade = true
			p.UpToDate = req.Version == ""
		}
	}
	u.locate(p)
	return p, nil
}

// locate fills Exe, ExeErr and Method.
func (u *Updater) locate(p *Plan) {
	exeFn := u.Executable
	if exeFn == nil {
		exeFn = os.Executable
	}
	exe, err := exeFn()
	if err != nil {
		p.ExeErr = fmt.Errorf("locating the running ccshelf: %w", err)
		return
	}
	if abs, aerr := filepath.Abs(exe); aerr == nil {
		exe = abs
	}
	in := u.Detect
	in.GOOS, in.Repo = u.GOOS, u.Source.Repo
	resolved, _, rerr := ResolveExecutable(exe)
	in.Paths = []string{exe}
	if r, e := filepath.EvalSymlinks(exe); e == nil && r != exe {
		in.Paths = append(in.Paths, r)
	}
	p.Method = DetectInstall(in)
	if rerr != nil {
		p.ExeErr = rerr
		return
	}
	p.Exe = resolved
}

// downloadError is the failure of fetching an asset; nothing was changed.
func downloadError(err error, name string) error {
	if errors.Is(err, ErrRedirect) {
		return newErr(KindFailure, redirectHint, err, "downloading %s: the download was redirected to a host that is not allowed", name)
	}
	return newErr(KindFailure, "nothing was changed; try again", err, "downloading %s", name)
}

const redirectHint = "if this server serves release assets from another host, list that exact hostname in [update] asset_hosts in config.toml (it is trusted with the download; the SHA-256 is still verified)"

func networkError(err error, version string, src Source) error {
	var he *HTTPError
	switch {
	case errors.Is(err, ErrRedirect):
		return newErr(KindFailure, redirectHint, err, "the download was redirected to a host that is not allowed")
	case errors.As(err, &he) && he.Status == http.StatusNotFound && version == "" && !strings.EqualFold(src.Web.Hostname(), DefaultHost) && strings.HasSuffix(strings.ToLower(src.Web.Hostname()), ".ghe.com"):
		return newErr(KindFailure, "GitHub Enterprise Cloud with data residency (*.ghe.com) keeps its API on api.<subdomain>.ghe.com, which this version does not support; download the release by hand", err, "the release information was not found")
	case errors.As(err, &he) && he.Status == http.StatusNotFound && version != "":
		return newErr(KindFailure, "see the releases page for the versions that exist", err, "release %s was not found", version)
	case errors.As(err, &he) && (he.Status == http.StatusForbidden || he.Status == http.StatusTooManyRequests):
		return newErr(KindFailure, "GitHub limits unauthenticated requests; try again later", err, "the release server refused the request")
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return newErr(KindFailure, "check your network connection and try again", err, "could not reach the release server in time")
	}
	return newErr(KindFailure, "check your network connection, or download the release by hand from its releases page", err, "could not get the release information")
}

// Guard refuses what must not be replaced without --force: a development
// build, and a binary a package manager owns.
func (u *Updater) Guard(p *Plan, req Request) error {
	if req.Force {
		return nil
	}
	if !p.Method.SelfUpdatable() {
		return newErr(KindManaged, "run: "+p.Method.Command+"   (or pass --force to replace the binary anyway)", nil,
			"this ccshelf was installed with %s and must be updated with it", p.Method.Kind)
	}
	if p.Dev {
		return newErr(KindManaged, "install a release, or pass --force to replace this build", nil,
			"this is a development build (%s), not a release", p.CurrentRaw)
	}
	return nil
}

// Result is what Apply did.
type Result struct {
	// From and To are the versions before and after (From may be "dev").
	From, To string
	// Exe is the replaced file and Backup the kept previous binary.
	Exe, Backup string
	// Signature says how checksums.txt was authenticated: "cosign" or "none".
	Signature string
}

func (u *Updater) cosign() (string, bool) {
	if u.LookCosign == nil {
		return "", false
	}
	return u.LookCosign()
}

// CheckSignatureRequirement fails with KindSignatureRequired when the request
// demands a verified signature and cosign is not available. The command
// calls it before it plans, prints the dry run or asks for confirmation, so
// that an update that cannot be verified as required never gets that far;
// Apply checks again.
func (u *Updater) CheckSignatureRequirement(req Request) error {
	if !req.RequireSignature {
		return nil
	}
	if _, ok := u.cosign(); ok {
		return nil
	}
	return newErr(KindSignatureRequired, "install cosign (https://docs.sigstore.dev/cosign/) and make sure it is on PATH, or run without --require-signature to rely on the SHA-256 check alone", nil,
		"--require-signature needs cosign, which is not on PATH")
}

// Apply downloads, verifies and installs plan.Target. progress receives short
// status lines. Nothing is replaced unless every check passes.
func (u *Updater) Apply(ctx context.Context, p *Plan, req Request, progress func(string)) (*Result, error) {
	if progress == nil {
		progress = func(string) {}
	}
	if err := u.Guard(p, req); err != nil {
		return nil, err
	}
	if p.Downgrade && !req.AllowDowngrade {
		return nil, newErr(KindUsage, "pass --allow-downgrade to install an older version", nil,
			"%s is older than the running %s", p.Target.Tag, p.CurrentRaw)
	}
	if p.ExeErr != nil || p.Exe == "" {
		return nil, newErr(KindFailure, "", p.ExeErr, "cannot replace the running binary")
	}
	dir := filepath.Dir(p.Exe)
	if err := CheckWritable(dir); err != nil {
		var nw *NotWritableError
		if errors.As(err, &nw) {
			return nil, newErr(KindNotWritable, "", err, "no permission to update %s", p.Exe)
		}
		return nil, newErr(KindFailure, "", err, "cannot update %s", p.Exe)
	}
	if err := u.CheckSignatureRequirement(req); err != nil {
		return nil, err
	}
	cosignPath, haveCosign := u.cosign()
	for _, name := range []string{p.Archive.Name, ChecksumsName} {
		if _, ok := p.Target.Asset(name); !ok {
			return nil, newErr(KindFailure, "the release may still be publishing; try again later", nil,
				"release %s has no %s (no build for %s/%s)", p.Target.Tag, name, u.GOOS, u.GOARCH)
		}
	}
	if err := os.MkdirAll(u.StateDir, 0o700); err != nil {
		return nil, newErr(KindFailure, "", err, "preparing the cache directory")
	}
	release, err := AcquireLock(u.StateDir, u.now)
	if err != nil {
		if errors.Is(err, ErrLocked) {
			return nil, newErr(KindLocked, "wait for it to finish; a lock left by a crash expires after 10 minutes", err, "another ccshelf update is already running")
		}
		return nil, newErr(KindFailure, "", err, "locking the update")
	}
	defer release()
	RemoveStale(dir, u.now().Add(-time.Hour))

	work, err := os.MkdirTemp(u.StateDir, "update-")
	if err != nil {
		return nil, newErr(KindFailure, "", err, "creating a download directory")
	}
	defer os.RemoveAll(work)

	f := u.fetcher()
	tag := p.Target.Tag
	progress("downloading " + ChecksumsName)
	sums, err := f.Get(ctx, u.Source.AssetURL(tag, ChecksumsName), "application/octet-stream", MaxChecksumsBytes)
	if err != nil {
		return nil, downloadError(err, ChecksumsName)
	}
	archivePath := filepath.Join(work, "archive")
	progress("downloading " + p.Archive.Name)
	sum, err := downloadHashed(ctx, f, u.Source.AssetURL(tag, p.Archive.Name), archivePath, p.Target, p.Archive.Name)
	if err != nil {
		return nil, downloadError(err, p.Archive.Name)
	}

	sig := "none"
	if haveCosign {
		progress("verifying the signature with cosign")
		if err := u.verifySignature(ctx, f, cosignPath, work, sums, tag); err != nil {
			return nil, err
		}
		sig = "cosign"
	}
	want, err := ParseChecksums(sums, p.Archive.Name)
	if err != nil {
		return nil, newErr(KindVerify, "nothing was changed", err, "reading %s", ChecksumsName)
	}
	if err := VerifySHA256(sum, want); err != nil {
		return nil, newErr(KindVerify, "nothing was changed; the download may be corrupt or tampered with", err, "verifying %s", p.Archive.Name)
	}

	tmp, err := CreateTemp(dir, u.GOOS)
	if err != nil {
		return nil, newErr(KindFailure, "", err, "preparing the new binary")
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := ExtractBinary(archivePath, p.Archive.Zip, p.Archive.Binary, tmp, MaxBinaryBytes); err != nil {
		return nil, newErr(KindVerify, "nothing was changed", err, "unpacking %s", p.Archive.Name)
	}
	if err := Finish(tmp, p.Exe); err != nil {
		return nil, newErr(KindFailure, "", err, "preparing the new binary")
	}
	progress("checking the new binary")
	got, err := u.runVersion(ctx, tmpName)
	if err != nil {
		return nil, newErr(KindVerify, "nothing was changed", err, "the downloaded binary does not run")
	}
	gv, perr := ParseVersion(got)
	if perr != nil || gv.Compare(p.Target.Version) != 0 {
		return nil, newErr(KindVerify, "nothing was changed", nil, "the downloaded binary reports version %q, not %s", got, p.Target.Version)
	}
	progress("installing")
	install := u.InstallFn
	if install == nil {
		install = Install
	}
	if err := install(tmpName, p.Exe); err != nil {
		return nil, newErr(KindFailure, "the previous binary is still in place", err, "replacing %s", p.Exe)
	}
	keep = true
	return &Result{From: u.Current, To: p.Target.Version.String(), Exe: p.Exe, Backup: p.Exe + BackupSuffix, Signature: sig}, nil
}

func (u *Updater) runVersion(ctx context.Context, path string) (string, error) {
	if u.RunVersion != nil {
		return u.RunVersion(ctx, path, u.environ())
	}
	return RunVersion(ctx, path, u.environ())
}

// downloadHashed downloads rawURL to path and returns the SHA-256 of what was
// written. The release's published size, when it lists one, must match.
func downloadHashed(ctx context.Context, f *Fetcher, rawURL, path string, rel Release, name string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // a new file in this update's private work directory
	if err != nil {
		return sum, err
	}
	h := sha256.New()
	n, err := f.Download(ctx, rawURL, "application/octet-stream", MaxArchiveBytes, io.MultiWriter(out, h))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return sum, err
	}
	if a, ok := rel.Asset(name); ok && a.Size > 0 && a.Size != n {
		return sum, fmt.Errorf("%w: got %d bytes, the release lists %d", ErrTruncated, n, a.Size)
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// verifySignature checks the keyless signature of checksums.txt with cosign.
func (u *Updater) verifySignature(ctx context.Context, f *Fetcher, cosignPath, work string, sums []byte, tag string) error {
	bundle, err := f.Get(ctx, u.Source.AssetURL(tag, SignatureName), "application/octet-stream", MaxChecksumsBytes)
	if err != nil {
		var he *HTTPError
		hint := "cosign is on PATH, so the signature is required; try again, or ask whoever publishes the release to check that it was signed"
		if errors.As(err, &he) && he.Status == http.StatusNotFound {
			hint = "this release carries no signature although cosign is installed; do not install it unless you can verify it another way"
		}
		return newErr(KindVerify, hint, err, "downloading %s", SignatureName)
	}
	sumsPath := filepath.Join(work, ChecksumsName)
	bundlePath := filepath.Join(work, SignatureName)
	for path, data := range map[string][]byte{sumsPath: sums, bundlePath: bundle} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return newErr(KindFailure, "", err, "writing %s", filepath.Base(path))
		}
	}
	verify := u.VerifyCosign
	if verify == nil {
		verify = VerifyCosign
	}
	if err := verify(ctx, cosignPath, u.environ(), bundlePath, sumsPath, u.Source.CosignIdentity(tag), OIDCIssuer); err != nil {
		return newErr(KindVerify, "nothing was changed; the release may not have been built by the project's release workflow", err, "the signature of %s is not valid", ChecksumsName)
	}
	return nil
}

// ErrNotCcshelf means a file ran but did not identify itself as ccshelf.
var ErrNotCcshelf = errors.New("not a ccshelf binary")

// RunVersion runs "<path> version --json" with a scrubbed environment and
// returns the version it reports. A failure to run is returned as is;
// output that is not a ccshelf version envelope is [ErrNotCcshelf].
func RunVersion(ctx context.Context, path string, environ []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "version", "--json") //nolint:gosec // path is the file this update just verified (or the backup being restored)
	cmd.Env = append(scrubEnv(environ), "CCSHELF_NO_UPDATE_CHECK=1")
	cmd.WaitDelay = execWaitDelay
	var out capWriter
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	var env struct {
		Kind string `json:"kind"`
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.NewDecoder(bytes.NewReader(out.b)).Decode(&env); err != nil || env.Kind != "version" || env.Data.Version == "" {
		return "", ErrNotCcshelf
	}
	return env.Data.Version, nil
}

// RollbackPlan describes restoring the previous binary.
type RollbackPlan struct {
	Exe, Backup string
	// BackupVersion is the version the backup reports, when it could be run.
	BackupVersion string
	// Verified is true when the backup ran and identified itself as ccshelf.
	Verified bool
	// VerifyErr says why it could not be verified.
	VerifyErr error
	Method    Method
}

// PrepareRollback locates and checks the backup. A backup that runs but is not
// ccshelf is an error; one that cannot be run at all (wrong platform, damaged)
// is returned unverified so that the caller can require explicit consent.
func (u *Updater) PrepareRollback(ctx context.Context) (*RollbackPlan, error) {
	p := &Plan{}
	u.locate(p)
	if p.ExeErr != nil || p.Exe == "" {
		return nil, newErr(KindFailure, "", p.ExeErr, "cannot locate the running binary")
	}
	rp := &RollbackPlan{Exe: p.Exe, Backup: p.Exe + BackupSuffix, Method: p.Method}
	fi, err := os.Lstat(rp.Backup)
	if err != nil || !fi.Mode().IsRegular() {
		return nil, newErr(KindNoBackup, "an update keeps the previous binary as "+filepath.Base(rp.Backup)+" next to ccshelf; there is none here", err, "there is no previous version to roll back to")
	}
	v, err := u.runVersion(ctx, rp.Backup)
	switch {
	case err == nil:
		rp.Verified, rp.BackupVersion = true, v
	case errors.Is(err, ErrNotCcshelf):
		return nil, newErr(KindVerify, "the file was not touched; delete it if it is not yours", err, "%s is not a ccshelf binary", rp.Backup)
	default:
		rp.VerifyErr = err
	}
	return rp, nil
}

// GuardRollback applies the package-manager refusal to a rollback.
func (u *Updater) GuardRollback(rp *RollbackPlan, force bool) error {
	if force || rp.Method.SelfUpdatable() {
		return nil
	}
	return newErr(KindManaged, "use "+rp.Method.Command+" (or pass --force)", nil,
		"this ccshelf was installed with %s; its backup is not managed here", rp.Method.Kind)
}

// Rollback swaps the backup into place. The replaced binary becomes the new
// backup, so a rollback can itself be undone.
func (u *Updater) Rollback(rp *RollbackPlan) error {
	if err := CheckWritable(filepath.Dir(rp.Exe)); err != nil {
		var nw *NotWritableError
		if errors.As(err, &nw) {
			return newErr(KindNotWritable, "", err, "no permission to roll back %s", rp.Exe)
		}
		return newErr(KindFailure, "", err, "cannot roll back %s", rp.Exe)
	}
	if err := os.MkdirAll(u.StateDir, 0o700); err != nil {
		return newErr(KindFailure, "", err, "preparing the cache directory")
	}
	release, err := AcquireLock(u.StateDir, u.now)
	if err != nil {
		if errors.Is(err, ErrLocked) {
			return newErr(KindLocked, "wait for it to finish", err, "another ccshelf update is already running")
		}
		return newErr(KindFailure, "", err, "locking the rollback")
	}
	defer release()
	swap := u.SwapFn
	if swap == nil {
		swap = Swap
	}
	if err := swap(rp.Exe); err != nil {
		return newErr(KindFailure, "", err, "restoring the previous version")
	}
	return nil
}
