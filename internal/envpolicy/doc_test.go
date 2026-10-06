package envpolicy

import "testing"

func TestReasonText(t *testing.T) {
	if r := DeniedReason("ANTHROPIC_BASE_URL"); r == "" {
		t.Fatal("expected a reason")
	}
	if r := DeniedReason("FOO_REF"); r != "" {
		t.Fatalf("unexpected reason %q", r)
	}
}
