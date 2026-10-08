package sidecar

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/yorch/ccshelf/internal/catalog/safepath"
	"github.com/yorch/ccshelf/internal/marketplace"
	"github.com/yorch/ccshelf/internal/orgconfig"
)

// Dir is the sidecar directory below the repository root.
const Dir = "catalog/plugins"

// MaxFileSize caps one sidecar file.
const MaxFileSize = 256 << 10

// Status values.
const (
	StatusActive       = "active"
	StatusExperimental = "experimental"
	StatusDeprecated   = "deprecated"
)

// ValidStatus reports whether s is one of the three status values.
func ValidStatus(s string) bool {
	return s == StatusActive || s == StatusExperimental || s == StatusDeprecated
}

// Sidecar is the catalog metadata of one plugin.
type Sidecar struct {
	Owner        string   `toml:"owner" json:"owner,omitempty"`
	Status       string   `toml:"status" json:"status,omitempty"`
	WhenToUse    []string `toml:"when_to_use" json:"when_to_use,omitempty"`
	AvoidWhen    []string `toml:"avoid_when" json:"avoid_when,omitempty"`
	OverlapsWith []string `toml:"overlaps_with" json:"overlaps_with,omitempty"`
	SupersededBy string   `toml:"superseded_by" json:"superseded_by,omitempty"`
	// ReviewBy is the date as YYYY-MM-DD text; empty when absent. A TOML local
	// date is converted. Use ReviewDate to parse it.
	ReviewBy string `toml:"review_by" json:"review_by,omitempty"`
	Support  string `toml:"support" json:"support,omitempty"`
	Docs     string `toml:"docs" json:"docs,omitempty"`

	// File is the repository-relative file the data came from (the sidecar, or
	// the marketplace file in single-file mode).
	File string `toml:"-" json:"-"`
	// Line is the line of the first key, FieldLines the line of each key
	// (sidecar files only; zero in single-file mode).
	Line       int            `toml:"-" json:"-"`
	FieldLines map[string]int `toml:"-" json:"-"`
	// Present lists the fields that were set, even to an empty value.
	Present map[string]bool `toml:"-" json:"-"`
}

// Has reports whether the field was written in the source.
func (s *Sidecar) Has(field string) bool { return s != nil && s.Present[field] }

// LineOf returns the line of a field, or the first line when unknown.
func (s *Sidecar) LineOf(field string) int {
	if l := s.FieldLines[field]; l > 0 {
		return l
	}
	return s.Line
}

// ReviewDate parses ReviewBy. It returns the zero time and false when the
// field is empty, and an error when it is not a valid YYYY-MM-DD date.
func (s *Sidecar) ReviewDate() (time.Time, bool, error) {
	if s.ReviewBy == "" {
		return time.Time{}, false, nil
	}
	t, err := time.Parse("2006-01-02", s.ReviewBy)
	if err != nil {
		return time.Time{}, true, fmt.Errorf("review_by %q is not a date of the form YYYY-MM-DD", s.ReviewBy)
	}
	return t, true, nil
}

// Problem is something wrong with one sidecar (or one entry's metadata).
type Problem struct {
	File    string
	Line    int
	Plugin  string
	Message string
}

func (p Problem) Error() string {
	if p.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.Message)
	}
	return fmt.Sprintf("%s: %s", p.File, p.Message)
}

// raw mirrors Sidecar for strict decoding; review_by may be a TOML date.
type raw struct {
	Owner        string   `toml:"owner"`
	Status       string   `toml:"status"`
	WhenToUse    []string `toml:"when_to_use"`
	AvoidWhen    []string `toml:"avoid_when"`
	OverlapsWith []string `toml:"overlaps_with"`
	SupersededBy string   `toml:"superseded_by"`
	ReviewBy     any      `toml:"review_by"`
	Support      string   `toml:"support"`
	Docs         string   `toml:"docs"`
}

var keyLine = regexp.MustCompile(`^\s*([A-Za-z0-9_-]+)\s*=`)

// scanLines returns the line of each top-level key in a sidecar text. It is
// a line scan, not a parser, which is enough for the flat files accepted here.
func scanLines(data []byte) map[string]int {
	out := map[string]int{}
	for i, l := range strings.Split(string(data), "\n") {
		if m := keyLine.FindStringSubmatch(l); m != nil {
			if _, dup := out[m[1]]; !dup {
				out[m[1]] = i + 1
			}
		}
	}
	return out
}

// Parse decodes one sidecar file. file is used for provenance only.
func Parse(file string, data []byte) (*Sidecar, error) {
	var r raw
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		var sm *toml.StrictMissingError
		if errors.As(err, &sm) {
			line := 0
			if errs := sm.Errors; len(errs) > 0 {
				line, _ = errs[0].Position()
			}
			return nil, &lineError{line, "unknown key (valid keys: " + strings.Join(orgconfig.SidecarFields(), ", ") + ")"}
		}
		var de *toml.DecodeError
		if errors.As(err, &de) {
			row, _ := de.Position()
			return nil, &lineError{row, decodeMsg(de)}
		}
		return nil, err
	}
	lines := scanLines(data)
	s := &Sidecar{
		Owner: r.Owner, Status: r.Status, WhenToUse: r.WhenToUse, AvoidWhen: r.AvoidWhen,
		OverlapsWith: r.OverlapsWith, SupersededBy: r.SupersededBy, Support: r.Support, Docs: r.Docs,
		File: file, FieldLines: lines, Present: map[string]bool{},
	}
	for k := range lines {
		s.Present[k] = true
	}
	first := 0
	for _, l := range lines {
		if first == 0 || l < first {
			first = l
		}
	}
	s.Line = first
	switch v := r.ReviewBy.(type) {
	case nil:
	case string:
		s.ReviewBy = v
	case toml.LocalDate:
		s.ReviewBy = v.String()
	default:
		return nil, &lineError{lines["review_by"], fmt.Sprintf("review_by must be a date (YYYY-MM-DD), got %T", v)}
	}
	return s, nil
}

// decodeMsg names the TOML key in a decode error, which otherwise talks
// about Go struct fields.
func decodeMsg(de *toml.DecodeError) string {
	msg := strings.TrimPrefix(de.Error(), "toml: ")
	if key := de.Key(); len(key) > 0 {
		return strings.Join(key, ".") + ": " + msg
	}
	return msg
}

type lineError struct {
	line int
	msg  string
}

func (e *lineError) Error() string { return e.msg }

// FromMetadata reads the same fields from a marketplace entry's metadata
// object (single-file mode, snake_case keys). Unknown keys and wrong types
// are errors.
func FromMetadata(file string, md map[string]any) (*Sidecar, error) {
	s := &Sidecar{File: file, Present: map[string]bool{}, FieldLines: map[string]int{}}
	known := map[string]bool{}
	for _, f := range orgconfig.SidecarFields() {
		known[f] = true
	}
	keys := make([]string, 0, len(md))
	for k := range md {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var errs []error
	for _, k := range keys {
		if !known[k] {
			errs = append(errs, fmt.Errorf("metadata.%s: unknown key (valid keys: %s)", k, strings.Join(orgconfig.SidecarFields(), ", ")))
			continue
		}
		s.Present[k] = true
		str := func(dst *string) {
			v, ok := md[k].(string)
			if !ok {
				errs = append(errs, fmt.Errorf("metadata.%s: must be a string", k))
				return
			}
			*dst = v
		}
		list := func(dst *[]string) {
			arr, ok := md[k].([]any)
			if !ok {
				errs = append(errs, fmt.Errorf("metadata.%s: must be an array of strings", k))
				return
			}
			for _, e := range arr {
				es, ok := e.(string)
				if !ok {
					errs = append(errs, fmt.Errorf("metadata.%s: must be an array of strings", k))
					return
				}
				*dst = append(*dst, es)
			}
		}
		switch k {
		case "owner":
			str(&s.Owner)
		case "status":
			str(&s.Status)
		case "when_to_use":
			list(&s.WhenToUse)
		case "avoid_when":
			list(&s.AvoidWhen)
		case "overlaps_with":
			list(&s.OverlapsWith)
		case "superseded_by":
			str(&s.SupersededBy)
		case "review_by":
			str(&s.ReviewBy)
		case "support":
			str(&s.Support)
		case "docs":
			str(&s.Docs)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return s, nil
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// LoadSidecars loads the metadata of every plugin according to cfg: in
// sidecar mode from catalog/plugins/*.toml, in single-file mode from the
// metadata of the entries of the configured marketplaces (which it loads
// itself). The map key is the plugin name. A missing sidecar directory gives
// an empty map. Per-file problems are returned as Problems; the error is for
// failures that stop the whole load.
func LoadSidecars(root string, cfg *orgconfig.Config) (map[string]*Sidecar, []Problem, error) {
	if !cfg.Catalog.Enabled {
		return map[string]*Sidecar{}, nil, nil // profiles-only repo: no catalog data is read
	}
	if cfg.Catalog.MetadataSource != orgconfig.SourceMarketplace {
		return loadDir(root)
	}
	out := map[string]*Sidecar{}
	var probs []Problem
	for _, mf := range cfg.Catalog.Marketplaces {
		m, err := marketplace.LoadFile(root, mf)
		if err != nil {
			return nil, nil, err
		}
		s, p := FromMarketplace(mf, m)
		probs = append(probs, p...)
		for k, v := range s {
			if _, dup := out[k]; !dup {
				out[k] = v
			}
		}
	}
	return out, probs, nil
}

// FromMarketplace reads single-file metadata from every entry of a loaded
// marketplace. Entries without metadata are left out of the map.
func FromMarketplace(file string, m *marketplace.Marketplace) (map[string]*Sidecar, []Problem) {
	out := map[string]*Sidecar{}
	var probs []Problem
	for _, p := range m.Plugins {
		if len(p.Metadata) == 0 {
			continue
		}
		s, err := FromMetadata(file, p.Metadata)
		if err != nil {
			probs = append(probs, Problem{File: file, Plugin: p.Name, Message: err.Error()})
			continue
		}
		out[p.Name] = s
	}
	return out, probs
}

func loadDir(root string) (map[string]*Sidecar, []Problem, error) {
	out := map[string]*Sidecar{}
	entries, err := safepath.ReadDir(root, Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", Dir, err)
	}
	var probs []Problem
	for _, e := range entries {
		fname := e.Name()
		if !strings.HasSuffix(fname, ".toml") {
			continue
		}
		rel := path.Join(Dir, fname)
		name := strings.TrimSuffix(fname, ".toml")
		if !nameRe.MatchString(name) {
			probs = append(probs, Problem{File: rel, Message: "sidecar file name is not a valid plugin name"})
			continue
		}
		data, err := safepath.ReadFile(root, rel, MaxFileSize)
		if err != nil {
			probs = append(probs, Problem{File: rel, Plugin: name, Message: err.Error()})
			continue
		}
		s, err := Parse(rel, data)
		if err != nil {
			pr := Problem{File: rel, Plugin: name, Message: err.Error()}
			var le *lineError
			if errors.As(err, &le) {
				pr.Line = le.line
			}
			probs = append(probs, pr)
			continue
		}
		out[name] = s
	}
	return out, probs, nil
}

// Taxonomy is the set of allowed categories and tags.
type Taxonomy struct {
	Categories []string `toml:"categories"`
	Tags       []string `toml:"tags"`
	// File is the repository-relative path it was read from.
	File string `toml:"-"`
}

// HasCategory reports whether c is an allowed category.
func (t *Taxonomy) HasCategory(c string) bool { return in(t.Categories, c) }

// HasTag reports whether tag is an allowed tag.
func (t *Taxonomy) HasTag(tag string) bool { return in(t.Tags, tag) }

func in(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// MaxTaxonomySize caps the taxonomy file.
const MaxTaxonomySize = 256 << 10

// LoadTaxonomy reads the taxonomy file named by cfg.Lint.Taxonomy. It returns
// nil and no error when the file does not exist, which turns the taxonomy
// rules off.
func LoadTaxonomy(root string, cfg *orgconfig.Config) (*Taxonomy, error) {
	if !cfg.Catalog.Enabled {
		return nil, nil
	}
	if cfg.Lint.Taxonomy == "" {
		return nil, nil
	}
	data, err := safepath.ReadFile(root, cfg.Lint.Taxonomy, MaxTaxonomySize)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", cfg.Lint.Taxonomy, err)
	}
	var t Taxonomy
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		var sm *toml.StrictMissingError
		if errors.As(err, &sm) {
			return nil, fmt.Errorf("%s: unknown key (valid keys: categories, tags):\n%s", cfg.Lint.Taxonomy, sm.String())
		}
		return nil, fmt.Errorf("%s: %w", cfg.Lint.Taxonomy, err)
	}
	t.File = cfg.Lint.Taxonomy
	sort.Strings(t.Categories)
	sort.Strings(t.Tags)
	return &t, nil
}
