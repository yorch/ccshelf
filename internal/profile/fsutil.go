package profile

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// refusedMode is the set of file types that are never followed or read:
// symlinks, and on Windows junctions and other reparse points, which Go
// reports as irregular.
const refusedMode = fs.ModeSymlink | fs.ModeIrregular

// readOpened reads the regular file at path whose Lstat result is want. The
// file is opened without following a final symlink where the OS allows it
// (openNoFollow) and its fstat is compared with want, which closes the gap
// between the Lstat and the open (B7).
func readOpened(path string, want fs.FileInfo, max int64) ([]byte, error) {
	f, err := openNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrPath, filepath.Base(path))
	}
	if !os.SameFile(fi, want) {
		return nil, fmt.Errorf("%w: %s changed while it was being opened", ErrPath, filepath.Base(path))
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", filepath.Base(path), max)
	}
	return b, nil
}

// readFileNoFollow reads a regular file of at most max bytes and refuses a
// symlink as the final path component.
func readFileNoFollow(path string, max int64) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&refusedMode != 0 {
		return nil, fmt.Errorf("%w: %s is a symbolic link", ErrPath, filepath.Base(path))
	}
	return readOpened(path, fi, max)
}

// readConfined reads root/rel (rel uses forward slashes and was already
// syntax-checked) and returns the bytes and the path read. root itself may be
// reached through symlinks (it is chosen by the tool, not by a profile), but
// every component below it is Lstat'ed and a symlink anywhere is refused: a
// shared repository can contain links, and following one is how a profile
// would read ~/.ssh (B7). The file is then opened with O_NOFOLLOW where
// available and compared with the Lstat result. On Windows, where O_NOFOLLOW
// does not exist, the Lstat check (which also refuses junctions) is the
// protection.
func readConfined(root, rel string, max int64) ([]byte, string, error) {
	rootEval, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, "", fmt.Errorf("resolving source root: %w", err)
	}
	segs := strings.Split(rel, "/")
	cur := rootEval
	var fi fs.FileInfo
	for i, seg := range segs {
		if seg == "" || seg == "." || seg == ".." {
			return nil, "", fmt.Errorf("%w: %q has an unsafe component", ErrPath, rel)
		}
		cur = filepath.Join(cur, seg)
		fi, err = os.Lstat(cur)
		if err != nil {
			return nil, "", err
		}
		if fi.Mode()&refusedMode != 0 {
			return nil, "", fmt.Errorf("%w: %s is a symbolic link", ErrPath, strings.Join(segs[:i+1], "/"))
		}
		if i < len(segs)-1 && !fi.IsDir() {
			return nil, "", fmt.Errorf("%w: %s is not a directory", ErrPath, strings.Join(segs[:i+1], "/"))
		}
	}
	b, err := readOpened(cur, fi, max)
	if err != nil {
		return nil, "", err
	}
	return b, cur, nil
}
