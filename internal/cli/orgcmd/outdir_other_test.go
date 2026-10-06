//go:build !unix

package orgcmd

import "testing"

// checkModes: file modes are not meaningful on this platform.
func checkModes(t *testing.T, _ string) { t.Helper() }
