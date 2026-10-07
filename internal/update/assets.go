package update

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Names fixed by .goreleaser.yaml.
const (
	// ChecksumsName is the checksums asset of a release.
	ChecksumsName = "checksums.txt"
	// SignatureName is the keyless signature bundle of the checksums asset.
	SignatureName = "checksums.txt.sigstore.json"
	binaryBase    = "ccshelf"
)

// Archive describes the asset to download for a platform.
type Archive struct {
	// Name is the release asset, ccshelf_<version>_<os>_<arch>.tar.gz (.zip
	// on Windows).
	Name string
	// Binary is the name of the executable inside it.
	Binary string
	// Zip is true for a zip archive, false for a tar.gz.
	Zip bool
}

// ArchiveFor returns the archive of version v for goos/goarch, following the
// name_template of .goreleaser.yaml ("ccshelf_<version>_<os>_<arch>", with
// the version lacking its "v").
func ArchiveFor(v Version, goos, goarch string) (Archive, error) {
	switch goos {
	case "darwin", "linux", "windows":
	default:
		return Archive{}, fmt.Errorf("there is no ccshelf build for %s/%s", goos, goarch)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return Archive{}, fmt.Errorf("there is no ccshelf build for %s/%s", goos, goarch)
	}
	a := Archive{Name: fmt.Sprintf("%s_%s_%s_%s", binaryBase, v, goos, goarch), Binary: binaryBase}
	if goos == "windows" {
		a.Name += ".zip"
		a.Binary += ".exe"
		a.Zip = true
	} else {
		a.Name += ".tar.gz"
	}
	return a, nil
}

var checksumLineRe = regexp.MustCompile(`^([0-9A-Fa-f]{64}) [ *]([^\s].*)$`)

// ParseChecksums returns the lower-case SHA-256 recorded for the file name in
// the sha256sum-style text of checksums.txt. Exactly one line must name the
// file: none is an error, and so are two (an ambiguous list is never trusted).
// Malformed lines are errors too, so that a damaged file is not half-read.
func ParseChecksums(data []byte, name string) (string, error) {
	var found []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), MaxChecksumsBytes)
	n := 0
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		n++
		m := checksumLineRe.FindStringSubmatch(line)
		if m == nil {
			return "", fmt.Errorf("checksums.txt line %d is not \"<sha256>  <file>\"", n)
		}
		if m[2] == name {
			found = append(found, strings.ToLower(m[1]))
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("reading checksums.txt: %w", err)
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("checksums.txt has no entry for %s", name)
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("checksums.txt has %d entries for %s; refusing to guess", len(found), name)
}

// ErrChecksum means a file's SHA-256 is not the one the release recorded.
var ErrChecksum = errors.New("checksum mismatch")

// VerifySHA256 compares the hex digest of a download with the recorded one.
func VerifySHA256(got [sha256.Size]byte, wantHex string) error {
	want, err := hex.DecodeString(wantHex)
	if err != nil || len(want) != sha256.Size {
		return fmt.Errorf("%w: the recorded checksum is not a SHA-256", ErrChecksum)
	}
	if subtle.ConstantTimeCompare(got[:], want) != 1 {
		return fmt.Errorf("%w: downloaded %s, release says %s", ErrChecksum, hex.EncodeToString(got[:]), wantHex)
	}
	return nil
}
