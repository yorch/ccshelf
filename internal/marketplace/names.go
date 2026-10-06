package marketplace

import (
	"fmt"
	"strings"
)

var reservedPrefixes = []string{"claude-", "anthropic-", "cc-plugin-"}

// ReservedNameCheck reports whether a plugin name collides with a reserved
// name rule of "claude plugin validate". The names starting with "claude-",
// "anthropic-" or "cc-plugin-" are errors (isError true); a name containing
// the whole word "claude" is a warning (isError false). reason is empty when
// the name is fine. Matching is case-insensitive.
func ReservedNameCheck(name string) (reason string, isError bool) {
	lower := strings.ToLower(name)
	for _, p := range reservedPrefixes {
		if strings.HasPrefix(lower, p) {
			return fmt.Sprintf("name starts with the reserved prefix %q", p), true
		}
	}
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	for _, w := range words {
		if w == "claude" {
			return `name contains the word "claude", which is reserved for Anthropic`, false
		}
	}
	return "", false
}

// ReservedNameReason returns why name is reserved, or "" when it is not. Use
// ReservedNameCheck to tell errors from warnings.
func ReservedNameReason(name string) string {
	r, _ := ReservedNameCheck(name)
	return r
}
