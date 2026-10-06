//go:build windows

package policy

import (
	"errors"
	"fmt"
	"io/fs"

	"golang.org/x/sys/windows/registry"
)

// readRegistry reads HKLM or HKCU SOFTWARE\Policies\ClaudeCode value
// Settings (REG_SZ or REG_EXPAND_SZ), read-only, without expanding
// environment variables.
func readRegistry(h Hive) (string, error) {
	root := registry.LOCAL_MACHINE
	if h == HKCU {
		root = registry.CURRENT_USER
	}
	k, err := registry.OpenKey(root, regKey, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", fs.ErrNotExist
		}
		return "", fmt.Errorf("open registry key: %w", err)
	}
	defer k.Close()
	v, typ, err := k.GetStringValue(regValue)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", fs.ErrNotExist
		}
		if errors.Is(err, registry.ErrUnexpectedType) {
			return "", fmt.Errorf("value %s is not a string (type %d)", regValue, typ)
		}
		return "", fmt.Errorf("read registry value: %w", err)
	}
	return v, nil
}
