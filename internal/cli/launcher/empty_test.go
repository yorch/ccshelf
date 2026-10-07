package launcher

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEmptyListsUseResultStream(t *testing.T) {
	for _, tc := range []struct {
		args []string
		kind string
		hint string
	}{
		{[]string{"ls"}, "profiles", "ccshelf new <name>"},
		{[]string{"account", "ls"}, "accounts", "accounts are optional"},
	} {
		h := newHarness(t)
		h.mustRun(tc.args...)
		if !strings.Contains(h.out.String(), "\nhint: ") || !strings.Contains(h.out.String(), tc.hint) || h.errb.Len() != 0 {
			t.Errorf("%v: out %q err %q", tc.args, h.out, h.errb)
		}
		h.mustRun(append([]string{"--json"}, tc.args...)...)
		var env struct {
			Kind string
			Data []json.RawMessage
		}
		if err := json.Unmarshal(h.out.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Kind != tc.kind || env.Data == nil || len(env.Data) != 0 || h.errb.Len() != 0 || strings.Contains(h.out.String(), "hint:") {
			t.Errorf("%v: JSON empty collection changed: %s%s", tc.args, h.out, h.errb)
		}
	}
}
