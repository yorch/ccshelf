package launcher

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/yorch/ccshelf/internal/cli/clicore"
	"github.com/yorch/ccshelf/internal/config"
	"github.com/yorch/ccshelf/internal/ui"
)

// cfgFile is an existing configuration file as it was read: the exact bytes
// (the write is refused if they change) and what they parse to.
type cfgFile struct {
	path string
	raw  []byte
	cfg  *config.Config
}

// openConfigFile reads the configuration for a command that changes or shows
// it. A missing file is a failure with a hint: ccshelf init is the only command
// that creates the file.
func openConfigFile(cc *clicore.Context) (*cfgFile, error) {
	path, err := configPath(cc)
	if err != nil {
		return nil, err
	}
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, ui.Failure(withHint(fmt.Errorf("there is no configuration file at %s", ui.SanitizeLine(path)),
			"create it first with: ccshelf init"))
	case err != nil:
		return nil, ui.Failure(fmt.Errorf("checking %s: %w", ui.SanitizeLine(path), err))
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, ui.Failure(withHint(fmt.Errorf("%s is a symbolic link", ui.SanitizeLine(path)),
			"ccshelf never replaces a link. Point --config at the real file"))
	case !fi.Mode().IsRegular():
		return nil, ui.Failure(fmt.Errorf("%s is not a regular file", ui.SanitizeLine(path)))
	}
	raw, err := config.ReadFile(path)
	if err != nil {
		return nil, ui.Failure(err)
	}
	cfg, err := config.ParseUnexpanded(raw, path)
	if err != nil {
		return nil, ui.Failure(withHint(err, "fix it with: ccshelf config edit"))
	}
	return &cfgFile{path: path, raw: raw, cfg: cfg}, nil
}

// clone returns a deep copy of the configuration, safe to change.
func (f *cfgFile) clone() *config.Config {
	c := *f.cfg
	c.Sources = append([]config.SourceConfig(nil), f.cfg.Sources...)
	c.Update.AssetHosts = append([]string(nil), f.cfg.Update.AssetHosts...)
	c.Accounts = make(map[string]config.Account, len(f.cfg.Accounts))
	for k, v := range f.cfg.Accounts {
		c.Accounts[k] = v
	}
	return &c
}

// writePlan is one change to the configuration file.
type writePlan struct {
	file *cfgFile
	// next is the new configuration, encoded on write (comments are lost).
	next *config.Config
	// nextRaw, for "config edit", is the new text, written as it is.
	nextRaw []byte
	// note is printed under the summary, for example that nothing is fetched.
	note string
	yes  bool
	// rec, when set, means the change was gathered by prompts: the equivalent
	// flag command is printed after a successful write.
	rec *ui.Recorder
}

type changeResult struct {
	Path           string   `json:"path"`
	Changed        bool     `json:"changed"`
	Backup         string   `json:"backup,omitempty"`
	Weakening      []string `json:"weakening"`
	CommentsLost   bool     `json:"comments_dropped"`
	NothingFetched bool     `json:"nothing_fetched_or_trusted"`
}

// commit shows what changes, asks when there is a terminal, and writes. It is
// the single path of every writing "config" subcommand.
func (l *launcher) commitConfig(ctx context.Context, cc *clicore.Context, w *writePlan) error {
	f := w.file
	next := w.next
	var data []byte
	var same bool
	if w.nextRaw != nil {
		var err error
		if next, err = config.ParseUnexpanded(w.nextRaw, f.path); err != nil {
			return ui.Failure(err)
		}
		data = w.nextRaw
		same = string(w.nextRaw) == string(f.raw)
	} else {
		if err := next.ValidateUnexpanded(); err != nil {
			return ui.Usage(fmt.Errorf("configuration: %s", renumberSources(err.Error())))
		}
		var err error
		if data, err = config.EncodeUnexpanded(next); err != nil {
			return ui.Failure(err)
		}
		cur, err := config.EncodeUnexpanded(f.cfg)
		if err != nil {
			return ui.Failure(err)
		}
		same = string(cur) == string(data)
	}
	// The diff is between the file as it is and the exact bytes that would be
	// written, so whatever re-encoding changes (comments, layout, empty
	// tables) is visible.
	before, after := splitLines(string(f.raw)), splitLines(string(data))
	res := changeResult{Path: f.path, Weakening: []string{}, NothingFetched: true}
	diff := diffLines(before, after)
	if same {
		return finishNoChange(cc, res)
	}
	res.Changed = true
	res.Weakening = append(res.Weakening, config.Weakening(f.cfg, next)...)
	res.CommentsLost = w.nextRaw == nil && config.HasComment(f.raw)
	weak := len(res.Weakening) > 0
	interactive := canPrompt(cc)

	if weak && !interactive && !w.yes {
		return ui.Usage(withHint(fmt.Errorf("this change weakens a security setting and needs --yes: %s", ui.SanitizeLine(strings.Join(res.Weakening, ", and "))),
			"re-run with --yes to confirm it, or leave it as it is"))
	}
	if interactive || weak {
		out := cc.Streams.Err
		fmt.Fprintf(out, "Change to %s (nothing has been written):\n", ui.SanitizeLine(f.path))
		const maxShown = 60
		for i, ln := range diff {
			if i == maxShown {
				fmt.Fprintf(out, "  ... and %d more changed lines\n", len(diff)-maxShown)
				break
			}
			fmt.Fprintf(out, "  %s\n", ui.SanitizeLine(ln))
		}
		if w.note != "" {
			fmt.Fprintf(out, "  %s\n", ui.SanitizeLine(w.note))
		}
	}
	for _, reason := range res.Weakening {
		warnf(cc, "this weakens a security setting: %s", ui.SanitizeLine(reason))
	}
	if res.CommentsLost {
		warnf(cc, "%s has comments, and they are dropped when the file is written again (the previous file is kept as %s)",
			ui.SanitizeLine(f.path), ui.SanitizeLine(config.BackupPath(f.path)))
	}
	if interactive && !w.yes {
		write, err := cc.Prompt.Confirm(ctx, "Write this configuration?", false)
		if err != nil {
			return err
		}
		if !write {
			return ui.Failure(errors.New("configuration not written"))
		}
	}
	// A cancellation must be seen after the last prompt and before the write,
	// even if a prompter answered after its context ended.
	if err := ctx.Err(); err != nil {
		return err
	}
	var err error
	if w.nextRaw != nil {
		res.Backup, err = config.SaveRawChecked(f.path, w.nextRaw, f.raw)
	} else {
		res.Backup, err = config.SaveRawChecked(f.path, data, f.raw)
	}
	switch {
	case errors.Is(err, config.ErrChangedWhileEditing):
		return ui.Failure(withHint(fmt.Errorf("%s changed while editing, nothing written", ui.SanitizeLine(f.path)),
			"run the command again to start from the current file"))
	case err != nil:
		return ui.Failure(fmt.Errorf("writing the configuration: %w", err))
	}
	if cc.Mode.JSON {
		return ui.WriteJSON(cc.Streams.Out, "config-change", res)
	}
	okf(cc, "wrote %s", f.path)
	okf(cc, "the previous file is kept as %s", config.BackupPath(f.path))
	if w.note != "" && !(interactive || weak) {
		fmt.Fprintf(cc.Streams.Err, "  %s\n", ui.SanitizeLine(w.note))
	}
	if w.rec != nil {
		printEquivalent(cc, w.rec)
	}
	return nil
}

var sourceIndexRe = regexp.MustCompile(`sources\[(\d+)\]`)

// renumberSources rewrites the 0-based "sources[2]" of validation errors as
// "source 3", the numbering of config show, ls, pin and rm.
func renumberSources(msg string) string {
	return sourceIndexRe.ReplaceAllStringFunc(msg, func(m string) string {
		n, _ := strconv.Atoi(m[len("sources[") : len(m)-1])
		return "source " + strconv.Itoa(n+1)
	})
}

func finishNoChange(cc *clicore.Context, res changeResult) error {
	if cc.Mode.JSON {
		return ui.WriteJSON(cc.Streams.Out, "config-change", res)
	}
	okf(cc, "no change: the configuration already has this, so nothing is written")
	return nil
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// diffLines returns the lines that differ, "- " for removed and "+ " for
// added, using the longest common subsequence so that repeated lines such as
// "type = \"git\"" are matched in order.
func diffLines(a, b []string) []string {
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	sa, sb := a[pre:], b[pre:]
	suf := 0
	for suf < len(sa) && suf < len(sb) && sa[len(sa)-1-suf] == sb[len(sb)-1-suf] {
		suf++
	}
	sa, sb = sa[:len(sa)-suf], sb[:len(sb)-suf]
	var out []string
	if len(sa)*len(sb) > 4_000_000 {
		for _, s := range sa {
			out = append(out, "- "+s)
		}
		for _, s := range sb {
			out = append(out, "+ "+s)
		}
		return out
	}
	n, m := len(sa), len(sb)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case sa[i] == sb[j]:
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && sa[i] == sb[j]:
			i++
			j++
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			out = append(out, "- "+sa[i])
			i++
		default:
			out = append(out, "+ "+sb[j])
			j++
		}
	}
	return out
}
