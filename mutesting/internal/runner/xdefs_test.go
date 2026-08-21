package main

import "testing"

func TestGoflagsXDefs(t *testing.T) {
	for _, tc := range []struct {
		name string
		defs map[string]string
		want string
	}{
		{
			name: "no x_defs",
			want: "",
		},
		{
			name: "several are sorted into one -ldflags",
			defs: map[string]string{"pkg.B": "two", "pkg.A": "one"},
			want: ` '-ldflags=-X "pkg.A=one" -X "pkg.B=two"'`,
		},
		{
			// The linker splits -ldflags the same way the go command splits
			// GOFLAGS, so an unquoted value would end at its first space.
			name: "a value with spaces stays one definition",
			defs: map[string]string{"pkg.Desc": "a stamped description"},
			want: ` '-ldflags=-X "pkg.Desc=a stamped description"'`,
		},
		{
			name: "a value that cannot survive the split is dropped",
			defs: map[string]string{"pkg.Quoted": `say "hi"`, "pkg.Plain": "fine"},
			want: ` '-ldflags=-X "pkg.Plain=fine"'`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := goflagsXDefs(tc.defs); got != tc.want {
				t.Errorf("goflagsXDefs() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStampedXDef(t *testing.T) {
	for _, tc := range []struct {
		name    string
		defs    map[string]string
		wantKey string
	}{
		{
			name: "expanded values are fine",
			defs: map[string]string{"pkg.Fixture": "_main/pkg/testdata/f.json"},
		},
		{
			name:    "a workspace status placeholder is reported",
			defs:    map[string]string{"pkg.A": "plain", "pkg.Commit": "{STABLE_GIT_COMMIT}"},
			wantKey: "pkg.Commit",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, _, ok := stampedXDef(tc.defs)
			if ok != (tc.wantKey != "") {
				t.Fatalf("stampedXDef() ok = %v, want %v", ok, tc.wantKey != "")
			}
			if k != tc.wantKey {
				t.Errorf("stampedXDef() key = %q, want %q", k, tc.wantKey)
			}
		})
	}
}
