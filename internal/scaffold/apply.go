package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// ApplyOptions tune Apply.
type ApplyOptions struct {
	// WriteSuggestions writes the suggestion of every needs-merge entry to
	// <file>.ccshelf-suggested. Without it, Apply only reports suggestions.
	WriteSuggestions bool
}

// Result lists what Apply did, as slash-separated paths.
type Result struct {
	Created     []string
	Overwritten []string
	Backups     []string
	Suggestions []string
	// Refused lists the suggestion files that Apply did not write because a
	// file of that name exists and this tool did not write it.
	Refused []string
	// RolledBack lists the files of Created and Suggestions that Apply removed
	// again after a failure (see Apply).
	RolledBack []string
}

// suggestionHeads are the first-line prefixes of the suggestions this tool
// writes; a <file>.ccshelf-suggested that starts with one is ours to refresh.
var suggestionHeads = []string{
	"# Lines that \"ccshelf catalog init\" suggests",
	"# Rules that \"ccshelf catalog init\" suggests",
}

// isOurSuggestion reports whether data is a suggestion file written by this
// tool: its first line is one of the generated headers.
func isOurSuggestion(data []byte) bool {
	first, _, _ := strings.Cut(string(data), "\n")
	for _, h := range suggestionHeads {
		if strings.HasPrefix(first, h) {
			return true
		}
	}
	return false
}

// made is a file this run created, kept for the rollback.
type made struct {
	path    string
	content []byte
}

// Apply performs the plan. Files are independent, and an error stops at the
// first failure with the Result of what was already done. A create fails (it
// never replaces) when the file appeared since planning; an overwrite re-reads
// the file and fails when it changed since planning, so a concurrent edit is
// never lost.
//
// On a failure Apply tries to undo its own work, best effort: it removes the
// files it created in this run (new files and new suggestion files, never a
// pre-existing file, and only when the content on disk is still the content it
// wrote) and the directories it created, when they are empty again. Files it
// replaced with --force and the .bak files stay, as the new content may
// already be in use. Result.Created lists everything that was written before
// the failure and Result.RolledBack what was removed again.
func Apply(fsys FS, plan *Plan, opt ApplyOptions) (*Result, error) {
	res := &Result{}
	var mades []made
	var dirs []string
	create := func(p string, data []byte) error {
		missing := missingDirs(fsys, path.Dir(p))
		if err := fsys.WriteNew(p, data, fileMode); err != nil {
			return err
		}
		dirs = append(dirs, missing...)
		mades = append(mades, made{p, data})
		return nil
	}
	err := apply(fsys, plan, opt, res, create)
	if err != nil {
		rollback(fsys, res, mades, dirs)
	}
	return res, err
}

func apply(fsys FS, plan *Plan, opt ApplyOptions, res *Result, create func(string, []byte) error) error {
	for _, e := range plan.Entries {
		switch e.Action {
		case ActionCreate:
			if err := create(e.Path, e.Content); err != nil {
				if errors.Is(err, fs.ErrExist) {
					return fmt.Errorf("%s appeared after ccshelf made the plan, so ccshelf did not change it (run the command again)", e.Path)
				}
				return fmt.Errorf("creating %s: %w", e.Path, err)
			}
			res.Created = append(res.Created, e.Path)
		case ActionOverwrite:
			cur, err := fsys.ReadFile(e.Path, maxReadSize)
			if err != nil {
				return fmt.Errorf("re-reading %s: %w", e.Path, err)
			}
			if string(cur) != string(e.Old) {
				return fmt.Errorf("%s changed after ccshelf made the plan, so ccshelf did not replace it (run the command again)", e.Path)
			}
			if err := fsys.WriteNew(e.Path+BackupSuffix, e.Old, e.OldMode); err != nil {
				return fmt.Errorf("saving the backup of %s: %w", e.Path, err)
			}
			res.Backups = append(res.Backups, e.Path+BackupSuffix)
			if err := fsys.Replace(e.Path, e.Content, fileMode); err != nil {
				return fmt.Errorf("replacing %s: %w", e.Path, err)
			}
			res.Overwritten = append(res.Overwritten, e.Path)
		case ActionMerge:
			if !opt.WriteSuggestions {
				continue
			}
			if err := writeSuggestion(fsys, e, res, create); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeSuggestion writes <file>.ccshelf-suggested. An existing one is
// refreshed only when this tool wrote it; otherwise it is left alone and
// reported in Result.Refused.
func writeSuggestion(fsys FS, e Entry, res *Result, create func(string, []byte) error) error {
	sp := e.Path + SuggestionSuffix
	err := create(sp, e.Suggestion)
	if errors.Is(err, fs.ErrExist) {
		cur, rerr := fsys.ReadFile(sp, maxReadSize)
		if rerr != nil || !isOurSuggestion(cur) {
			res.Refused = append(res.Refused, sp)
			return nil
		}
		// An earlier suggestion of this tool: refresh it.
		if err = fsys.Replace(sp, e.Suggestion, fileMode); err != nil {
			return fmt.Errorf("writing %s: %w", sp, err)
		}
		res.Suggestions = append(res.Suggestions, sp)
		return nil
	}
	if err != nil {
		return fmt.Errorf("writing %s: %w", sp, err)
	}
	res.Suggestions = append(res.Suggestions, sp)
	return nil
}

// missingDirs lists dir and its ancestors that do not exist yet, deepest first.
func missingDirs(fsys FS, dir string) []string {
	var out []string
	for d := dir; d != "." && d != "/" && d != ""; d = path.Dir(d) {
		if _, err := fsys.Lstat(d); err == nil {
			break
		}
		out = append(out, d)
	}
	return out
}

// rollback removes what this run created.
func rollback(fsys FS, res *Result, files []made, dirs []string) {
	for i := len(files) - 1; i >= 0; i-- {
		f := files[i]
		cur, err := fsys.ReadFile(f.path, maxReadSize)
		if err != nil || !bytes.Equal(cur, f.content) {
			continue
		}
		if fsys.Remove(f.path) == nil {
			res.RolledBack = append(res.RolledBack, f.path)
		}
	}
	sort.Strings(res.RolledBack)
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		_ = fsys.Remove(d) // fails, and is left, when something else is inside
	}
}
