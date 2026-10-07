package orgcmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yorch/ccshelf/internal/ui"
)

func TestDiscoveryEmptyHints(t *testing.T) {
	var b bytes.Buffer
	if err := writeSearchText(&b, ui.Mode{}, searchJSON{Query: "unlikely words"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "no plugin matches") || !strings.Contains(b.String(), "hint: try fewer or broader words") {
		t.Errorf("search: %q", b.String())
	}
	b.Reset()
	if err := writeRecommendText(&b, ui.Mode{}, recommendJSON{Dir: "project\nhint: forged\x1b"}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(b.String(), "\n") != 2 || strings.Contains(b.String(), "\x1b") || !strings.Contains(b.String(), "ccshelf search <query>") {
		t.Errorf("recommend: %q", b.String())
	}
	if err := writeSearchText(failingEmptyWriter{}, ui.Mode{}, searchJSON{}); err == nil {
		t.Error("search write error ignored")
	}
}

type failingEmptyWriter struct{}

func (failingEmptyWriter) Write([]byte) (int, error) { return 0, bytes.ErrTooLarge }
