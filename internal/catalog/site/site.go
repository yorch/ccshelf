package site

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ccshelf/ccshelf/internal/catalog"
)

//go:embed templates/index.html templates/app.js templates/style.css
var templates embed.FS

// File is one generated file.
type File struct {
	// Name is the file name inside the output directory.
	Name string
	Data []byte
}

// ContentSecurityPolicy is the policy written into the page.
const ContentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'none'"

// Render generates the site files in memory, in the order index.html, app.js,
// style.css, catalog.json.
func Render(c *catalog.Catalog) ([]File, error) {
	js, err := catalog.JSON(c)
	if err != nil {
		return nil, err
	}
	// JSON escapes <, > and &; this check guards the guarantee.
	if bytes.ContainsAny(js, "<>&") || bytes.Contains(js, []byte(" ")) || bytes.Contains(js, []byte(" ")) {
		return nil, errors.New("catalog JSON contains an unescaped HTML character")
	}
	index, err := templates.ReadFile("templates/index.html")
	if err != nil {
		return nil, fmt.Errorf("read index template: %w", err)
	}
	title := c.Title
	if title == "" {
		title = "Plugin catalog"
	}
	// One pass, so replaced text is never scanned for placeholders again.
	page := strings.NewReplacer(
		"{{TITLE}}", html.EscapeString(catalog.Text(title, 200)),
		"{{CATALOG_JSON}}", strings.TrimSpace(string(js)),
	).Replace(string(index))
	files := []File{{Name: "index.html", Data: []byte(page)}}
	for _, name := range []string{"app.js", "style.css"} {
		b, err := templates.ReadFile("templates/" + name)
		if err != nil {
			return nil, fmt.Errorf("read %s template: %w", name, err)
		}
		files = append(files, File{Name: name, Data: b})
	}
	files = append(files, File{Name: "catalog.json", Data: js})
	return files, nil
}

// Published file and directory modes. The site is published output that a web
// server or Pages action must be able to read, so it is world-readable; the
// private-cache rule (0700/0600) applies to the tool's cache, not to this.
const (
	dirMode  fs.FileMode = 0o755
	fileMode fs.FileMode = 0o644
)

// Write generates the site into dir, creating it (mode 0755) when needed.
func Write(dir string, c *catalog.Catalog) error {
	files, err := Render(c)
	if err != nil {
		return err
	}
	_, existed := os.Stat(dir)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if existed != nil {
		// MkdirAll is subject to the umask; a new publish directory is 0755.
		if err := os.Chmod(dir, dirMode); err != nil {
			return fmt.Errorf("set mode of %s: %w", dir, err)
		}
	}
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	for _, f := range files {
		if err := writeFile(dir, f); err != nil {
			return err
		}
	}
	return nil
}

// writeFile writes atomically: an exclusively created temporary file (os.CreateTemp, mode 0600) that is
// set to 0644 before a rename over the target. It refuses a target that is a symlink or anything
// but a regular file.
func writeFile(dir string, f File) error {
	target := filepath.Join(dir, f.Name)
	if st, err := os.Lstat(target); err == nil {
		if !st.Mode().IsRegular() {
			return fmt.Errorf("refusing to overwrite %s: not a regular file", target)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+f.Name+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", f.Name, err)
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := tmp.Write(f.Data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("write %s: %w", f.Name, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close %s: %w", f.Name, err)
	}
	if err := os.Chmod(name, fileMode); err != nil {
		cleanup()
		return fmt.Errorf("set mode of %s: %w", f.Name, err)
	}
	if err := os.Rename(name, target); err != nil {
		cleanup()
		return fmt.Errorf("replace %s: %w", target, err)
	}
	return nil
}
