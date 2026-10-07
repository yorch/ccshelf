package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// archiveExtras are the files goreleaser puts next to the binary
// (.goreleaser.yaml: files: LICENSE, README.md). They are tolerated and never
// extracted; any other entry makes the archive unacceptable.
var archiveExtras = map[string]bool{"LICENSE": true, "README.md": true}

// maxExtraBytes bounds an extra entry; its content is skipped, not stored.
const maxExtraBytes = 4 << 20

// ErrArchive means the archive is not the shape a release archive has.
var ErrArchive = errors.New("unacceptable archive")

func archiveErr(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrArchive, fmt.Sprintf(format, a...))
}

// checkEntryName accepts only a plain file name: no directory part, no
// traversal, no absolute or drive-qualified path, no backslash, no control
// character.
func checkEntryName(name string) error {
	switch {
	case name == "" || name == "." || name == "..":
		return archiveErr("entry has no usable name")
	case strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) || (len(name) > 1 && name[1] == ':'):
		return archiveErr("entry %q is an absolute path", name)
	case strings.ContainsAny(name, "/\\"):
		return archiveErr("entry %q is not at the top level of the archive", name)
	case strings.IndexFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0:
		return archiveErr("entry name contains a control character")
	}
	return nil
}

// ExtractBinary copies the file called binary out of the archive at path to
// w and returns its size. Only that entry is read: the extras are skipped;
// directories, links, devices, duplicates, other names, traversal and any
// entry larger than max are errors, and so is an archive without the binary.
func ExtractBinary(path string, zipped bool, binary string, w io.Writer, max int64) (int64, error) {
	if zipped {
		return extractZip(path, binary, w, max)
	}
	return extractTarGz(path, binary, w, max)
}

func extractTarGz(path, binary string, w io.Writer, max int64) (int64, error) {
	f, err := os.Open(path) //nolint:gosec // the archive this update just downloaded into its private work directory
	if err != nil {
		return 0, fmt.Errorf("opening the archive: %w", err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return 0, archiveErr("not a gzip file: %v", err)
	}
	defer zr.Close()
	// A decompression bomb is cut off: the binary plus the extras at most.
	tr := tar.NewReader(io.LimitReader(zr, max+8*maxExtraBytes))
	var (
		got  int64
		seen bool
	)
	for entries := 0; ; entries++ {
		if entries > 16 {
			return 0, archiveErr("more than 16 entries")
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, archiveErr("reading the tar stream: %v", err)
		}
		if err := checkEntryName(h.Name); err != nil {
			return 0, err
		}
		if h.Typeflag != tar.TypeReg {
			return 0, archiveErr("entry %q is not a regular file (type %q)", h.Name, string(h.Typeflag))
		}
		switch {
		case h.Name == binary:
			if seen {
				return 0, archiveErr("%s appears twice", binary)
			}
			if h.Size < 0 || h.Size > max {
				return 0, archiveErr("%s is %d bytes, over the %d byte limit", binary, h.Size, max)
			}
			n, err := io.Copy(w, io.LimitReader(tr, h.Size))
			if err != nil {
				return 0, fmt.Errorf("extracting %s: %w", binary, err)
			}
			if n != h.Size {
				return 0, archiveErr("%s is shorter than its header says", binary)
			}
			got, seen = n, true
		case archiveExtras[h.Name]:
			if h.Size > maxExtraBytes {
				return 0, archiveErr("%s is unreasonably large", h.Name)
			}
		default:
			return 0, archiveErr("unexpected entry %q", h.Name)
		}
	}
	if !seen {
		return 0, archiveErr("%s is not in the archive", binary)
	}
	return got, nil
}

func extractZip(path, binary string, w io.Writer, max int64) (int64, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return 0, archiveErr("not a zip file: %v", err)
	}
	defer zr.Close()
	if len(zr.File) > 16 {
		return 0, archiveErr("more than 16 entries")
	}
	var (
		got  int64
		seen bool
	)
	for _, f := range zr.File {
		if err := checkEntryName(f.Name); err != nil {
			return 0, err
		}
		if !f.Mode().IsRegular() {
			return 0, archiveErr("entry %q is not a regular file", f.Name)
		}
		switch {
		case f.Name == binary:
			if seen {
				return 0, archiveErr("%s appears twice", binary)
			}
			if f.UncompressedSize64 > uint64(max) { //nolint:gosec // max is a positive constant
				return 0, archiveErr("%s is %d bytes, over the %d byte limit", binary, f.UncompressedSize64, max)
			}
			rc, err := f.Open()
			if err != nil {
				return 0, archiveErr("opening %s: %v", binary, err)
			}
			n, err := io.Copy(w, io.LimitReader(rc, max+1))
			_ = rc.Close()
			if err != nil {
				return 0, fmt.Errorf("extracting %s: %w", binary, err)
			}
			if n > max || uint64(n) != f.UncompressedSize64 { //nolint:gosec // n is non-negative
				return 0, archiveErr("%s does not match its declared size", binary)
			}
			got, seen = n, true
		case archiveExtras[f.Name]:
			if f.UncompressedSize64 > maxExtraBytes {
				return 0, archiveErr("%s is unreasonably large", f.Name)
			}
		default:
			return 0, archiveErr("unexpected entry %q", f.Name)
		}
	}
	if !seen {
		return 0, archiveErr("%s is not in the archive", binary)
	}
	return got, nil
}
