package profile

import "testing"

func TestPluginIDMustStartWithAlnum(t *testing.T) {
	for id, ok := range map[string]bool{
		"sre-kit@acme": true, "a.b_c@m-1": true,
		"--x@y": false, "x@--y": false, "-x@y": false, ".x@y": false, "x@.y": false, "x": false, "@y": false,
	} {
		if got := pluginIDRe.MatchString(id); got != ok {
			t.Errorf("pluginIDRe(%q) = %v, want %v", id, got, ok)
		}
	}
}
