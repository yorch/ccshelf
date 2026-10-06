package e2e

import (
	"context"
	"sync"
	"testing"
)

func TestConcurrentRunsOfDifferentProfiles(t *testing.T) {
	s := newSandbox(t)
	s.writeProfile("alpha", "name = \"alpha\"\n[plugins]\ninclude = [\"design-kit@acme\"]\n")
	s.writeProfile("beta", "name = \"beta\"\n[plugins]\ninclude = [\"sre-kit@acme\"]\n[session]\neffort = \"low\"\n")
	// Keep each fake alive long enough that the runs overlap.
	s.Setenv("FAKE_CLAUDE_SLEEP", "400")

	const rounds = 4
	type out struct {
		profile string
		r       result
		err     error
	}
	var wg sync.WaitGroup
	results := make(chan out, 2*rounds)
	start := make(chan struct{})
	for i := 0; i < rounds; i++ {
		for _, p := range []string{"alpha", "beta"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				r, err := s.try(context.Background(), s.Work, "", "run", p)
				results <- out{p, r, err}
			}()
		}
	}
	close(start)
	wg.Wait()
	close(results)
	for o := range results {
		if o.err != nil || o.r.Code != 0 {
			t.Errorf("run %s: %v exit %d\n%s", o.profile, o.err, o.r.Code, o.r.Stderr)
		}
	}

	files := map[string]string{} // profile -> settings path
	for _, inv := range s.launches() {
		st := settingsOf(t, inv)
		env, _ := st["env"].(map[string]any)
		prof, _ := env["CCSHELF_PROFILE"].(string)
		path := argAfter(inv.Argv, "--settings")
		if prev, ok := files[prof]; ok && prev != path {
			t.Errorf("profile %s used two different settings files (%s, %s)", prof, prev, path)
		}
		files[prof] = path
		ep := enabledPlugins(t, st)
		switch prof {
		case "alpha":
			if !ep["design-kit@acme"] || ep["sre-kit@acme"] {
				t.Errorf("alpha settings = %v", ep)
			}
		case "beta":
			if ep["design-kit@acme"] || !ep["sre-kit@acme"] {
				t.Errorf("beta settings = %v", ep)
			}
			if argAfter(inv.Argv, "--effort") != "low" {
				t.Errorf("beta lost its effort: %v", inv.Argv)
			}
		default:
			t.Errorf("settings of an unknown profile %q", prof)
		}
	}
	if len(files) != 2 || files["alpha"] == files["beta"] {
		t.Errorf("settings files = %v, want two distinct", files)
	}
	if n := len(s.launches()); n != 2*rounds {
		t.Errorf("launches = %d, want %d", n, 2*rounds)
	}
}
