package tomlkeys

import (
	"reflect"
	"testing"
)

type leaf struct {
	Value string `toml:"value,omitempty"`
	Skip  string `toml:"-"`
	Plain string
}

type tree struct {
	Name  string            `toml:"name"`
	Inner leaf              `toml:"inner"`
	Ptr   *leaf             `toml:"ptr"`
	List  []leaf            `toml:"list"`
	Map   map[string]leaf   `toml:"map"`
	Env   map[string]string `toml:"env"`
	Words []string          `toml:"words"`
	priv  string
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want []string
	}{
		{"exact", "name = 'a'\n[inner]\nvalue = 'x'\n", nil},
		{"top level", "Name = 'a'\n", []string{"Name"}},
		{"table name", "[Inner]\nvalue = 'x'\n", []string{"Inner"}},
		{"nested key", "[inner]\nVALUE = 'x'\n", []string{"inner.VALUE"}},
		{"pointer", "[ptr]\nValue = 'x'\n", []string{"ptr.Value"}},
		{"array of tables", "[[list]]\nvalue = 'x'\n[[list]]\nVAlue = 'y'\n", []string{"list[1].VAlue"}},
		{"map of tables", "[map.a]\nValue = 'x'\n", []string{"map.a.Value"}},
		{"map keys are free", "[env]\nFOO = 'x'\nfoo = 'y'\n[map.Mixed]\nvalue = 'z'\n", nil},
		{"untagged field uses its name", "[inner]\nPlain = 'x'\nplain = 'y'\n", []string{"inner.plain"}},
		{"dash field is not a key", "[inner]\nskip = 'x'\n", nil},
		{"unknown keys are not this package's job", "bogus = 1\n[inner]\nzzz = 2\n", nil},
		{"unexported field is not a key", "priv = 'x'\n", nil},
		{"words", "words = ['a']\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Check([]byte(tc.toml), tree{})
			if err != nil {
				t.Fatal(err)
			}
			var paths []string
			for _, i := range got {
				paths = append(paths, i.Path)
			}
			if !reflect.DeepEqual(paths, tc.want) {
				t.Fatalf("paths = %v, want %v", paths, tc.want)
			}
		})
	}
}

var _ = tree{priv: ""} // the unexported field exists to prove it is not a key

func TestIssueText(t *testing.T) {
	got, err := Check([]byte("Name = 'a'\n"), &tree{})
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].String() != `key "Name" must be spelled "name"` || got[0].Want != "name" || got[0].Key != "Name" {
		t.Errorf("issue = %+v (%s)", got[0], got[0])
	}
}

func TestCheckInvalidTOML(t *testing.T) {
	if _, err := Check([]byte("= broken"), tree{}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestCheckNonStruct(t *testing.T) {
	if got, err := Check([]byte("a = 1\n"), 5); err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := Check([]byte("a = 1\n"), nil); err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
}
