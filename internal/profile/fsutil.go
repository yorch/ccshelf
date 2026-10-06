package profile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// readFileLimited reads a regular file of at most max bytes.
func readFileLimited(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
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
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", filepath.Base(path), max)
	}
	return b, nil
}

// readConfined reads root/rel (rel uses forward slashes and was already
// syntax-checked by CheckRelPath) after resolving symlinks, and refuses any
// result outside root. It returns the bytes and the resolved path.
func readConfined(root, rel string, max int64) ([]byte, string, error) {
	rootEval, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, "", fmt.Errorf("resolving source root: %w", err)
	}
	full := filepath.Join(rootEval, filepath.FromSlash(rel))
	eval, err := filepath.EvalSymlinks(full)
	if err != nil {
		return nil, "", err
	}
	if !within(rootEval, eval) {
		return nil, "", fmt.Errorf("%w: %s resolves outside the source root", ErrPath, rel)
	}
	b, err := readFileLimited(eval, max)
	if err != nil {
		return nil, "", err
	}
	return b, eval, nil
}

// within reports whether path is root or below it.
func within(root, path string) bool {
	r, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}
