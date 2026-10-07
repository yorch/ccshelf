package scaffold

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateAcceptsGoodValues(t *testing.T) {
	p := Params{
		Mode: ModeAdopt, MarketplaceName: "acme-2", Org: "Acme Corp (EU) & Sons", Owner: "@acme/web",
		PlatformOwners: []string{"@acme/platform", "@jdoe", "ops@acme.example", "@acme/platform"},
		CcshelfRef:     strings.Repeat("a", 40), CcshelfVersion: "v1.2.3-rc.1", RunnerLabel: "self-hosted.linux_x64",
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(p.PlatformOwners) != 3 {
		t.Errorf("duplicates were kept: %v", p.PlatformOwners)
	}
	if err := (&Params{CcshelfRef: "v0.1.0", CcshelfVersion: "v0.1.0"}).Validate(); err != nil {
		t.Errorf("a tag as ref with the same version: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	long := strings.Repeat("a", 65)
	cases := []struct {
		name string
		p    Params
		flag string
	}{
		{"mode", Params{Mode: "x"}, "--mode"},
		{"name upper", Params{MarketplaceName: "Acme"}, "--marketplace-name"},
		{"name leading hyphen", Params{MarketplaceName: "-a"}, "--marketplace-name"},
		{"name too long", Params{MarketplaceName: long}, "--marketplace-name"},
		{"name newline", Params{MarketplaceName: "a\nb"}, "--marketplace-name"},
		{"name quote", Params{MarketplaceName: `a"b`}, "--marketplace-name"},
		{"name slash", Params{MarketplaceName: "a/b"}, "--marketplace-name"},
		{"name unicode digit", Params{MarketplaceName: "a٣"}, "--marketplace-name"},
		{"org control", Params{Org: "a\x1b[31m"}, "--org"},
		{"org bidi", Params{Org: "a\u202eb"}, "--org"},
		{"org zero width", Params{Org: "a\u200bb"}, "--org"},
		{"org line separator", Params{Org: "a\u2028b"}, "--org"},
		{"org invalid utf8", Params{Org: "a\xffb"}, "--org"},
		{"org tab", Params{Org: "a\tb"}, "--org"},
		{"org padded", Params{Org: "a "}, "--org"},
		{"org long", Params{Org: strings.Repeat("é", 101)}, "--org"},
		{"owner space", Params{Owner: "@a b"}, "--owner"},
		{"owner no at", Params{Owner: "acme/team"}, "--owner"},
		{"owner hash", Params{Owner: "@a#b"}, "--owner"},
		{"owner two slashes", Params{Owner: "@a/b/c"}, "--owner"},
		{"owner email space", Params{Owner: "a b@x.example"}, "--owner"},
		{"owner email newline", Params{Owner: "a@x.example\n@evil"}, "--owner"},
		{"platform bad", Params{PlatformOwners: []string{"@ok", "bad"}}, "--platform-owners"},
		{"platform many", Params{PlatformOwners: func() []string {
			var o []string
			for i := range 21 {
				o = append(o, "@u"+string(rune('a'+i)))
			}
			return o
		}()}, "--platform-owners"},
		{"ref branch", Params{CcshelfRef: "main"}, "--ccshelf-ref"},
		{"ref short sha", Params{CcshelfRef: "abc123"}, "--ccshelf-ref"},
		{"ref upper sha", Params{CcshelfRef: strings.Repeat("A", 40)}, "--ccshelf-ref"},
		{"ref long sha", Params{CcshelfRef: strings.Repeat("a", 41)}, "--ccshelf-ref"},
		{"ref tag two parts", Params{CcshelfRef: "v1.2"}, "--ccshelf-ref"},
		{"ref tag leading zero", Params{CcshelfRef: "v01.2.3"}, "--ccshelf-ref"},
		{"ref newline", Params{CcshelfRef: "v1.2.3\n"}, "--ccshelf-ref"},
		{"version sha", Params{CcshelfVersion: strings.Repeat("a", 40)}, "--ccshelf-version"},
		{"version yaml", Params{CcshelfVersion: "v1.2.3 # x"}, "--ccshelf-version"},
		{"version conflict", Params{CcshelfRef: "v1.2.3", CcshelfVersion: "v1.2.4"}, "--ccshelf-version"},
		{"runner quote", Params{RunnerLabel: "a'b"}, "--runner-label"},
		{"runner expression", Params{RunnerLabel: "${{x}}"}, "--runner-label"},
		{"runner space", Params{RunnerLabel: "a b"}, "--runner-label"},
		{"runner long", Params{RunnerLabel: long}, "--runner-label"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.p.Validate()
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Flag != tc.flag {
				t.Fatalf("err = %v, want a FieldError for %s", err, tc.flag)
			}
			for _, bad := range []string{"\x1b", "\u202e", "\x00", "\n"} {
				if strings.Contains(fe.Error(), bad) {
					t.Errorf("the message echoes %q: %q", bad, fe.Error())
				}
			}
			// Build refuses it as well and writes nothing.
			if _, err := Build(Empty(), tc.p); err == nil {
				t.Error("Build accepted it")
			}
		})
	}
}

func TestEnabled(t *testing.T) {
	p := Params{Skip: map[Group]bool{GroupReadme: true}}
	if p.Enabled(GroupReadme) || !p.Enabled(GroupConfig) || p.Enabled(GroupExampleProfile) {
		t.Error("Enabled is wrong")
	}
	p.ExampleProfile = true
	if !p.Enabled(GroupExampleProfile) {
		t.Error("the example profile was requested")
	}
}

func TestMissingErrorMessage(t *testing.T) {
	e := &MissingError{Flags: []string{"--a", "--b"}}
	if got := e.Error(); got != "missing required flag --a, --b" {
		t.Error(got)
	}
	var fe error = &FieldError{"--x", "bad"}
	if fe.Error() != "--x: bad" {
		t.Error(fe.Error())
	}
}
