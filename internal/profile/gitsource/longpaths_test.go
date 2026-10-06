package gitsource

import (
	"strings"
	"testing"
)

// git for Windows ignores files below a path longer than MAX_PATH unless
// core.longpaths is on; a cached checkout under a long cache path then looks
// like it has no objects. The option must stay in the hardening set.
func TestHardeningEnablesLongPaths(t *testing.T) {
	s := &Source{}
	args := strings.Join(s.hardening(), " ")
	if !strings.Contains(args, "core.longpaths=true") {
		t.Errorf("hardening lacks core.longpaths=true: %s", args)
	}
}
