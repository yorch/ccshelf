// Package tomlkeys closes a gap in the TOML decoder. go-toml matches struct
// tags case-insensitively. Thus "Inherit_User_Settings = false" written after
// "inherit_user_settings = true" would silently override it, and a reviewer
// who reads the file would see two different spellings of one setting. Check
// decodes the same bytes into a generic map and compares every key with the
// exact tags of the target struct. Thus the only accepted spelling is the one
// that the schema documents.
package tomlkeys

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Issue is one key whose spelling is not exactly a struct tag.
type Issue struct {
	// Path is the dotted path of the key as written, for example
	// "session.Inherit_User_Settings" or "servers.fig.Windows".
	Path string
	// Key is the key as written.
	Key string
	// Want is the exact spelling the schema uses.
	Want string
}

// String formats the issue as `key "X" must be spelled "x"`.
func (i Issue) String() string {
	return fmt.Sprintf("key %q must be spelled %q", i.Key, i.Want)
}

// Check decodes raw into a generic map and walks it against the type of
// target (a struct or a pointer to one), returning one Issue per key that
// matches a field only case-insensitively. Check does not report keys that
// match no field, because the strict decoder reports unknown keys. Check
// sorts the issues by path. An error means raw is not valid TOML.
func Check(raw []byte, target any) ([]Issue, error) {
	var generic map[string]any
	if err := toml.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("decoding keys: %w", err)
	}
	var out []Issue
	walk(reflect.TypeOf(target), generic, "", &out)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func deref(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// tags returns the exact TOML key of every exported field of struct type t.
func tags(t reflect.Type) map[string]reflect.Type {
	m := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := f.Name
		if tag, ok := f.Tag.Lookup("toml"); ok {
			name = strings.Split(tag, ",")[0]
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
		}
		m[name] = f.Type
	}
	return m
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func walk(t reflect.Type, v any, path string, out *[]Issue) {
	t = deref(t)
	if t == nil {
		return
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		want := tags(t)
		for k, sub := range m {
			if ft, exact := want[k]; exact {
				walk(ft, sub, join(path, k), out)
				continue
			}
			for name := range want {
				if strings.EqualFold(name, k) {
					*out = append(*out, Issue{Path: join(path, k), Key: k, Want: name})
					break
				}
			}
		}
	case reflect.Map:
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		for k, sub := range m {
			walk(t.Elem(), sub, join(path, k), out)
		}
	case reflect.Slice, reflect.Array:
		switch items := v.(type) {
		case []any:
			for i, sub := range items {
				walk(t.Elem(), sub, fmt.Sprintf("%s[%d]", path, i), out)
			}
		case []map[string]any:
			for i, sub := range items {
				walk(t.Elem(), sub, fmt.Sprintf("%s[%d]", path, i), out)
			}
		}
	}
}
