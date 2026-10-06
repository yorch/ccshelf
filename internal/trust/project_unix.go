//go:build !windows

package trust

import (
	"errors"
	"fmt"
	"os"
	"path"

	"golang.org/x/sys/unix"
)

// maxProjectDepth bounds directory nesting below the project folder.
const maxProjectDepth = 64

// readProjectTree reads every directory and file below dir. It works relative
// to open directory descriptors (openat with O_NOFOLLOW), so replacing an
// intermediate directory with a symlink while the walk runs is never followed.
func readProjectTree(dir string) ([]projectEntry, error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
			return nil, fmt.Errorf("%s must be a plain directory, not a symlink", dir)
		}
		return nil, fmt.Errorf("opening %s: %w", dir, err)
	}
	root := os.NewFile(uintptr(fd), dir)
	defer root.Close()
	lim := &projectLimits{dir: dir}
	var out []projectEntry
	if err := walkProjectDir(root, "", 0, lim, &out); err != nil {
		return nil, err
	}
	sortProjectEntries(out)
	return out, nil
}

func walkProjectDir(d *os.File, rel string, depth int, lim *projectLimits, out *[]projectEntry) error {
	if depth > maxProjectDepth {
		return fmt.Errorf("%s is nested too deeply", lim.dir)
	}
	ents, err := d.ReadDir(-1)
	if err != nil {
		return fmt.Errorf("reading %s/%s: %w", ProjectFolder, rel, err)
	}
	for _, e := range ents {
		name := e.Name()
		child := path.Join(rel, name)
		if e.Type()&os.ModeSymlink != 0 {
			return errProjectSymlink(child)
		}
		cfd, err := unix.Openat(int(d.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			if errors.Is(err, unix.ELOOP) {
				return errProjectSymlink(child)
			}
			return fmt.Errorf("reading %s/%s: %w", ProjectFolder, child, err)
		}
		cf := os.NewFile(uintptr(cfd), name)
		err = visitProjectEntry(cf, child, depth, lim, out)
		_ = cf.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func visitProjectEntry(cf *os.File, child string, depth int, lim *projectLimits, out *[]projectEntry) error {
	st, err := cf.Stat()
	if err != nil {
		return fmt.Errorf("reading %s/%s: %w", ProjectFolder, child, err)
	}
	switch {
	case st.IsDir():
		if err := lim.addEntry(); err != nil {
			return err
		}
		*out = append(*out, projectEntry{rel: child, isDir: true})
		return walkProjectDir(cf, child, depth+1, lim, out)
	case st.Mode().IsRegular():
		if err := lim.addEntry(); err != nil {
			return err
		}
		b, err := readProjectFile(cf, child)
		if err != nil {
			return err
		}
		if err := lim.addBytes(len(b)); err != nil {
			return err
		}
		*out = append(*out, projectEntry{rel: child, data: b})
		return nil
	}
	return errProjectSpecial(child)
}
