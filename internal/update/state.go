package update

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/yorch/ccshelf/internal/cache"
)

// StateName is the state file in the cache directory.
const StateName = "update-state.json"

// maxStateBytes bounds the state file; a larger one is ignored.
const maxStateBytes = 4 << 10

// State is everything the automatic check remembers. It holds times and a
// version number, never an identifier of the user or the machine.
type State struct {
	// LastCheck is when the release metadata was last requested (successful
	// or not, so a failing network is retried once per interval, not on every
	// command).
	LastCheck time.Time `json:"last_check,omitzero"`
	// Latest is the newest stable version seen, without a leading "v".
	Latest string `json:"latest,omitempty"`
	// NotifiedAt is when the "update available" line was last shown.
	NotifiedAt time.Time `json:"notified_at,omitzero"`
}

// LoadState reads the state file in dir. A missing, unreadable, corrupt,
// unknown-keyed or implausible file (a version that does not parse, a time in
// the future) yields the zero State: state is a cache, never a reason to fail.
func LoadState(dir string, now time.Time) State {
	b, err := cache.ReadState(dir, StateName, maxStateBytes)
	if err != nil {
		return State{}
	}
	var s State
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return State{}
	}
	if dec.More() {
		return State{}
	}
	if s.Latest != "" {
		if _, err := ParseVersion(s.Latest); err != nil {
			return State{}
		}
	}
	horizon := now.Add(24 * time.Hour)
	if s.LastCheck.After(horizon) || s.NotifiedAt.After(horizon) {
		return State{}
	}
	return s
}

// SaveState writes the state file atomically (mode 0600, no symlink
// followed).
func SaveState(dir string, s State) error {
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encoding the update state: %w", err)
	}
	if err := cache.WriteState(dir, StateName, append(b, '\n')); err != nil {
		return fmt.Errorf("writing the update state: %w", err)
	}
	return nil
}
