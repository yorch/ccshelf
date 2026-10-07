package update

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yorch/ccshelf/internal/config"
)

// Time budgets of the automatic path.
const (
	// AutoCheckTimeout is the hard overall limit of the periodic check.
	AutoCheckTimeout = 3 * time.Second
	// AutoInstallTimeout is the limit of an automatic install, which downloads
	// an archive; it only runs after a check found a newer release in policy.
	AutoInstallTimeout = 60 * time.Second
	// NoticeEvery is how often the "update available" line may be shown.
	NoticeEvery = 24 * time.Hour
	// KillSwitchEnv, when set to any non-empty value, turns every automatic
	// check off (the "ccshelf update" command is unaffected).
	KillSwitchEnv = "CCSHELF_NO_UPDATE_CHECK"
)

// AutoOptions is the policy of one invocation.
type AutoOptions struct {
	// Mode is the [update] mode: off, notify or install.
	Mode string
	// Interval is the minimum time between checks (default 24h).
	Interval time.Duration
	// CI is true when the CI environment variable is set; KillSwitch when
	// CCSHELF_NO_UPDATE_CHECK is.
	CI, KillSwitch bool
	// TTY is true when a person is looking: only then is a line printed.
	TTY bool
	// CheckTimeout and InstallTimeout override the budgets (tests).
	CheckTimeout, InstallTimeout time.Duration
}

func (o AutoOptions) off() bool {
	return (o.Mode != config.UpdateNotify && o.Mode != config.UpdateInstall) || o.CI || o.KillSwitch
}

func (o AutoOptions) interval() time.Duration {
	if o.Interval <= 0 {
		return config.DefaultUpdateInterval
	}
	return o.Interval
}

// Auto runs the opt-in periodic check. It never fails and never blocks for
// longer than its time budgets: every error ends in at most one line through
// say (only when TTY), and the next attempt waits for the interval. mode
// "notify" prints one line when a newer release exists (and, with nobody to
// tell, does not even contact the network); "install" also
// installs a newer release of the same major version (for 0.x, the same minor),
// never a downgrade and never a pre-release, and only for a binary ccshelf may
// replace; the new binary takes effect on the next invocation.
func (u *Updater) Auto(ctx context.Context, o AutoOptions, say func(string)) {
	if o.off() || IsDevVersion(u.Current) {
		return
	}
	if o.Mode == config.UpdateNotify && !o.TTY {
		return // nobody to tell: do not even ask the network
	}
	cur, err := ParseVersion(u.Current)
	if err != nil {
		return
	}
	now := u.now()
	st := LoadState(u.StateDir, now)
	updated := false
	if st.LastCheck.IsZero() || now.Sub(st.LastCheck) >= o.interval() {
		timeout := o.CheckTimeout
		if timeout <= 0 {
			timeout = AutoCheckTimeout
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		rel, err := u.fetcher().Latest(cctx, false)
		cancel()
		st.LastCheck = now
		if err != nil {
			_ = SaveState(u.StateDir, st)
			if o.TTY {
				say("ccshelf: could not check for updates (" + shortReason(err) + "); will try again later")
			}
			return
		}
		st.Latest = rel.Version.String()
		_ = SaveState(u.StateDir, st)
		if o.Mode == config.UpdateInstall && !rel.Prerelease && AutoEligible(cur, rel.Version) {
			updated = u.autoInstall(ctx, o, rel, say)
		}
	}
	if !updated {
		u.maybeNotice(o, cur, &st, say)
	}
}

// CachedNotice prints the "update available" line from the state file alone,
// with no network access. It is for the commands that start Claude Code and
// must stay fast, and it only acts in mode "notify".
func (u *Updater) CachedNotice(o AutoOptions, say func(string)) {
	if o.off() || o.Mode != config.UpdateNotify || IsDevVersion(u.Current) {
		return
	}
	cur, err := ParseVersion(u.Current)
	if err != nil {
		return
	}
	st := LoadState(u.StateDir, u.now())
	u.maybeNotice(o, cur, &st, say)
}

// maybeNotice prints the line when the state records a newer version and it
// has not been shown for NoticeEvery.
func (u *Updater) maybeNotice(o AutoOptions, cur Version, st *State, say func(string)) {
	if !o.TTY || st.Latest == "" {
		return
	}
	latest, err := ParseVersion(st.Latest)
	if err != nil || latest.Compare(cur) <= 0 || latest.IsPrerelease() {
		return
	}
	now := u.now()
	if !st.NotifiedAt.IsZero() && now.Sub(st.NotifiedAt) < NoticeEvery {
		return
	}
	run := "ccshelf update"
	p := &Plan{}
	u.locate(p)
	if !p.Method.SelfUpdatable() {
		run = p.Method.Command
	}
	say(fmt.Sprintf("ccshelf %s is available (you have %s). Run: %s", latest, cur, run))
	st.NotifiedAt = now
	_ = SaveState(u.StateDir, *st)
}

// autoInstall installs rel when it is allowed to; it reports whether it did.
func (u *Updater) autoInstall(ctx context.Context, o AutoOptions, rel Release, say func(string)) bool {
	plan, err := u.PlanFor(rel, Request{})
	if err != nil || u.Guard(plan, Request{}) != nil || plan.ExeErr != nil {
		return false // package-managed, development build, unwritable: only the notice
	}
	timeout := o.InstallTimeout
	if timeout <= 0 {
		timeout = AutoInstallTimeout
	}
	ictx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if o.TTY {
		say(fmt.Sprintf("ccshelf: updating %s -> %s ...", plan.Current, rel.Version))
	}
	res, err := u.Apply(ictx, plan, Request{}, nil)
	if err != nil {
		if KindOf(err) != KindLocked && o.TTY {
			say("ccshelf: automatic update failed (" + shortReason(err) + "); run: ccshelf update")
		}
		return false
	}
	say(fmt.Sprintf("ccshelf updated to %s (the previous version is kept as %s); it takes effect the next time you run ccshelf", res.To, res.Backup))
	return true
}

// shortReason is a one-line, bounded reason for a warning.
func shortReason(err error) string {
	var e *Error
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.As(err, &e):
		return strings.TrimSpace(e.Msg)
	}
	msg := strings.Join(strings.Fields(err.Error()), " ")
	if len(msg) > 120 {
		msg = msg[:120] + "..."
	}
	return msg
}
