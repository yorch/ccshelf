package update

import "testing"

func TestParseVersion(t *testing.T) {
	good := map[string]string{
		"1.2.3":            "1.2.3",
		"v1.2.3":           "1.2.3",
		"0.0.1":            "0.0.1",
		"10.20.30":         "10.20.30",
		"1.2.3-rc.1":       "1.2.3-rc.1",
		"v1.2.3-alpha":     "1.2.3-alpha",
		"1.2.3-0.3.7":      "1.2.3-0.3.7",
		"1.2.3-x.7.z.92":   "1.2.3-x.7.z.92",
		"1.2.3+build.5":    "1.2.3",
		"1.2.3-rc.1+b.7":   "1.2.3-rc.1",
		"1.0.0-alpha-beta": "1.0.0-alpha-beta",
	}
	for in, want := range good {
		v, err := ParseVersion(in)
		if err != nil {
			t.Errorf("ParseVersion(%q): %v", in, err)
			continue
		}
		if v.String() != want {
			t.Errorf("ParseVersion(%q).String() = %q, want %q", in, v.String(), want)
		}
		if v.Tag() != "v"+want {
			t.Errorf("Tag() = %q", v.Tag())
		}
	}
	bad := []string{
		"", "v", "1", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.03", "a.b.c", "1.2.3-", "1.2.3-01", "1.2.3-rc..1",
		"1.2.3-rc_1", "v v1.2.3", " 1.2.3", "1.2.3 ", "vv1.2.3", "1.2.3+", "99999999999.0.0", "dev", "1.2.3\n",
		"1.2.3-" + string(make([]byte, 80)),
	}
	for _, in := range bad {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) succeeded", in)
		}
	}
}

func TestCompare(t *testing.T) {
	// Ordered from lowest to highest (semver.org section 11).
	order := []string{
		"0.9.9", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2",
		"1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0", "10.0.0",
	}
	for i, a := range order {
		for j, b := range order {
			va, _ := ParseVersion(a)
			vb, _ := ParseVersion(b)
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got := va.Compare(vb); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", a, b, got, want)
			}
		}
	}
	a, _ := ParseVersion("1.2.3+one")
	b, _ := ParseVersion("v1.2.3+two")
	if a.Compare(b) != 0 {
		t.Error("build metadata must not affect precedence")
	}
	if !(Version{Pre: []string{"rc"}}).IsPrerelease() || (Version{}).IsPrerelease() {
		t.Error("IsPrerelease")
	}
}

func TestIsDevVersion(t *testing.T) {
	for in, want := range map[string]bool{
		"": true, "dev": true, "(devel)": true, "unknown": true,
		"0.1.0": false, "v0.1.0": false, "1.2.3-rc.1": false,
		"v0.0.0-20260101120000-abcdef123456":     true,
		"0.2.1-0.20260101120000-abcdef123456":    true,
		"v0.2.1-0.20260101120000-abcdef123456+x": true,
		"0.1.0+dirty":                            true,
		"v1.0.0+incompatible":                    true,
		"banana":                                 true,
		"1.2":                                    true,
	} {
		if got := IsDevVersion(in); got != want {
			t.Errorf("IsDevVersion(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAutoEligible(t *testing.T) {
	for _, tc := range []struct {
		cur, latest string
		want        bool
	}{
		{"1.2.3", "1.2.4", true},
		{"1.2.3", "1.3.0", true},
		{"1.2.3", "2.0.0", false}, // new major: offered, never installed
		{"1.2.3", "1.2.3", false},
		{"1.2.3", "1.2.2", false}, // never a downgrade
		{"1.2.3", "1.3.0-rc.1", false},
		{"0.1.0", "0.1.1", true},
		{"0.1.0", "0.2.0", false}, // 0.x: a minor bump is a breaking one
		{"0.1.5", "0.1.6", true},
		{"0.1.0", "1.0.0", false},
		{"0.1.0-rc.1", "0.1.0", true},
		{"1.0.0", "1.0.1-rc.1", false},
	} {
		c, _ := ParseVersion(tc.cur)
		l, _ := ParseVersion(tc.latest)
		if got := AutoEligible(c, l); got != tc.want {
			t.Errorf("AutoEligible(%s, %s) = %v, want %v", tc.cur, tc.latest, got, tc.want)
		}
	}
}
