package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"unicode/utf8"

	"github.com/yorch/ccshelf/internal/envpolicy"
)

// MaxSize is the largest settings document [Validate] accepts.
const MaxSize = 1 << 20

// Validate strictly checks raw settings JSON against the closed schema. It
// is the guard that must run before every launch. All problems found are
// reported together, each naming its key. It applies the same rules as
// [Build] (model names, env values, server labels), and in addition rejects a
// UTF-8 byte order mark and invalid UTF-8. Being stricter than Claude Code is
// safe: Claude Code silently ignores a file it cannot parse.
func Validate(raw []byte) error {
	if len(raw) > MaxSize {
		return fmt.Errorf("settings: %d bytes exceeds the %d byte limit", len(raw), MaxSize)
	}
	if bytes.HasPrefix(raw, []byte("\xef\xbb\xbf")) {
		return errors.New("settings: starts with a UTF-8 byte order mark, which is not allowed")
	}
	if !utf8.Valid(raw) {
		return errors.New("settings: not valid UTF-8")
	}
	if err := checkNoDuplicates(raw); err != nil {
		return fmt.Errorf("settings: %w", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		if err == nil {
			err = errors.New("must be a JSON object, not null")
		}
		return fmt.Errorf("settings: not a JSON object: %w", err)
	}
	keys := make([]string, 0, len(top))
	for k := range top {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var errs []error
	for _, k := range keys {
		var err error
		switch k {
		case "enabledPlugins":
			err = validEnabledPlugins(top[k])
		case "skillOverrides":
			err = validSkillOverrides(top[k])
		case "disableClaudeAiConnectors":
			var b bool
			err = strict(top[k], &b, "a boolean")
		case "deniedMcpServers":
			err = validDenied(top[k])
		case "model":
			var s string
			err = strict(top[k], &s, "a string")
			if err == nil && s == "" {
				err = errors.New("must be a non-empty string")
			}
			if err == nil {
				err = checkModel(s)
			}
		case "env":
			err = validEnv(top[k])
		default:
			err = errors.New("key is not allowed (closed schema)")
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("settings key %q: %w", k, err))
		}
	}
	return errors.Join(errs...)
}

func strict(raw json.RawMessage, dst any, want string) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("must be %s, not null", want)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("must be %s", want)
	}
	return nil
}

func stringMap(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := strict(raw, &m, "an object"); err != nil {
		return nil, err
	}
	return m, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func validEnabledPlugins(raw json.RawMessage) error {
	m, err := stringMap(raw)
	if err != nil {
		return err
	}
	var errs []error
	for _, k := range sortedKeys(m) {
		if !pluginIDPattern.MatchString(k) {
			errs = append(errs, fmt.Errorf("%q is not a plugin id of the form name@marketplace", k))
			continue
		}
		var b bool
		if strict(m[k], &b, "a boolean") != nil {
			errs = append(errs, fmt.Errorf("%q: value must be true or false", k))
		}
	}
	return errors.Join(errs...)
}

func validSkillOverrides(raw json.RawMessage) error {
	m, err := stringMap(raw)
	if err != nil {
		return err
	}
	var errs []error
	for _, k := range sortedKeys(m) {
		if len(k) > maxNameLen || !skillPattern.MatchString(k) {
			errs = append(errs, fmt.Errorf("skill name %q must match %s", k, skillPattern))
			continue
		}
		var s string
		if strict(m[k], &s, "a string") != nil || !skillValues[s] {
			errs = append(errs, fmt.Errorf("%q: value must be one of on, name-only, user-invocable-only, off", k))
		}
	}
	return errors.Join(errs...)
}

func validDenied(raw json.RawMessage) error {
	var items []json.RawMessage
	if err := strict(raw, &items, "an array"); err != nil {
		return err
	}
	var errs []error
	for i, it := range items {
		m, err := stringMap(it)
		if err != nil || len(m) != 1 {
			errs = append(errs, fmt.Errorf("entry %d must be an object with exactly one key", i))
			continue
		}
		k := sortedKeys(m)[0]
		switch k {
		case "serverName", "serverUrl":
			var s string
			if strict(m[k], &s, "a string") != nil || !checkServerLabel(s) {
				errs = append(errs, fmt.Errorf("entry %d: %s must be a non-empty string of at most %d characters without surrounding whitespace or control characters", i, k, maxNameLen))
			}
		case "serverCommand":
			var cmd []string
			if strict(m[k], &cmd, "an array of strings") != nil || len(cmd) == 0 {
				errs = append(errs, fmt.Errorf("entry %d: serverCommand must be a non-empty array of strings", i))
			}
		default:
			errs = append(errs, fmt.Errorf("entry %d: key %q must be serverName, serverCommand or serverUrl", i, k))
		}
	}
	return errors.Join(errs...)
}

func validEnv(raw json.RawMessage) error {
	m, err := stringMap(raw)
	if err != nil {
		return err
	}
	var errs []error
	for _, k := range sortedKeys(m) {
		if r := envpolicy.DeniedReason(k); r != "" {
			errs = append(errs, fmt.Errorf("variable %q is not allowed: %s", k, r))
			continue
		}
		var s string
		if strict(m[k], &s, "a string") != nil {
			errs = append(errs, fmt.Errorf("variable %q: value must be a string", k))
		} else if err := checkEnvValue(k, s); err != nil {
			errs = append(errs, fmt.Errorf("variable %q: %w", k, err))
		}
	}
	return errors.Join(errs...)
}

// checkNoDuplicates walks the token stream and fails on a repeated key in
// any object, on invalid JSON and on trailing data.
func checkNoDuplicates(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := walk(dec, "$"); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing data after the JSON document")
	}
	return nil
}

func walk(dec *json.Decoder, path string) error {
	tok, err := dec.Token()
	if err != nil {
		return fmt.Errorf("invalid JSON at %s: %w", path, err)
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return fmt.Errorf("invalid JSON at %s: %w", path, err)
			}
			key, _ := kt.(string)
			if seen[key] {
				return fmt.Errorf("duplicate key %q at %s", key, path)
			}
			seen[key] = true
			if err := walk(dec, path+"."+key); err != nil {
				return err
			}
		}
	case '[':
		for i := 0; dec.More(); i++ {
			if err := walk(dec, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("invalid JSON at %s: %w", path, err)
	}
	return nil
}
