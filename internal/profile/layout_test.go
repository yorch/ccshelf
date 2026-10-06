package profile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDirSourceAtCustomLayout(t *testing.T) {
	isolate(t)
	reg := "[servers.figma]\ncommand = \"npx\"\n"
	root := mk(t, map[string]string{
		"teams/a.toml":      "name = \"a\"\n[mcp]\nservers = [\"figma\"]\n",
		"config/mcp.toml":   reg,
		"mcp/registry.toml": "[servers.figma]\ncommand = \"decoy\"\n",
	})
	s := DirSourceAt(KindOrg, root, Layout{Profiles: "teams", Registry: "config/mcp.toml"})
	if s.Root() == "" {
		t.Fatal("source refused")
	}
	if got := registryPathOf(s); got != "config/mcp.toml" {
		t.Errorf("registryPathOf = %q", got)
	}
	names, err := s.Names()
	if err != nil || len(names) != 1 || names[0] != "a" {
		t.Fatalf("Names = %v, %v", names, err)
	}
	r, err := Resolve("a", []Source{s}, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.MCP["figma"].Command != "npx" {
		t.Errorf("the registry at the configured path was not used: %+v", r.MCP["figma"])
	}
}

func TestDirSourceAtDefaultsAndUnknownServer(t *testing.T) {
	isolate(t)
	root := mk(t, map[string]string{
		"profiles/a.toml":   "name = \"a\"\n[mcp]\nservers = [\"figma\"]\n",
		"mcp/registry.toml": "[servers.other]\ncommand = \"x\"\n",
	})
	s := DirSourceAt(KindOrg, root, Layout{})
	if got := registryPathOf(s); got != DefaultRegistryPath {
		t.Errorf("default registry = %q", got)
	}
	_, err := Resolve("a", []Source{s}, ResolveOptions{})
	if !errors.Is(err, ErrUnknownMCPServer) {
		t.Fatalf("err = %v", err)
	}
	mustErrContain(t, err, DefaultRegistryPath)
}

func TestDirSourceAtRejectsBadLayout(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	for _, l := range []Layout{
		{Profiles: "../x"},
		{Profiles: "/abs"},
		{Profiles: "a/../b"},
		{Profiles: ".hidden"},
		{Profiles: "a\\b"},
		{Registry: "../reg.toml"},
		{Registry: "mcp/.reg.toml"},
		{Registry: "c:/reg.toml"},
	} {
		s := DirSourceAt(KindOrg, root, l)
		if s.Root() != "" {
			t.Errorf("%+v: the source was accepted", l)
		}
		if _, err := s.Names(); !errors.Is(err, ErrPath) {
			t.Errorf("%+v: Names err = %v", l, err)
		}
		if _, err := s.Open("a"); !errors.Is(err, ErrPath) {
			t.Errorf("%+v: Open err = %v", l, err)
		}
	}
	if s := DirSourceAt(KindOrg, string(filepath.Separator), Layout{}); s.Root() != "" {
		t.Error("the file system root was accepted")
	}
}

func TestCustomRegistrySymlinkRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	isolate(t)
	outside := mk(t, map[string]string{"r.toml": "[servers.figma]\ncommand = \"x\"\n"})
	root := mk(t, map[string]string{"profiles/a.toml": "name = \"a\"\n[mcp]\nservers = [\"figma\"]\n"})
	if err := os.MkdirAll(filepath.Join(root, "cfg"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "r.toml"), filepath.Join(root, "cfg", "reg.toml")); err != nil {
		t.Skip(err)
	}
	s := DirSourceAt(KindOrg, root, Layout{Registry: "cfg/reg.toml"})
	if _, err := Resolve("a", []Source{s}, ResolveOptions{}); !errors.Is(err, ErrPath) {
		t.Errorf("a symlinked registry must be refused: %v", err)
	}
}
