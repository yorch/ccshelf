package analytics

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Limits of ParseOTelJSONL.
const (
	maxLineSize  = 1 << 20
	maxInputSize = 256 << 20
)

// Event names (with or without the claude_code. prefix).
const (
	eventSkill     = "skill_activated"
	eventLoaded    = "plugin_loaded"
	eventInstalled = "plugin_installed"
)

var (
	eventNameKeys = []string{"event.name", "event_name", "eventName", "name", "body"}
	pluginKeys    = []string{"plugin.name", "plugin_name", "plugin.id", "plugin_id", "plugin"}
	marketKeys    = []string{"plugin.marketplace", "marketplace.name", "marketplace_name", "marketplace"}
	skillKeys     = []string{"skill.name", "skill_name", "skill"}
	timeKeys      = []string{"timestamp", "time", "event.timestamp", "observed_time", "timeUnixNano", "observedTimeUnixNano", "time_unix_nano"}
)

// ParseOTelJSONL reads one JSON object per line, an export of Claude Code
// OpenTelemetry log events, and counts claude_code.skill_activated
// (Invocations), plugin_loaded (Loads) and plugin_installed (Installs) per
// plugin.
//
// It is tolerant about the envelope: the event name may be under event.name,
// event_name, name or body; attributes may be top-level keys, an
// "attributes" object, or an OTLP list of {key, value:{stringValue}} pairs.
// The plugin comes from plugin.name / plugin_name / plugin.id (joined with a
// marketplace attribute when there is one); a skill_activated event without a
// plugin attribute uses the part before ":" of the skill name. Redacted or
// missing names ("<REDACTED>", "redacted", empty) increase Usage.Redacted.
// Lines that are not JSON objects or not events of these kinds are ignored;
// lines that are not JSON objects at all are counted in Usage.Skipped. It is
// an error when the input has lines but none is a JSON object.
func ParseOTelJSONL(r io.Reader) (*Usage, error) {
	u := newUsage("otel")
	sc := bufio.NewScanner(io.LimitReader(r, maxInputSize))
	sc.Buffer(make([]byte, 0, 64<<10), maxLineSize)
	var first, last time.Time
	lines, objects := 0, 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		lines++
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			u.Skipped++
			continue
		}
		objects++
		ev := flatten(obj)
		name := eventName(ev)
		if name == "" {
			continue
		}
		var c Counts
		switch name {
		case eventSkill:
			c.Invocations = 1
		case eventLoaded:
			c.Loads = 1
		case eventInstalled:
			c.Installs = 1
		default:
			continue
		}
		if t, ok := eventTime(ev); ok {
			if first.IsZero() || t.Before(first) {
				first = t
			}
			if t.After(last) {
				last = t
			}
		}
		key := pluginKey(ev, name == eventSkill)
		if key == "" {
			u.Redacted++
			continue
		}
		u.add(key, c)
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return nil, fmt.Errorf("reading OTel events: a line is longer than %d bytes", maxLineSize)
		}
		return nil, fmt.Errorf("reading OTel events: %w", err)
	}
	if lines > 0 && objects == 0 {
		return nil, errors.New("reading OTel events: no line is a JSON object (expected JSON lines)")
	}
	if !first.IsZero() {
		u.From, u.To = first.UTC().Format(dateLayout), last.UTC().Format(dateLayout)
	}
	return u, nil
}

// flatten merges an event's attributes into one string map. Top-level scalar
// keys are included; attributes win over them.
func flatten(obj map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range obj {
		if s, ok := scalar(v); ok {
			out[k] = s
		}
	}
	for _, key := range []string{"attributes", "attrs", "resource_attributes"} {
		switch a := obj[key].(type) {
		case map[string]any:
			for k, v := range a {
				if s, ok := scalar(v); ok {
					out[k] = s
				}
			}
		case []any:
			for _, item := range a {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				k, _ := m["key"].(string)
				if k == "" {
					continue
				}
				if s, ok := otlpValue(m["value"]); ok {
					out[k] = s
				}
			}
		}
	}
	return out
}

func otlpValue(v any) (string, bool) {
	if m, ok := v.(map[string]any); ok {
		for _, k := range []string{"stringValue", "intValue", "doubleValue", "boolValue"} {
			if x, ok := m[k]; ok {
				return scalar(x)
			}
		}
		return "", false
	}
	return scalar(v)
}

func scalar(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case bool:
		return strconv.FormatBool(x), true
	}
	return "", false
}

func pick(ev map[string]string, keys []string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(ev[k]); v != "" {
			return v
		}
	}
	return ""
}

func eventName(ev map[string]string) string {
	n := pick(ev, eventNameKeys)
	n = strings.TrimPrefix(n, "claude_code.")
	switch n {
	case eventSkill, eventLoaded, eventInstalled:
		return n
	}
	return ""
}

func isRedacted(s string) bool {
	t := strings.ToLower(strings.Trim(strings.TrimSpace(s), "<>[]*"))
	return t == "" || t == "redacted" || t == "unknown" || t == "third-party"
}

func pluginKey(ev map[string]string, skill bool) string {
	name := pick(ev, pluginKeys)
	if isRedacted(name) {
		name = ""
	}
	if name == "" && skill {
		if s := pick(ev, skillKeys); !isRedacted(s) {
			if i := strings.Index(s, ":"); i > 0 {
				name = s[:i]
			}
		}
	}
	if name == "" {
		return ""
	}
	if !strings.Contains(name, "@") {
		if m := pick(ev, marketKeys); !isRedacted(m) {
			name += "@" + m
		}
	}
	return name
}

func eventTime(ev map[string]string) (time.Time, bool) {
	s := pick(ev, timeKeys)
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	if n, err := strconv.ParseFloat(s, 64); err == nil && n > 0 {
		switch {
		case n > 1e17: // nanoseconds
			return time.Unix(0, int64(n)), true
		case n > 1e14: // microseconds
			return time.UnixMicro(int64(n)), true
		case n > 1e11: // milliseconds
			return time.UnixMilli(int64(n)), true
		default:
			return time.Unix(int64(n), 0), true
		}
	}
	return time.Time{}, false
}
