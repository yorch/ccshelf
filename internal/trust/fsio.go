package trust

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// maxStateFile bounds the lockfile and the project trust file.
const maxStateFile = 4 << 20

// readStateFile reads a small regular file without following a symlink. A
// missing file returns an error that satisfies errors.Is(err, fs.ErrNotExist).
func readStateFile(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symlink; refusing to follow it", path)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	f, err := openNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	b, err := io.ReadAll(io.LimitReader(f, maxStateFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxStateFile {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, maxStateFile)
	}
	return b, nil
}

// ensureStateDir creates dir with mode 0700 and checks it is a directory that
// only the user can write to. A symlinked directory (a dotfiles manager) is
// accepted; the state file itself is never followed.
func ensureStateDir(dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(dir), err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("inspecting %s: %w", dir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if err := checkDirOwner(fi); err != nil {
		return fmt.Errorf("directory %s: %w", dir, err)
	}
	return nil
}

// writeStateFile atomically replaces path with data (mode 0600, directory
// 0700): exclusive-create temporary file in the same directory, fsync, rename.
func writeStateFile(path string, data []byte) error {
	if len(data) > maxStateFile {
		return fmt.Errorf("refusing to write %d bytes to %s", len(data), path)
	}
	dir := filepath.Dir(path)
	if err := ensureStateDir(dir); err != nil {
		return err
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; refusing to write through it", path)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return fmt.Errorf("generating a temporary name: %w", err)
	}
	tmp := filepath.Join(dir, "."+filepath.Base(path)+"."+hex.EncodeToString(rnd[:])+".tmp")
	f, err := openNoFollow(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("creating a temporary file: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("syncing %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	ok = true
	return nil
}
