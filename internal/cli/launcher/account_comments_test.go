package launcher

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/ui"
)

const commentWarning = " without its comments (the previous file, with the comments, is kept as "

func (h *harness) warnCount() int { return strings.Count(h.errb.String(), commentWarning) }

func TestAccountAddWarnsWhenCommentsDropped(t *testing.T) {
	h := newHarness(t)
	start := "# my notes\n[accounts.old]\nconfig_dir = \"/x/old\"\n"
	h.writeConfig(start)
	dir := filepath.Join(t.TempDir(), "claude-work")

	h.mustRun("account", "add", "work", "--dir", dir)
	if n := h.warnCount(); n != 1 {
		t.Fatalf("warning count = %d, stderr:\n%s", n, h.errb)
	}
	if h.out.Len() > 0 && strings.Contains(h.out.String(), commentWarning) {
		t.Errorf("warning on stdout:\n%s", h.out)
	}
	if !strings.Contains(h.errb.String(), "config.toml.bak") {
		t.Errorf("warning does not name the backup:\n%s", h.errb)
	}
	bak, err := os.ReadFile(h.configFile() + ".bak")
	if err != nil || string(bak) != start || !strings.Contains(string(bak), "# my notes") {
		t.Fatalf("backup = %q, %v", bak, err)
	}

	// The file has no comments now, and a repeated add writes nothing.
	h.mustRun("account", "add", "work", "--dir", dir)
	if n := h.warnCount(); n != 0 {
		t.Errorf("no-op add warned %d times:\n%s", n, h.errb)
	}
}

func TestAccountAddNoopOnCommentedFileDoesNotWarn(t *testing.T) {
	h := newHarness(t)
	dir := filepath.Join(t.TempDir(), "claude-work")
	h.writeConfig("[accounts.work]\nconfig_dir = '" + dir + "'\n# note\n")
	before := h.readConfig()
	h.mustRun("account", "add", "work", "--dir", dir)
	if n := h.warnCount(); n != 0 {
		t.Errorf("warned %d times:\n%s", n, h.errb)
	}
	if h.readConfig() != before {
		t.Error("a no-op add wrote the file")
	}
}

func TestAccountAddNoWarningWithoutCommentsOrFile(t *testing.T) {
	h := newHarness(t)
	// Missing file: it is created, and nothing is dropped.
	h.mustRun("account", "add", "one", "--dir", filepath.Join(t.TempDir(), "one"))
	if n := h.warnCount(); n != 0 {
		t.Errorf("create warned:\n%s", h.errb)
	}
	// An existing file without comments.
	h.mustRun("account", "add", "two", "--dir", filepath.Join(t.TempDir(), "two"))
	if n := h.warnCount(); n != 0 {
		t.Errorf("uncommented file warned:\n%s", h.errb)
	}
}

func TestAccountRmWarnsWhenCommentsDropped(t *testing.T) {
	h := newHarness(t)
	start := "# my notes\ndefault_account = \"work\"\n[accounts.work]\nconfig_dir = \"/x/work\"\n"
	h.writeConfig(start)
	h.mustRun("account", "rm", "work")
	if n := h.warnCount(); n != 1 {
		t.Fatalf("warning count = %d, stderr:\n%s", n, h.errb)
	}
	bak, err := os.ReadFile(h.configFile() + ".bak")
	if err != nil || string(bak) != start {
		t.Fatalf("backup = %q, %v", bak, err)
	}
	// An uncommented file does not warn.
	h.mustRun("account", "add", "b", "--dir", filepath.Join(t.TempDir(), "b"))
	h.mustRun("account", "rm", "b")
	if n := h.warnCount(); n != 0 {
		t.Errorf("uncommented rm warned:\n%s", h.errb)
	}
}

type accountRmDoc struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`
	Data    struct {
		Name             string `json:"name"`
		ConfigDir        string `json:"config_dir"`
		WasDefault       bool   `json:"was_default"`
		DirectoryDeleted bool   `json:"directory_deleted"`
		CommentsLost     bool   `json:"comments_dropped"`
	} `json:"data"`
}

func TestAccountRmJSON(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("# notes\ndefault_account = \"work\"\n[accounts.work]\nconfig_dir = \"/x/work\"\n[accounts.other]\nconfig_dir = \"/x/other\"\n")
	h.mustRun("--json", "account", "rm", "work")
	var doc accountRmDoc
	if err := json.Unmarshal(h.out.Bytes(), &doc); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, h.out)
	}
	d := doc.Data
	if doc.Version != 1 || doc.Kind != "account-rm" || d.Name != "work" || !d.WasDefault || d.DirectoryDeleted || !d.CommentsLost || !strings.HasSuffix(filepath.ToSlash(d.ConfigDir), "x/work") {
		t.Errorf("doc = %+v", doc)
	}
	if h.warnCount() != 1 {
		t.Errorf("warning count under --json = %d:\n%s", h.warnCount(), h.errb)
	}

	// Not the default, no comments now.
	h.mustRun("--json", "account", "rm", "other")
	doc = accountRmDoc{}
	if err := json.Unmarshal(h.out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Data.WasDefault || doc.Data.CommentsLost || doc.Data.Name != "other" {
		t.Errorf("doc = %+v", doc)
	}
	if h.warnCount() != 0 {
		t.Errorf("unexpected warning:\n%s", h.errb)
	}

	// Unknown account: same exit code and error form as without --json.
	if code := h.run("--json", "account", "rm", "nope"); code != ui.ExitUsage {
		t.Errorf("unknown account exit = %d", code)
	}
	if h.out.Len() != 0 || !strings.Contains(h.errb.String(), "removing account") {
		t.Errorf("out=%q err=%q", h.out, h.errb)
	}
}

func TestAccountAddJSONReportsCommentsDropped(t *testing.T) {
	h := newHarness(t)
	h.writeConfig("# notes\n")
	h.mustRun("--json", "account", "add", "work", "--dir", filepath.Join(t.TempDir(), "w"))
	var doc struct {
		Data struct {
			CommentsLost bool `json:"comments_dropped"`
		} `json:"data"`
	}
	if err := json.Unmarshal(h.out.Bytes(), &doc); err != nil || !doc.Data.CommentsLost {
		t.Fatalf("%v\n%s", err, h.out)
	}
	if h.warnCount() != 1 {
		t.Errorf("warning count = %d:\n%s", h.warnCount(), h.errb)
	}
}
