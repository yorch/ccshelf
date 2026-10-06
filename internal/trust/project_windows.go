//go:build windows

package trust

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// readProjectTree reads every directory and file below dir. Windows has no
// openat, so the walk is by path and every file is opened with the same
// symlink check as the state files.
func readProjectTree(dir string) ([]projectEntry, error) {
	lim := &projectLimits{dir: dir}
	var out []projectEntry
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.Type()&os.ModeSymlink != 0:
			return errProjectSymlink(rel)
		case d.IsDir():
			if err := lim.addEntry(); err != nil {
				return err
			}
			out = append(out, projectEntry{rel: rel, isDir: true})
		case d.Type().IsRegular():
			if err := lim.addEntry(); err != nil {
				return err
			}
			f, err := openNoFollow(p, os.O_RDONLY, 0)
			if err != nil {
				return fmt.Errorf("reading %s/%s: %w", ProjectFolder, rel, err)
			}
			b, err := readProjectFile(f, rel)
			_ = f.Close()
			if err != nil {
				return err
			}
			if err := lim.addBytes(len(b)); err != nil {
				return err
			}
			out = append(out, projectEntry{rel: rel, data: b})
		default:
			return errProjectSpecial(rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortProjectEntries(out)
	return out, nil
}
