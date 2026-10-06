//go:build !windows

package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForeignOwnerRefused(t *testing.T) {
	d := filepath.Join(t.TempDir(), "foreign")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkOwnerAs(fi, os.Geteuid()+1); err == nil || !strings.Contains(err.Error(), "owned by") {
		t.Fatalf("err = %v", err)
	}
	if err := checkOwnerAs(fi, os.Geteuid()); err != nil {
		t.Fatalf("own directory refused: %v", err)
	}
}
