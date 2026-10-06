//go:build !windows

package policy

import "errors"

// readRegistry is a stub: the registry only exists on Windows. Detect
// reports the source as Unknown.
func readRegistry(Hive) (string, error) {
	return "", errors.New("the Windows registry is not available on this platform")
}
