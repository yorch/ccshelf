package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCheckInstructionsText(t *testing.T) {
	refuse := map[string]string{
		// The corpus of bypasses found in review. Claude Code imports every one.
		"fence opener then fence in next file": "```\nhi\n\n```\n@/etc/passwd\n",
		"unterminated fence":                   "```\n@/etc/passwd\n",
		"code span then at":                    "`x`@/etc/passwd\n",
		"emphasis star":                        "*@/etc/passwd*\n",
		"strong":                               "**@/etc/passwd**\n",
		"emphasis underscore":                  "_@x_\n",
		"link text":                            "[@/etc/passwd](https://x)\n",
		"after link":                           "[a](b)@/etc/passwd\n",
		"escaped star":                         "\\*@/etc/passwd\n",
		"html tag then at":                     "<b>@/etc/passwd\n",
		"unmatched backtick heading":           "# Title `\n@/etc/passwd `\n",
		"unmatched backtick list":              "- a `\n- @/etc/passwd `\n",
		"escaped backtick":                     "\\` @/etc/passwd `\n",
		"nbsp before at":                       "a\u00a0@/etc/passwd\n",
		"line separator before at":             "a\u2028@/etc/passwd\n",
		"ideographic space before at":          "a\u3000@/etc/passwd\n",
		"bom":                                  "\ufeffhello\n",
		"fence closed by backtick tilde":       "```\n```~\n@/etc/passwd\n",
		"fence in comment":                     "<!--\n```\n-->\n@/etc/passwd\n```\n",
		"fence in html block":                  "<div>\n```\n</div>\n\n@/etc/passwd\n```\n",
		"lone cr":                              "a `x\r\r@/etc/passwd `\n",
		// Other refusals.
		"start of text":                  "@docs/x.md\n",
		"after space":                    "see @docs/x.md now\n",
		"after tab":                      "see\t@docs/x.md\n",
		"after newline":                  "one\n\n@x\n",
		"after paren":                    "(@x)\n",
		"second line of paragraph":       "one\n@x\n",
		"import after closed fence":      "```\n@a\n```\n@b\n",
		"import before closed fence":     "@a\n```\n@b\n```\n",
		"unclosed span with import":      "an ` @x\n",
		"span does not cross lines":      "start `a\n@x` end\n",
		"front matter":                   "---\nname: x\n---\n",
		"front matter with spaces":       "---  \nx\n",
		"html comment":                   "text <!-- hidden --> more\n",
		"html comment in code span":      "`<!--`\n",
		"html tag at line start":         "<div>\ntext\n",
		"closing tag at line start":      "</div>\n",
		"html tag after three spaces":    "   <span>x\n",
		"html tag inside a closed fence": "```\n<div>\n```\n",
		"declaration":                    "<!DOCTYPE html>\n",
		"short closer does not close":    "````\n@x\n```\n@y\n",
		"four space indent is no fence":  "    ```\n@x\n    ```\n",
		"tilde does not close backtick":  "```\n~~~\n@x\n",
		"closer with text":               "```\n``` x\n@y\n",
		"info string with backtick":      "``` a`b\n@x\n```\n",
	}
	for name, text := range refuse {
		t.Run("refuse "+name, func(t *testing.T) {
			if line, msg := CheckInstructionsText(text); line == 0 || msg == "" {
				t.Errorf("accepted %q", text)
			}
		})
	}
	allow := map[string]string{
		"empty":                       "",
		"plain":                       "Use the style guide.\n",
		"email":                       "mail ops@example.com please\n",
		"foo at bar":                  "foo@bar\n",
		"digit before at":             "user1@x.org\n",
		"unicode letter before at":    "josé@example.com\n",
		"lone at":                     "write @ then space\n",
		"at before backslash":         "@\\x\n",
		"at at end of text":           "ends with @",
		"escaped at":                  "an \\@docs/x.md here\n",
		"scoped package in code span": "install `@types/node` now\n",
		"double backtick span":        "see ``a ` @x`` now\n",
		"closed backtick fence":       "text\n```\n@x\n```\nafter\n",
		"fence with info string":      "```sh\n@x\n```\n",
		"closed tilde fence":          "~~~\n@x\n~~~\n",
		"fence indented three spaces": "   ```\n@x\n   ```\n",
		"longer fence needs longer":   "````\n```\n@x\n````\nend\n",
		"fence closer with spaces":    "```\n@x\n```  \t\n",
		"crlf fence":                  "```\r\n@x\r\n```\r\nend\r\n",
		"dashes after first line":     "Title\n---\n",
		"angle bracket not a tag":     "a < b and 3 <4\n",
		"line starts with spaced lt":  "< x\n",
	}
	for name, text := range allow {
		t.Run("allow "+name, func(t *testing.T) {
			if line, msg := CheckInstructionsText(text); line != 0 {
				t.Errorf("refused %q at line %d: %s", text, line, msg)
			}
		})
	}
	// Line numbers.
	for text, want := range map[string]int{"a\nb\n@x\n": 3, "a\r\nb\r\n@x\r\n": 3, "ok\n\n<div>\n": 3, "a\nb `\r\r": 2} {
		if line, _ := CheckInstructionsText(text); line != want {
			t.Errorf("%q: line %d; want %d", text, line, want)
		}
	}
}

func TestJoinInstructions(t *testing.T) {
	got := string(joinInstructions([][]byte{[]byte("a\n\n"), []byte("\n"), []byte("b"), []byte("  \n")}))
	if got != "a\n\nb\n" {
		t.Errorf("joined = %q", got)
	}
	if joinInstructions([][]byte{[]byte("\n")}) != nil {
		t.Error("only empty parts must give nil")
	}
}

func TestParseInstructions(t *testing.T) {
	m, err := Parse([]byte("name = \"a\"\n[instructions]\nfiles = [\"prompts/a.md\", \"prompts/sub/b.md\"]\ninherit = false\n"), "a.toml")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.Instructions.Files, []string{"prompts/a.md", "prompts/sub/b.md"}) || m.Instructions.Inherits() || !m.Instructions.Set() {
		t.Errorf("decoded = %+v", m.Instructions)
	}
	m, err = Parse([]byte("name = \"a\"\n"), "a.toml")
	if err != nil || m.Instructions.Set() || !m.Instructions.Inherits() {
		t.Errorf("default = %+v, %v", m.Instructions, err)
	}

	many := make([]string, MaxInstructionFiles+1)
	for i := range many {
		many[i] = fmt.Sprintf("%q", fmt.Sprintf("prompts/f%d.md", i))
	}
	bad := map[string]struct{ body, want string }{
		"unknown key":     {"[instructions]\nfiles = []\nextra = 1\n", "unknown key"},
		"not a prompt":    {"[instructions]\nfiles = [\"other/a.md\"]\n", "must be a file below prompts/"},
		"parent segment":  {"[instructions]\nfiles = [\"prompts/../a.md\"]\n", `".."`},
		"hidden":          {"[instructions]\nfiles = [\"prompts/.x.md\"]\n", "starting with"},
		"absolute":        {"[instructions]\nfiles = [\"/etc/passwd\"]\n", "relative"},
		"duplicate":       {"[instructions]\nfiles = [\"prompts/a.md\", \"prompts/a.md\"]\n", "duplicate entry"},
		"too many":        {"[instructions]\nfiles = [" + strings.Join(many, ",") + "]\n", "the limit is 32"},
		"inherit type":    {"[instructions]\ninherit = \"no\"\n", "inherit"},
		"wrong spelling":  {"[instructions]\nFiles = [\"prompts/a.md\"]\n", "Files"},
		"files not array": {"[instructions]\nfiles = \"prompts/a.md\"\n", "files"},
	}
	for name, c := range bad {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte("name = \"a\"\n"+c.body), "a.toml")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v; want containing %q", err, c.want)
			}
		})
	}
	if _, err := Parse([]byte("name = \"a\"\n[instructions]\nfiles = ["+strings.Join(many[:MaxInstructionFiles], ",")+"]\n"), "a.toml"); err != nil {
		t.Errorf("exactly the limit must pass: %v", err)
	}
}

// instrFiles builds a source tree where each profile has an [instructions]
// table. spec is "name|extends|inherit|files" with inherit "", "true" or "false".
func instrTree(profiles map[string]string, prompts map[string]string) map[string]string {
	files := map[string]string{}
	for k, v := range prompts {
		files["prompts/"+k] = v
	}
	for n, body := range profiles {
		files["profiles/"+n+".toml"] = "name = \"" + n + "\"\n" + body
	}
	return files
}

func instrPaths(r *Resolved) []string {
	var out []string
	for _, f := range r.Instructions {
		out = append(out, f.Profile+":"+f.Path)
	}
	return out
}

func resolveOne(t *testing.T, files map[string]string, name string) *Resolved {
	t.Helper()
	root := mk(t, files)
	r, err := Resolve(name, []Source{src(KindOrg, root)}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestInstructionsMerge(t *testing.T) {
	prompts := map[string]string{"a.md": "A\n", "b.md": "B\r\n", "c.md": "C\n", "d.md": "D\n"}
	run := func(name string, profiles map[string]string, top string, want []string, text string) {
		t.Run(name, func(t *testing.T) {
			r := resolveOne(t, instrTree(profiles, prompts), top)
			if got := instrPaths(r); !reflect.DeepEqual(got, want) {
				t.Errorf("files = %v; want %v", got, want)
			}
			if text != "" {
				text = InstructionsHeader(top) + text
			}
			if string(r.InstructionsText) != text {
				t.Errorf("text = %q; want %q", r.InstructionsText, text)
			}
		})
	}
	run("none", map[string]string{"x": ""}, "x", nil, "")
	run("single", map[string]string{"x": "[instructions]\nfiles = [\"prompts/a.md\"]\n"}, "x",
		[]string{"x:prompts/a.md"}, "A\n")
	run("child appends after parent", map[string]string{
		"p": "[instructions]\nfiles = [\"prompts/a.md\"]\n",
		"x": "extends = [\"p\"]\n[instructions]\nfiles = [\"prompts/b.md\"]\n",
	}, "x", []string{"p:prompts/a.md", "x:prompts/b.md"}, "A\n\nB\n")
	run("inherit true is the default", map[string]string{
		"p": "[instructions]\nfiles = [\"prompts/a.md\"]\n",
		"x": "extends = [\"p\"]\n[instructions]\ninherit = true\n",
	}, "x", []string{"p:prompts/a.md"}, "A\n")
	run("inherit false drops parents", map[string]string{
		"p": "[instructions]\nfiles = [\"prompts/a.md\"]\n",
		"x": "extends = [\"p\"]\n[instructions]\ninherit = false\nfiles = [\"prompts/b.md\"]\n",
	}, "x", []string{"x:prompts/b.md"}, "B\n")
	run("inherit false without files clears all", map[string]string{
		"p": "[instructions]\nfiles = [\"prompts/a.md\"]\n",
		"x": "extends = [\"p\"]\n[instructions]\ninherit = false\n",
	}, "x", nil, "")
	run("inherit false in the middle", map[string]string{
		"g": "[instructions]\nfiles = [\"prompts/a.md\"]\n",
		"m": "extends = [\"g\"]\n[instructions]\ninherit = false\nfiles = [\"prompts/b.md\"]\n",
		"x": "extends = [\"m\"]\n[instructions]\nfiles = [\"prompts/c.md\"]\n",
	}, "x", []string{"m:prompts/b.md", "x:prompts/c.md"}, "B\n\nC\n")
	run("multi parent follows chain order", map[string]string{
		"base": "[instructions]\nfiles = [\"prompts/a.md\"]\n",
		"l":    "extends = [\"base\"]\n[instructions]\nfiles = [\"prompts/b.md\"]\n",
		"r":    "extends = [\"base\"]\n[instructions]\nfiles = [\"prompts/c.md\"]\n",
		"x":    "extends = [\"l\", \"r\"]\n[instructions]\nfiles = [\"prompts/d.md\"]\n",
	}, "x", []string{"base:prompts/a.md", "l:prompts/b.md", "r:prompts/c.md", "x:prompts/d.md"}, "A\n\nB\n\nC\n\nD\n")
	run("multi parent reversed", map[string]string{
		"l": "[instructions]\nfiles = [\"prompts/b.md\"]\n",
		"r": "[instructions]\nfiles = [\"prompts/c.md\"]\n",
		"x": "extends = [\"r\", \"l\"]\n",
	}, "x", []string{"r:prompts/c.md", "l:prompts/b.md"}, "C\n\nB\n")
	run("inherit false keeps the files of a sibling parent", map[string]string{
		"l": "[instructions]\nfiles = [\"prompts/b.md\"]\n",
		"r": "[instructions]\ninherit = false\nfiles = [\"prompts/c.md\"]\n",
		"x": "extends = [\"l\", \"r\"]\n",
	}, "x", []string{"l:prompts/b.md", "r:prompts/c.md"}, "B\n\nC\n")
	run("sibling order does not change what stays", map[string]string{
		"l": "[instructions]\nfiles = [\"prompts/b.md\"]\n",
		"r": "[instructions]\ninherit = false\nfiles = [\"prompts/c.md\"]\n",
		"x": "extends = [\"r\", \"l\"]\n",
	}, "x", []string{"r:prompts/c.md", "l:prompts/b.md"}, "C\n\nB\n")
	diamond := func(order string) map[string]string {
		return map[string]string{
			"base": "[instructions]\nfiles = [\"prompts/a.md\"]\n",
			"l":    "extends = [\"base\"]\n[instructions]\nfiles = [\"prompts/b.md\"]\n",
			"r":    "extends = [\"base\"]\n[instructions]\ninherit = false\nfiles = [\"prompts/c.md\"]\n",
			"x":    "extends = " + order + "\n[instructions]\nfiles = [\"prompts/d.md\"]\n",
		}
	}
	run("diamond drops the common ancestor only", diamond(`["l", "r"]`), "x",
		[]string{"l:prompts/b.md", "r:prompts/c.md", "x:prompts/d.md"}, "B\n\nC\n\nD\n")
	run("diamond in the other order", diamond(`["r", "l"]`), "x",
		[]string{"r:prompts/c.md", "l:prompts/b.md", "x:prompts/d.md"}, "C\n\nB\n\nD\n")
	run("drop does not remove a later declaration of the same file", map[string]string{
		"base": "[instructions]\nfiles = [\"prompts/a.md\"]\n",
		"l":    "extends = [\"base\"]\n[instructions]\nfiles = [\"prompts/a.md\"]\n",
		"r":    "extends = [\"base\"]\n[instructions]\ninherit = false\n",
		"x":    "extends = [\"l\", \"r\"]\n",
	}, "x", []string{"l:prompts/a.md"}, "A\n")
	run("duplicate keeps the first", map[string]string{
		"p": "[instructions]\nfiles = [\"prompts/a.md\", \"prompts/b.md\"]\n",
		"x": "extends = [\"p\"]\n[instructions]\nfiles = [\"prompts/b.md\", \"prompts/c.md\"]\n",
	}, "x", []string{"p:prompts/a.md", "p:prompts/b.md", "x:prompts/c.md"}, "A\n\nB\n\nC\n")
	run("duplicate after a reset is kept", map[string]string{
		"p": "[instructions]\nfiles = [\"prompts/a.md\"]\n",
		"x": "extends = [\"p\"]\n[instructions]\ninherit = false\nfiles = [\"prompts/a.md\"]\n",
	}, "x", []string{"x:prompts/a.md"}, "A\n")
}

func TestInstructionsAcrossSources(t *testing.T) {
	org := mk(t, instrTree(map[string]string{
		"base": "[instructions]\nfiles = [\"prompts/shared.md\", \"prompts/org.md\"]\n",
	}, map[string]string{"shared.md": "ORG-SHARED\n", "org.md": "ORG\n"}))
	personal := mk(t, instrTree(map[string]string{
		"mine": "extends = [\"base\"]\n[instructions]\nfiles = [\"prompts/shared.md\", \"prompts/mine.md\"]\n",
	}, map[string]string{"shared.md": "PERSONAL-SHARED\n", "mine.md": "MINE\n"}))
	r, err := Resolve("mine", []Source{src(KindOrg, org), src(KindPersonal, personal)}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// The same path in another source is another file: each is read from the
	// root of the profile that declares it.
	want := []string{"base:prompts/shared.md", "base:prompts/org.md", "mine:prompts/shared.md", "mine:prompts/mine.md"}
	if got := instrPaths(r); !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v; want %v", got, want)
	}
	if string(r.InstructionsText) != InstructionsHeader("mine")+"ORG-SHARED\n\nORG\n\nPERSONAL-SHARED\n\nMINE\n" {
		t.Errorf("text = %q", r.InstructionsText)
	}
	for _, f := range r.Instructions {
		if strings.Contains(f.Source, string(filepath.Separator)) && !strings.HasPrefix(f.Source, "dir:") {
			t.Errorf("source label %q", f.Source)
		}
		if f.Source != "dir:org" && f.Source != "dir:personal" {
			t.Errorf("source label %q is not portable", f.Source)
		}
	}
}

func TestInstructionsRefusals(t *testing.T) {
	prof := func(files ...string) map[string]string {
		var q []string
		for _, f := range files {
			q = append(q, fmt.Sprintf("%q", f))
		}
		return map[string]string{"x": "[instructions]\nfiles = [" + strings.Join(q, ",") + "]\n"}
	}
	resolve := func(profiles, prompts map[string]string, setup func(root string)) error {
		root := mk(t, instrTree(profiles, prompts))
		if setup != nil {
			setup(root)
		}
		_, err := Resolve("x", []Source{src(KindOrg, root)}, ResolveOptions{})
		return err
	}
	t.Run("missing file", func(t *testing.T) {
		err := resolve(prof("prompts/gone.md"), nil, nil)
		if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "instructions.files of x") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("file over the per-file cap", func(t *testing.T) {
		err := resolve(prof("prompts/big.md"), map[string]string{"big.md": strings.Repeat("x", MaxPromptSize+1)}, nil)
		if err == nil || !strings.Contains(err.Error(), "instructions.files of x") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("total over the cap", func(t *testing.T) {
		half := strings.Repeat("y", MaxPromptSize/2+10)
		err := resolve(prof("prompts/a.md", "prompts/b.md", "prompts/c.md"), map[string]string{"a.md": half, "b.md": half, "c.md": half}, nil)
		if err == nil || !strings.Contains(err.Error(), "the limit is 65536 bytes") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("total at the cap passes", func(t *testing.T) {
		body := strings.Repeat("y", MaxInstructionsSize-1) + "\n"
		root := mk(t, instrTree(prof("prompts/a.md"), map[string]string{"a.md": body}))
		r, err := Resolve("x", []Source{src(KindOrg, root)}, ResolveOptions{})
		if err != nil || len(r.InstructionsText) != MaxInstructionsSize+len(InstructionsHeader("x")) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("import token", func(t *testing.T) {
		err := resolve(prof("prompts/a.md"), map[string]string{"a.md": "fine\r\nsee @../../secret.md\r\n"}, nil)
		if err == nil || !strings.Contains(err.Error(), "prompts/a.md of x, line 2") || !strings.Contains(err.Error(), "backticks") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("import in code is fine", func(t *testing.T) {
		if err := resolve(prof("prompts/a.md"), map[string]string{"a.md": "use `@x`\n```\n@y\n```\n"}, nil); err != nil {
			t.Error(err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		err := resolve(prof("prompts/link.md"), map[string]string{"real.md": "x"}, func(root string) {
			if e := os.Symlink(filepath.Join(root, "prompts", "real.md"), filepath.Join(root, "prompts", "link.md")); e != nil {
				t.Skip("symlinks are not available")
			}
		})
		if !errors.Is(err, ErrPath) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("source without a prompts folder", func(t *testing.T) {
		root := mk(t, map[string]string{"x.toml": "name = \"x\"\n[instructions]\nfiles = [\"prompts/a.md\"]\n"})
		_, err := Resolve("x", []Source{DirSource(KindOrg, root)}, ResolveOptions{})
		if !errors.Is(err, ErrPath) {
			t.Errorf("err = %v", err)
		}
	})
}

func TestInstructionsProjectRestrictions(t *testing.T) {
	for name, body := range map[string]string{
		"files":   "[instructions]\nfiles = [\"prompts/a.md\"]\n",
		"inherit": "[instructions]\ninherit = false\n",
	} {
		t.Run("set "+name, func(t *testing.T) {
			root := mk(t, instrTree(map[string]string{"p": body}, map[string]string{"a.md": "A"}))
			_, err := Resolve("p", []Source{src(KindProject, root)}, ResolveOptions{AllowProject: true})
			if !errors.Is(err, ErrProjectForbidden) || !strings.Contains(err.Error(), "instructions") {
				t.Errorf("err = %v", err)
			}
		})
	}
	org := mk(t, instrTree(map[string]string{
		"withinstr": "[instructions]\nfiles = [\"prompts/a.md\"]\n",
		"via":       "extends = [\"withinstr\"]\n",
		"plain":     "[plugins]\ninclude = [\"a@b\"]\n",
	}, map[string]string{"a.md": "A"}))
	for _, parent := range []string{"withinstr", "via"} {
		proj := mk(t, map[string]string{"profiles/p.toml": fmt.Sprintf("name = \"p\"\nextends = [%q]\n", parent)})
		_, err := Resolve("p", []Source{src(KindOrg, org), src(KindProject, proj)}, ResolveOptions{AllowProject: true})
		if !errors.Is(err, ErrProjectForbidden) || !strings.Contains(err.Error(), "instructions") {
			t.Errorf("parent %s: %v", parent, err)
		}
	}
	proj := mk(t, map[string]string{"profiles/p.toml": "name = \"p\"\nextends = [\"plain\"]\n"})
	r, err := Resolve("p", []Source{src(KindOrg, org), src(KindProject, proj)}, ResolveOptions{AllowProject: true})
	if err != nil || len(r.Instructions) != 0 {
		t.Errorf("plain parent: %v", err)
	}
}

func instrClosure(t *testing.T, topBody string, prompts map[string]string) *Resolved {
	t.Helper()
	return resolveOne(t, instrTree(map[string]string{"x": topBody}, prompts), "x")
}

func TestInstructionsClosure(t *testing.T) {
	prompts := map[string]string{"a.md": "A\n", "b.md": "B\n"}
	base := instrClosure(t, "[instructions]\nfiles = [\"prompts/a.md\", \"prompts/b.md\"]\n", prompts)

	var items []ClosureItem
	for _, it := range base.Closure.Items {
		if it.Kind == ItemInstruction {
			items = append(items, it)
			if !it.Risky {
				t.Errorf("%+v must be risky", it)
			}
			if strings.ContainsAny(it.Name, `\`) || strings.Contains(it.Name, os.TempDir()) {
				t.Errorf("machine path in %+v", it)
			}
		}
	}
	if len(items) != 3 || items[0].Name != "00 header" || items[0].Digest != digest([]byte(InstructionsHeader("x"))) ||
		items[1].Name != "01 prompts/a.md" || items[2].Name != "02 prompts/b.md" || items[1].Digest != digest([]byte("A\n")) {
		t.Fatalf("items = %+v", items)
	}

	again := instrClosure(t, "[instructions]\nfiles = [\"prompts/a.md\", \"prompts/b.md\"]\n", prompts)
	if again.Closure.Hash != base.Closure.Hash {
		t.Error("the hash must not depend on the machine path")
	}
	reordered := instrClosure(t, "[instructions]\nfiles = [\"prompts/b.md\", \"prompts/a.md\"]\n", prompts)
	if reordered.Closure.Hash == base.Closure.Hash {
		t.Error("a reorder must change the hash")
	}
	changed := instrClosure(t, "[instructions]\nfiles = [\"prompts/a.md\", \"prompts/b.md\"]\n", map[string]string{"a.md": "A\n", "b.md": "B!\n"})
	if changed.Closure.Hash == base.Closure.Hash {
		t.Error("a content change must change the hash")
	}
	fewer := instrClosure(t, "[instructions]\nfiles = [\"prompts/a.md\"]\n", prompts)
	if fewer.Closure.Hash == base.Closure.Hash {
		t.Error("removing a file must change the hash")
	}
	crlf := instrClosure(t, "[instructions]\nfiles = [\"prompts/a.md\", \"prompts/b.md\"]\n", map[string]string{"a.md": "A\r\n", "b.md": "B\r\n"})
	if crlf.Closure.Hash != base.Closure.Hash {
		t.Error("line ends must be normalized in the hash")
	}
	// Setting inherit changes what the profile does, so it changes the
	// controls digest even when the effective files stay the same.
	inh := instrClosure(t, "[instructions]\ninherit = false\nfiles = [\"prompts/a.md\", \"prompts/b.md\"]\n", prompts)
	if inh.Closure.Hash == base.Closure.Hash {
		t.Error("inherit = false must change the hash")
	}
}

func TestInstructionsKeepExistingHashes(t *testing.T) {
	// A profile without [instructions] must hash as it did before the table
	// existed: its controls JSON has no "instructions" member.
	b, err := ControlsJSON(&Manifest{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "instructions") {
		t.Errorf("controls JSON = %s", b)
	}
	b, err = ControlsJSON(&Manifest{Name: "a", Instructions: Instructions{Files: []string{"prompts/a.md", "prompts/b.md"}}})
	if err != nil || !strings.Contains(string(b), `"instructions.files":["prompts/a.md","prompts/b.md"]`) {
		t.Errorf("controls JSON = %s, %v", b, err)
	}
}

func TestInstructionsJoinedTextIsChecked(t *testing.T) {
	// Each file passes alone. The fence of the first file closes on the
	// opening line of the second, and the token comes out of the fence.
	files := instrTree(map[string]string{"x": "[instructions]\nfiles = [\"prompts/a.md\", \"prompts/b.md\"]\n"},
		map[string]string{"a.md": "```\nhi\n", "b.md": "```\n@/etc/passwd\n```\n"})
	for _, f := range []string{"a.md", "b.md"} {
		if line, msg := CheckInstructionsText(files["prompts/"+f]); line != 0 {
			t.Fatalf("%s alone: line %d: %s", f, line, msg)
		}
	}
	root := mk(t, files)
	_, err := Resolve("x", []Source{src(KindOrg, root)}, ResolveOptions{})
	if err == nil || !strings.Contains(err.Error(), "the joined instructions text of x") || !strings.Contains(err.Error(), "import token") {
		t.Errorf("err = %v", err)
	}
}

func TestInstructionsFileRefusals(t *testing.T) {
	for name, c := range map[string]struct{ text, want string }{
		"front matter": {"---\ntitle: x\n---\nBody\n", "front matter"},
		"bom":          {"\ufeffText\n", "byte order mark"},
		"comment":      {"a\n<!-- x -->\n", "HTML comment"},
		"html":         {"a\n<div>\n", "HTML tag"},
		"lone cr":      {"a\rb\n", "carriage return"},
	} {
		t.Run(name, func(t *testing.T) {
			root := mk(t, instrTree(map[string]string{"x": "[instructions]\nfiles = [\"prompts/a.md\"]\n"}, map[string]string{"a.md": c.text}))
			_, err := Resolve("x", []Source{src(KindOrg, root)}, ResolveOptions{})
			if err == nil || !strings.Contains(err.Error(), "prompts/a.md of x") || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestInstructionsCutBy(t *testing.T) {
	prompts := map[string]string{"a.md": "A\n", "b.md": "B\n"}
	r := resolveOne(t, instrTree(map[string]string{
		"base": "[instructions]\nfiles = [\"prompts/a.md\"]\n",
		"l":    "[instructions]\nfiles = [\"prompts/b.md\"]\n",
		"x":    "extends = [\"base\", \"l\"]\n[instructions]\ninherit = false\n",
	}, prompts), "x")
	if r.InstructionsCutBy != "x" || len(r.Instructions) != 0 {
		t.Errorf("cut by %q, files %v", r.InstructionsCutBy, instrPaths(r))
	}
	// Nothing to drop: no cut is reported.
	r = resolveOne(t, instrTree(map[string]string{
		"l": "[instructions]\nfiles = [\"prompts/b.md\"]\n",
		"r": "[instructions]\ninherit = false\n",
		"x": "extends = [\"l\", \"r\"]\n",
	}, prompts), "x")
	if r.InstructionsCutBy != "" || len(r.Instructions) != 1 {
		t.Errorf("cut by %q, files %v", r.InstructionsCutBy, instrPaths(r))
	}
}
