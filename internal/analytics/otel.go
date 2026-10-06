package analytics

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// Limits of ParseOTelJSONL.
const (
	maxLineSize  = 1 << 20
	maxInputSize = 256 << 20

	// Event timestamps outside these Unix seconds (years 2000 to 2100) are
	// not believable and are treated as missing.
	minUnixSeconds = 946684800
	maxUnixSeconds = 4102444800
)

// ErrInputTooLarge is returned when the OTel export is longer than the 256 MiB
// cap. Reading less than the whole file would silently undercount usage, so
// an oversized input is an error, never a truncation.
var ErrInputTooLarge = errors.New("the OTel export is larger than 256 MiB; split it or export a shorter window")

// OTelOptions narrows ParseOTelJSONLWindow.
type OTelOptions struct {
	// From (inclusive) and To (exclusive) bound the events counted. A zero
	// bound is open. Events without a usable timestamp are always counted,
	// because they cannot be shown to lie outside the window.
	From, To time.Time
}

// capReader fails with ErrInputTooLarge instead of silently stopping at max.
type capReader struct {
	r   io.Reader
	n   int64
	max int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if room := c.max - c.n + 1; int64(len(p)) > room {
		p = p[:room]
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n > c.max {
		return n, ErrInputTooLarge
	}
	return n, err
}

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
// Timestamps outside the years 2000 to 2100 are treated as missing. Lines that
// are not JSON objects or not events of these kinds are ignored;
// lines that are not JSON objects at all are counted in Usage.Skipped. It is
// an error when the input has lines but none is a JSON object.
func ParseOTelJSONL(r io.Reader) (*Usage, error) {
	return ParseOTelJSONLWindow(r, OTelOptions{})
}

// ParseOTelJSONLWindow is ParseOTelJSONL restricted to the events inside the
// window of opt. When a bound is set, Usage.From and Usage.To echo it (To is
// the last day included); otherwise they come from the events. Input larger
// than 256 MiB is an error (ErrInputTooLarge).
func ParseOTelJSONLWindow(r io.Reader, opt OTelOptions) (*Usage, error) {
	return parseOTel(r, opt, maxInputSize)
}

func parseOTel(r io.Reader, opt OTelOptions, limit int64) (*Usage, error) {
	u := newUsage("otel")
	sc := bufio.NewScanner(&capReader{r: r, max: limit})
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
		t, dated := eventTime(ev)
		if dated && ((!opt.From.IsZero() && t.Before(opt.From)) || (!opt.To.IsZero() && !t.Before(opt.To))) {
			continue
		}
		if dated {
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
			return nil, fmt.Errorf("reading OTel events: a line is longer than %d bytes: %w", maxLineSize, err)
		}
		if errors.Is(err, ErrInputTooLarge) {
			return nil, fmt.Errorf("reading OTel events: %w", err)
		}
		return nil, fmt.Errorf("reading OTel events: %w", err)
	}
	if lines > 0 && objects == 0 {
		return nil, errors.New("reading OTel events: no line is a JSON object (expected JSON lines)")
	}
	if !first.IsZero() {
		u.From, u.To = first.UTC().Format(dateLayout), last.UTC().Format(dateLayout)
	}
	if !opt.From.IsZero() {
		u.From = opt.From.UTC().Format(dateLayout)
	}
	if !opt.To.IsZero() {
		u.To = opt.To.Add(-time.Nanosecond).UTC().Format(dateLayout)
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
		return t, plausible(t.Unix())
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || !(n > 0) {
		return time.Time{}, false
	}
	// Scale to seconds before converting, so a huge value cannot overflow.
	var sec float64
	switch {
	case n > 1e17: // nanoseconds
		sec = n / 1e9
	case n > 1e14: // microseconds
		sec = n / 1e6
	case n > 1e11: // milliseconds
		sec = n / 1e3
	default:
		sec = n
	}
	if !(sec >= minUnixSeconds && sec <= maxUnixSeconds) {
		return time.Time{}, false
	}
	whole := math.Floor(sec)
	return time.Unix(int64(whole), int64((sec-whole)*1e9)), true
}

func plausible(unix int64) bool { return unix >= minUnixSeconds && unix <= maxUnixSeconds }
