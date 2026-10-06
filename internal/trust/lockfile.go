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

	"github.com/ccshelf/ccshelf/internal/profile"
	"github.com/ccshelf/ccshelf/internal/version"
)

// LockVersion is the lockfile format version.
const LockVersion = 1

const (
	maxEntries = 10000
	maxItems   = 5000
)

// Errors returned by the store.
var (
	// ErrHashMismatch is returned by Accept when the expected closure hash is
	// not the hash of the closure being accepted.
	ErrHashMismatch = errors.New("closure hash does not match")
	// ErrInconsistentClosure is returned when a Closure's Hash is not the
	// hash of its Items.
	ErrInconsistentClosure = errors.New("closure hash does not match its items")
	// ErrNotFound is returned by Revoke when nothing matched.
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
	// Items is the accepted closure, kept to explain later changes.
	Items []profile.ClosureItem
	// AcceptedAt is when the user accepted it (UTC).
	AcceptedAt time.Time
	// ToolVersion is the ccshelf version that recorded the entry.
	ToolVersion string
}

type itemJSON struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Risky  bool   `json:"risky,omitempty"`
}

type entryJSON struct {
	Profile     string     `json:"profile"`
	Source      string     `json:"source"`
	Ref         string     `json:"ref,omitempty"`
	Commit      string     `json:"commit,omitempty"`
	ClosureHash string     `json:"closureHash"`
	Items       []itemJSON `json:"items"`
	AcceptedAt  time.Time  `json:"acceptedAt"`
	ToolVersion string     `json:"toolVersion,omitempty"`
}

// MarshalJSON writes the entry with stable lower-case keys.
func (e Entry) MarshalJSON() ([]byte, error) {
	j := entryJSON{Profile: e.Profile, Source: e.Source, Ref: e.Ref, Commit: e.Commit, ClosureHash: e.ClosureHash,
		Items: make([]itemJSON, 0, len(e.Items)), AcceptedAt: e.AcceptedAt, ToolVersion: e.ToolVersion}
	for _, it := range e.Items {
		j.Items = append(j.Items, itemJSON{Kind: it.Kind, Name: it.Name, Digest: it.Digest, Risky: it.Risky})
	}
	return json.Marshal(j)
}

// UnmarshalJSON reads an entry; unknown keys are errors.
func (e *Entry) UnmarshalJSON(b []byte) error {
	var j entryJSON
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&j); err != nil {
		return err
	}
	*e = Entry{Profile: j.Profile, Source: j.Source, Ref: j.Ref, Commit: j.Commit, ClosureHash: j.ClosureHash,
		AcceptedAt: j.AcceptedAt, ToolVersion: j.ToolVersion}
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
	}
	return nil
}

// Store is the trust lockfile. Methods are safe for concurrent use by
// goroutines; Accept and Revoke re-read the file before writing so two
// processes rarely lose each other's update, but there is no cross-process
// lock.
type Store struct {
	path string
	now  func() time.Time

	mu      sync.Mutex
	entries []Entry
}

// Open reads the lockfile at path (see config.LockfilePath). A missing file is
// an empty store. The file is never followed through a symlink, is limited in
// size and has a closed schema; an unreadable or inconsistent file is an
// error, not an empty store.
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
		return fmt.Errorf("trust lockfile %s has version %d; this ccshelf understands version %d", s.path, lf.Version, LockVersion)
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

// Accept records the closure of r as trusted. When expectedHash is not empty
// it must equal the closure hash of r (the scripted "--accept <hash>" form
// that names exactly what is accepted). Callers must never pass a value that
// came from "--yes": that flag never accepts trust. A closure with only
// personal sources needs no entry and Accept does nothing.
func (s *Store) Accept(r *profile.Resolved, expectedHash string) error {
	if r == nil {
		return errors.New("nothing to accept")
	}
	hash, err := closureHash(r)
	if err != nil {
		return err
	}
	if expectedHash != "" && expectedHash != hash {
		return fmt.Errorf("%w: expected %s, the profile now resolves to %s", ErrHashMismatch, expectedHash, hash)
	}
	id := identify(r)
	if !id.needsEntry {
		return nil
	}
	if len(r.Closure.Items) > maxItems {
		return errors.New("closure has too many items")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reload(); err != nil {
		return err
	}
	e := Entry{
		Profile: r.Name, Source: id.source, Ref: id.ref, Commit: id.commit,
		ClosureHash: hash, Items: append([]profile.ClosureItem(nil), r.Closure.Items...),
		AcceptedAt: s.now(), ToolVersion: version.Version,
	}
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

// Revoke removes the trust records of a profile. An empty source removes the
// profile's records for every source. It returns ErrNotFound when nothing
// matched.
func (s *Store) Revoke(profileName, source string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
