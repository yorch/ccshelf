package trust

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/yorch/ccshelf/internal/profile"
	"github.com/yorch/ccshelf/internal/version"
)

// LockVersion is the lockfile format version.
const LockVersion = 1

const (
	maxEntries      = 10000
	maxItems        = 5000
	maxControlsText = 64 << 10
)

// Errors returned by the store.
var (
	// Accept returns ErrHashMismatch when the expected closure hash is not
	// the hash of the closure that it accepts.
	ErrHashMismatch = errors.New("closure hash does not match")
	// ErrInconsistentClosure is returned when a Closure's Hash is not the
	// hash of its Items.
	ErrInconsistentClosure = errors.New("closure hash does not match its items")
	// Accept returns ErrHashRequired when the caller gives no closure hash.
	// The caller must name the hash that was shown for review.
	ErrHashRequired = errors.New("accepting trust needs the closure hash that was reviewed")
	// Revoke returns ErrNotFound when nothing matched.
	ErrNotFound = errors.New("no trust record found")
)

var hexHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Entry is one accepted closure, keyed by (Profile, Source).
type Entry struct {
	// Profile is the requested profile name.
	Profile string
	// Source identifies where the profile comes from without the commit:
	// "git:<url>" for git, "plugin:<id>", or the portable id (dir:org).
	Source string
	// Ref is the tag or SHA the source was pinned to ("" for sources without
	// one). With Commit it detects a tag that moved.
	Ref string
	// Commit is the commit (or plugin version) the ref resolved to.
	Commit string
	// ClosureHash is the accepted closure hash.
	ClosureHash string
	// Sources records the ref and commit of every shared source in the
	// profile's chain (most specific first), so a tag that moved in any of
	// them is noticed. Ref and Commit above repeat the first one.
	Sources []SourceRecord
	// Items is the accepted closure, kept to explain later changes.
	Items []profile.ClosureItem
	// Controls holds, per profile name in the chain, the canonical JSON of
	// its controls (profile.ControlsJSON) as it was accepted. The digest of
	// each text is the digest of the matching profile-controls item. It lets
	// a later change be explained field by field.
	Controls map[string]string
	// AcceptedAt is when the user accepted it (UTC).
	AcceptedAt time.Time
	// ToolVersion is the ccshelf version that recorded the entry.
	ToolVersion string
}

// SourceRecord is the identity and pin of one shared source in an entry.
type SourceRecord struct {
	// Source is the source without its commit ("git:<url>", "plugin:<id>",
	// "dir:org").
	Source string
	// Ref is the tag or version it was pinned to ("" when it has none).
	Ref string
	// Commit is what the ref resolved to when the entry was accepted.
	Commit string
}

type sourceJSON struct {
	Source string `json:"source"`
	Ref    string `json:"ref,omitempty"`
	Commit string `json:"commit,omitempty"`
}

type itemJSON struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Risky  bool   `json:"risky,omitempty"`
}

type entryJSON struct {
	Profile     string            `json:"profile"`
	Source      string            `json:"source"`
	Ref         string            `json:"ref,omitempty"`
	Commit      string            `json:"commit,omitempty"`
	ClosureHash string            `json:"closureHash"`
	Sources     []sourceJSON      `json:"sources,omitempty"`
	Items       []itemJSON        `json:"items"`
	Controls    map[string]string `json:"controls,omitempty"`
	AcceptedAt  time.Time         `json:"acceptedAt"`
	ToolVersion string            `json:"toolVersion,omitempty"`
}

// MarshalJSON writes the entry with stable lower-case keys.
func (e Entry) MarshalJSON() ([]byte, error) {
	j := entryJSON{
		Profile: e.Profile, Source: e.Source, Ref: e.Ref, Commit: e.Commit, ClosureHash: e.ClosureHash,
		Items: make([]itemJSON, 0, len(e.Items)), Controls: e.Controls, AcceptedAt: e.AcceptedAt, ToolVersion: e.ToolVersion,
	}
	for _, sr := range e.Sources {
		j.Sources = append(j.Sources, sourceJSON(sr))
	}
	for _, it := range e.Items {
		j.Items = append(j.Items, itemJSON{Kind: it.Kind, Name: it.Name, Digest: it.Digest, Risky: it.Risky})
	}
	return json.Marshal(j)
}

// UnmarshalJSON reads an entry. Unknown keys are errors.
func (e *Entry) UnmarshalJSON(b []byte) error {
	var j entryJSON
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&j); err != nil {
		return err
	}
	*e = Entry{
		Profile: j.Profile, Source: j.Source, Ref: j.Ref, Commit: j.Commit, ClosureHash: j.ClosureHash,
		Controls: j.Controls, AcceptedAt: j.AcceptedAt, ToolVersion: j.ToolVersion,
	}
	for _, sr := range j.Sources {
		e.Sources = append(e.Sources, SourceRecord(sr))
	}
	for _, it := range j.Items {
		e.Items = append(e.Items, profile.ClosureItem{Kind: it.Kind, Name: it.Name, Digest: it.Digest, Risky: it.Risky})
	}
	return nil
}

type lockFile struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// validate checks one entry read from disk: the stored hash must be the hash
// of the stored items, so a hand-edited or corrupted lockfile fails closed.
func (e *Entry) validate() error {
	switch {
	case e.Profile == "" || e.Source == "":
		return errors.New("entry without a profile or source")
	case !hexHash.MatchString(e.ClosureHash):
		return fmt.Errorf("entry %s/%s has an invalid closure hash", e.Profile, e.Source)
	case len(e.Items) > maxItems:
		return fmt.Errorf("entry %s/%s has too many items", e.Profile, e.Source)
	case profile.HashItems(e.Items) != e.ClosureHash:
		return fmt.Errorf("entry %s/%s: %w (the lockfile was edited or is corrupt)", e.Profile, e.Source, ErrInconsistentClosure)
	case len(e.Sources) > maxItems || len(e.Controls) > maxItems:
		return fmt.Errorf("entry %s/%s has too many sources or controls", e.Profile, e.Source)
	}
	digests := map[string]string{}
	for _, it := range e.Items {
		if it.Kind == profile.ItemProfileControls {
			digests[it.Name] = it.Digest
		}
	}
	for name, text := range e.Controls {
		d, ok := digests[name]
		switch {
		case len(text) > maxControlsText:
			return fmt.Errorf("entry %s/%s: the controls of %q are too large", e.Profile, e.Source, name)
		case !ok || profile.DigestBytes([]byte(text)) != d:
			return fmt.Errorf("entry %s/%s: the stored controls of %q: %w (the lockfile was edited or is corrupt)", e.Profile, e.Source, name, ErrInconsistentClosure)
		}
	}
	return nil
}

// Store is the trust lockfile. Methods are safe for concurrent use by
// goroutines. Accept and Revoke hold an exclusive operating system lock on
// "<lockfile>.lock" while they re-read the file, change it and replace it, so
// concurrent processes (and other Store values on the same file) never lose
// each other's update.
type Store struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	entries []Entry
}

// Open reads the lockfile at path (see config.LockfilePath). A missing file is
// an empty store. Open never follows the file through a symlink. The file is
// limited in size and has a closed schema. An unreadable or inconsistent file
// is an error, not an empty store.
func Open(path string) (*Store, error) {
	s := &Store{path: path, now: func() time.Time { return time.Now().UTC() }}
	if err := s.reload(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) reload() error {
	b, err := readStateFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		s.entries = nil
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading trust lockfile: %w", err)
	}
	var lf lockFile
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&lf); err != nil {
		return fmt.Errorf("parsing trust lockfile %s: %w", s.path, err)
	}
	if lf.Version != LockVersion {
		return fmt.Errorf("trust lockfile %s has version %d. This ccshelf understands version %d", s.path, lf.Version, LockVersion)
	}
	if len(lf.Entries) > maxEntries {
		return fmt.Errorf("trust lockfile %s has too many entries", s.path)
	}
	seen := map[[2]string]bool{}
	for i := range lf.Entries {
		if err := lf.Entries[i].validate(); err != nil {
			return fmt.Errorf("trust lockfile %s: %w", s.path, err)
		}
		k := [2]string{lf.Entries[i].Profile, lf.Entries[i].Source}
		if seen[k] {
			return fmt.Errorf("trust lockfile %s: duplicate entry for %s/%s", s.path, k[0], k[1])
		}
		seen[k] = true
	}
	s.entries = lf.Entries
	return nil
}

func (s *Store) save() error {
	sortEntries(s.entries)
	b, err := json.MarshalIndent(lockFile{Version: LockVersion, Entries: nonNil(s.entries)}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding trust lockfile: %w", err)
	}
	if err := writeStateFile(s.path, append(b, '\n')); err != nil {
		return fmt.Errorf("writing trust lockfile: %w", err)
	}
	return nil
}

func nonNil(e []Entry) []Entry {
	if e == nil {
		return []Entry{}
	}
	return e
}

func sortEntries(e []Entry) {
	sort.Slice(e, func(i, j int) bool {
		if e[i].Profile != e[j].Profile {
			return e[i].Profile < e[j].Profile
		}
		return e[i].Source < e[j].Source
	})
}

// Path returns the lockfile path.
func (s *Store) Path() string { return s.path }

// List returns the recorded entries sorted by profile and source.
func (s *Store) List() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Entry(nil), s.entries...)
	sortEntries(out)
	return out
}

func (s *Store) find(profileName, source string) *Entry {
	for i := range s.entries {
		if s.entries[i].Profile == profileName && s.entries[i].Source == source {
			return &s.entries[i]
		}
	}
	return nil
}

// Accept records the closure of r as trusted. expectedHash is required: it
// must be the closure hash that was shown for review (Verdict.Hash, or the
// value the user passed to "ccshelf trust --accept"), so that what was
// reviewed is what is stored. An empty hash is ErrHashRequired and a hash that
// is not the current closure hash is ErrHashMismatch. Callers must never pass
// a value that came from "--yes": that flag never accepts trust. A closure
// with only personal sources needs no entry and Accept then does nothing.
func (s *Store) Accept(r *profile.Resolved, expectedHash string) error {
	if r == nil {
		return errors.New("nothing to accept")
	}
	if expectedHash == "" {
		return ErrHashRequired
	}
	hash, err := closureHash(r)
	if err != nil {
		return err
	}
	if expectedHash != hash {
		return fmt.Errorf("%w: expected %s, the profile now resolves to %s", ErrHashMismatch, expectedHash, hash)
	}
	id := identify(r)
	if !id.needsEntry {
		return nil
	}
	if len(r.Closure.Items) > maxItems {
		return errors.New("closure has too many items")
	}
	controls, err := controlsOf(r)
	if err != nil {
		return err
	}
	e := Entry{
		Profile: r.Name, Source: id.source, Ref: id.ref, Commit: id.commit, Sources: id.sources,
		ClosureHash: hash, Items: append([]profile.ClosureItem(nil), r.Closure.Items...), Controls: controls,
		ToolVersion: version.Version,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := lockState(s.path)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.reload(); err != nil {
		return err
	}
	e.AcceptedAt = s.now()
	if old := s.find(e.Profile, e.Source); old != nil {
		*old = e
	} else {
		if len(s.entries) >= maxEntries {
			return errors.New("the trust lockfile is full")
		}
		s.entries = append(s.entries, e)
	}
	return s.save()
}

// Rekey updates the refs of the recorded sources of a closure that is already
// trusted, and reports whether it wrote anything. It exists for a source that
// changes how it is pinned without changing what it resolves to: a tag source
// that becomes a branch source at the same commit (D-54). The verdict is
// Trusted by content, but the lockfile would keep the old ref, and a later
// move of the new ref would then be a generic "Changed" instead of a moved
// ref.
//
// Rekey changes nothing unless every one of these holds: an entry exists for
// the profile and the key source, its closure hash equals the hash of r, and
// the sources of the chain match the recorded ones one by one with the same
// locator (so the same URL) and the same commit. Only the refs may differ.
// Anything else is left for the trust check, so no trust can be created or
// moved to a different URL, commit or content by this call.
func (s *Store) Rekey(r *profile.Resolved) (bool, error) {
	if r == nil {
		return false, nil
	}
	hash, err := closureHash(r)
	if err != nil {
		return false, err
	}
	id := identify(r)
	if !id.needsEntry || id.project {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := lockState(s.path)
	if err != nil {
		return false, err
	}
	defer unlock()
	if err := s.reload(); err != nil {
		return false, err
	}
	old := s.find(r.Name, id.source)
	if old == nil || old.ClosureHash != hash {
		return false, nil
	}
	recorded := old.Sources
	if len(recorded) == 0 {
		recorded = []SourceRecord{{Source: old.Source, Ref: old.Ref, Commit: old.Commit}}
	}
	if len(recorded) != len(id.sources) {
		return false, nil
	}
	differs := false
	for i, cur := range id.sources {
		prev := recorded[i]
		if prev.Source != cur.Source || prev.Commit != cur.Commit || cur.Commit == "" {
			return false, nil
		}
		if prev.Ref != cur.Ref {
			differs = true
		}
	}
	if !differs {
		return false, nil
	}
	old.Ref, old.Commit = id.ref, id.commit
	old.Sources = append([]SourceRecord(nil), id.sources...)
	return true, s.save()
}

// controlsOf returns the canonical controls text of every profile in r's
// chain, checked against the closure: each text must hash to the digest of its
// profile-controls item.
func controlsOf(r *profile.Resolved) (map[string]string, error) {
	digests := map[string]string{}
	for _, it := range r.Closure.Items {
		if it.Kind == profile.ItemProfileControls {
			digests[it.Name] = it.Digest
		}
	}
	out := map[string]string{}
	for _, f := range r.Chain {
		if f == nil || f.Manifest == nil {
			continue
		}
		b, err := profile.ControlsJSON(f.Manifest)
		if err != nil {
			return nil, err
		}
		if d, ok := digests[f.Name]; !ok || profile.DigestBytes(b) != d {
			return nil, fmt.Errorf("the controls of profile %q: %w", f.Name, ErrInconsistentClosure)
		}
		out[f.Name] = string(b)
	}
	return out, nil
}

// Revoke removes the trust records of a profile. An empty source removes the
// profile's records for every source. It returns ErrNotFound when nothing
// matched.
func (s *Store) Revoke(profileName, source string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := lockState(s.path)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.reload(); err != nil {
		return err
	}
	kept := s.entries[:0:0]
	for _, e := range s.entries {
		if e.Profile == profileName && (source == "" || e.Source == source) {
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) == len(s.entries) {
		return fmt.Errorf("%w for profile %q", ErrNotFound, profileName)
	}
	s.entries = kept
	return s.save()
}
