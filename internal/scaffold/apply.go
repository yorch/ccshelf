package scaffold

import (
	"errors"
	"fmt"
	"io/fs"
)

// ApplyOptions tune Apply.
type ApplyOptions struct {
	// WriteSuggestions writes the suggestion of every needs-merge entry to
	// <file>.ccshelf-suggested. Without it, suggestions are only reported.
	WriteSuggestions bool
}

// Result lists what Apply did, as slash-separated paths.
type Result struct {
	Created     []string
	Overwritten []string
	Backups     []string
	Suggestions []string
}

// Apply performs the plan. Files are independent: there is no rollback, and an
// error stops at the first failure with the Result of what was already done.
// A create fails (it never replaces) when the file appeared since planning; an
// overwrite re-reads the file and fails when it changed since planning, so a
// concurrent edit is never lost.
func Apply(fsys FS, plan *Plan, opt ApplyOptions) (*Result, error) {
	res := &Result{}
	for _, e := range plan.Entries {
		switch e.Action {
		case ActionCreate:
			if err := fsys.WriteNew(e.Path, e.Content, fileMode); err != nil {
				if errors.Is(err, fs.ErrExist) {
					return res, fmt.Errorf("%s appeared after the plan was made; nothing of it was changed (run the command again)", e.Path)
				}
				return res, fmt.Errorf("creating %s: %w", e.Path, err)
			}
			res.Created = append(res.Created, e.Path)
		case ActionOverwrite:
			cur, err := fsys.ReadFile(e.Path, maxReadSize)
			if err != nil {
				return res, fmt.Errorf("re-reading %s: %w", e.Path, err)
			}
			if string(cur) != string(e.Old) {
				return res, fmt.Errorf("%s changed after the plan was made; it was not replaced (run the command again)", e.Path)
			}
			if err := fsys.WriteNew(e.Path+BackupSuffix, e.Old, e.OldMode); err != nil {
				return res, fmt.Errorf("saving the backup of %s: %w", e.Path, err)
			}
			res.Backups = append(res.Backups, e.Path+BackupSuffix)
			if err := fsys.Replace(e.Path, e.Content, fileMode); err != nil {
				return res, fmt.Errorf("replacing %s: %w", e.Path, err)
			}
			res.Overwritten = append(res.Overwritten, e.Path)
		case ActionMerge:
			if !opt.WriteSuggestions {
				continue
			}
			sp := e.Path + SuggestionSuffix
			err := fsys.WriteNew(sp, e.Suggestion, fileMode)
			if errors.Is(err, fs.ErrExist) {
				// An earlier suggestion of this tool: refresh it.
				err = fsys.Replace(sp, e.Suggestion, fileMode)
			}
			if err != nil {
				return res, fmt.Errorf("writing %s: %w", sp, err)
			}
			res.Suggestions = append(res.Suggestions, sp)
		}
	}
	return res, nil
}
