package ui

import (
	"os"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestComputeMode(t *testing.T) {
	tests := []struct {
		name         string
		env          map[string]string
		in, out, vt  bool
		flags        ModeFlags
		wantInter    bool
		wantColor    bool
		wantPlain    bool
		wantJSONFlag bool
	}{
		{name: "terminal", in: true, out: true, vt: true, wantInter: true, wantColor: true},
		{name: "stdin not tty", out: true, vt: true, wantColor: true},
		{name: "stdout not tty", in: true, vt: true},
		{name: "no-interactive", in: true, out: true, vt: true, flags: ModeFlags{NoInteractive: true}, wantColor: true},
		{name: "config never", in: true, out: true, vt: true, flags: ModeFlags{ConfigInteractive: "never"}, wantColor: true},
		{name: "CI set", env: map[string]string{"CI": "true"}, in: true, out: true, vt: true, wantColor: true},
		{name: "CI one", env: map[string]string{"CI": "1"}, in: true, out: true, vt: true, wantColor: true},
		{name: "CI false", env: map[string]string{"CI": "false"}, in: true, out: true, vt: true, wantInter: true, wantColor: true},
		{name: "CI zero", env: map[string]string{"CI": "0"}, in: true, out: true, vt: true, wantInter: true, wantColor: true},
		{name: "CI empty", env: map[string]string{"CI": ""}, in: true, out: true, vt: true, wantInter: true, wantColor: true},
		{name: "dumb", env: map[string]string{"TERM": "dumb"}, in: true, out: true, vt: true, wantPlain: true},
		{name: "NO_COLOR", env: map[string]string{"NO_COLOR": "1"}, in: true, out: true, vt: true, wantInter: true},
		{name: "NO_COLOR any value", env: map[string]string{"NO_COLOR": "false"}, in: true, out: true, vt: true, wantInter: true},
		{name: "no-color flag", in: true, out: true, vt: true, flags: ModeFlags{NoColor: true}, wantInter: true},
		{name: "plain", in: true, out: true, vt: true, flags: ModeFlags{Plain: true}, wantInter: true, wantColor: true, wantPlain: true},
		{name: "json", in: true, out: true, vt: true, flags: ModeFlags{JSON: true}, wantJSONFlag: true},
		{name: "config always on pipe", flags: ModeFlags{ConfigColor: "always"}, wantColor: true},
		{name: "config always loses to NO_COLOR", env: map[string]string{"NO_COLOR": "1"}, flags: ModeFlags{ConfigColor: "always"}},
		{name: "config never", in: true, out: true, vt: true, flags: ModeFlags{ConfigColor: "never"}, wantInter: true},
		{name: "windows console without VT", in: true, out: true, vt: false, wantPlain: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := computeMode(envOf(tt.env), tt.in, tt.out, tt.vt, tt.flags)
			if m.Interactive != tt.wantInter || m.Color != tt.wantColor || m.Plain != tt.wantPlain || m.JSON != tt.wantJSONFlag {
				t.Errorf("got %+v, want interactive=%v color=%v plain=%v json=%v", m, tt.wantInter, tt.wantColor, tt.wantPlain, tt.wantJSONFlag)
			}
		})
	}
}

func TestDetectModeWithHooks(t *testing.T) {
	f1, err := os.CreateTemp(t.TempDir(), "in")
	if err != nil {
		t.Fatal(err)
	}
	defer f1.Close()
	f2, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f2.Close()

	oldT, oldW := isTerminal, terminalWidth
	t.Cleanup(func() { isTerminal, terminalWidth = oldT, oldW })

	// Real files are not terminals.
	if m := DetectMode(envOf(nil), f1, f2, ModeFlags{}); m.Interactive || m.Color || m.Width != 0 {
		t.Errorf("regular files: %+v", m)
	}
	if m := DetectMode(nil, nil, nil, ModeFlags{}); m.Interactive {
		t.Errorf("nil files: %+v", m)
	}

	isTerminal = func(uintptr) bool { return true }
	terminalWidth = func(uintptr) int { return 100 }
	m := DetectMode(envOf(nil), f1, f2, ModeFlags{})
	if !m.Interactive || !m.Color || m.Width != 100 {
		t.Errorf("fake terminal: %+v", m)
	}
	terminalWidth = func(uintptr) int { return 0 }
	m = DetectMode(envOf(map[string]string{"COLUMNS": "72"}), f1, f2, ModeFlags{})
	if m.Width != 72 {
		t.Errorf("COLUMNS fallback: %+v", m)
	}
	m = DetectMode(envOf(nil), f1, f2, ModeFlags{JSON: true})
	if m.Width != 0 || m.Interactive {
		t.Errorf("json: %+v", m)
	}
}

func TestStdStreams(t *testing.T) {
	s := StdStreams()
	if s.In != os.Stdin || s.Out != os.Stdout || s.Err != os.Stderr {
		t.Error("StdStreams does not bind the standard streams")
	}
}
