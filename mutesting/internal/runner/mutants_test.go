package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The fixtures below were produced by running go-mutesting v2.8.2 over the
// source in originalFixture and recording the checksums it assigned. They pin
// this package's reimplementation of the tool's mutation key to the real
// thing: if the two ever diverge, exclusions stop selecting the right
// mutations and changed-lines mode would silently measure the wrong set.
const originalFixture = `package adder

func Add(a, b int) int {
	return a + b
}

func Mul(a, b int) int {
	if a == 0 {
		return 0
	}
	return a * b
}
`

func TestStableMutationKeyMatchesTool(t *testing.T) {
	cases := []struct {
		name     string
		mutated  string
		want     string
		wantLine int
	}{
		{
			name:     "arithmetic on line 4",
			mutated:  replaceLine(originalFixture, 4, "\treturn a - b"),
			want:     "6090b0701dc32c02a0d6df7af2d5f962",
			wantLine: 4,
		},
		{
			name:     "arithmetic on line 11",
			mutated:  replaceLine(originalFixture, 11, "\treturn a / b"),
			want:     "73e6bf5ece5e4002f77d43b678b346a8",
			wantLine: 11,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, lines := stableMutationKey([]byte(originalFixture), []byte(tc.mutated))
			if got != tc.want {
				t.Errorf("checksum = %s, want %s", got, tc.want)
			}
			if len(lines) != 1 || lines[0] != tc.wantLine {
				t.Errorf("changed lines = %v, want [%d]", lines, tc.wantLine)
			}
		})
	}
}

func TestStableMutationKeyIgnoresLocation(t *testing.T) {
	// The tool keys a mutation on the text it changes, not on where the change
	// happens. exclusionsForChangedLines depends on this to stay correct.
	a := "package a\n\nfunc F() int {\n\treturn 1 + 2\n}\n"
	b := "package a\n\n// a comment\n\nfunc F() int {\n\treturn 1 + 2\n}\n"
	keyA, _ := stableMutationKey([]byte(a), []byte(replaceLine(a, 4, "\treturn 1 - 2")))
	keyB, _ := stableMutationKey([]byte(b), []byte(replaceLine(b, 6, "\treturn 1 - 2")))
	if keyA != keyB {
		t.Errorf("same mutation at different lines produced %s and %s", keyA, keyB)
	}
}

func TestChangedLinesIsolatesShiftingMutations(t *testing.T) {
	// A mutator that removes or adds a line shifts every line below it. Only
	// the region it rewrote is changed; reporting the shifted remainder too
	// would sweep unrelated mutations into a changed-lines run.
	lines := splitLines(originalFixture)
	cases := []struct {
		name    string
		mutated string
		want    []int
	}{
		{
			name:    "replacement",
			mutated: replaceLine(originalFixture, 4, "\treturn a - b"),
			want:    []int{4},
		},
		{
			name:    "removed line",
			mutated: joinLines(append(append([]string{}, lines[:8]...), lines[9:]...)),
			want:    []int{9},
		},
		{
			name: "added line",
			mutated: joinLines(append(append(append([]string{}, lines[:8]...),
				"\t\tpanic(\"\")"), lines[8:]...)),
			want: []int{9},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, got := stableMutationKey([]byte(originalFixture), []byte(tc.mutated))
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("changed lines = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestChangedLinesOnIdenticalFiles(t *testing.T) {
	if _, got := stableMutationKey([]byte(originalFixture), []byte(originalFixture)); got != nil {
		t.Errorf("changed lines = %v, want none", got)
	}
}

func TestExclusionsKeepInScopeTwin(t *testing.T) {
	// The same textual mutation can appear both on a changed line and
	// elsewhere. Excluding the out-of-scope copy would also drop the in-scope
	// one, because the tool generates only the first of any duplicate pair.
	changed := &ChangedLines{Files: map[string][][2]int{"pkg/a.go": {{10, 10}}}}
	mutants := []mutant{
		{Checksum: "shared", Workspace: "pkg/a.go", Lines: []int{10}},
		{Checksum: "shared", Workspace: "pkg/a.go", Lines: []int{99}},
		{Checksum: "elsewhere", Workspace: "pkg/a.go", Lines: []int{42}},
	}
	exclude, kept := exclusionsForChangedLines(mutants, changed)
	if kept != 1 {
		t.Errorf("kept = %d, want 1", kept)
	}
	if len(exclude) != 1 || exclude[0] != "elsewhere" {
		t.Errorf("exclude = %v, want [elsewhere]", exclude)
	}
}

func TestExclusionsCoverRanges(t *testing.T) {
	changed := &ChangedLines{Files: map[string][][2]int{
		"pkg/a.go": {{10, 12}, {20, 20}},
	}}
	mutants := []mutant{
		{Checksum: "in-range-start", Workspace: "pkg/a.go", Lines: []int{10}},
		{Checksum: "in-range-end", Workspace: "pkg/a.go", Lines: []int{12}},
		{Checksum: "single-line-range", Workspace: "pkg/a.go", Lines: []int{20}},
		{Checksum: "out-of-range", Workspace: "pkg/a.go", Lines: []int{13}},
		{Checksum: "other-file", Workspace: "pkg/b.go", Lines: []int{10}},
		{Checksum: "generated", Workspace: "", Lines: []int{10}},
	}
	exclude, kept := exclusionsForChangedLines(mutants, changed)
	if kept != 3 {
		t.Errorf("kept = %d, want 3", kept)
	}
	want := map[string]bool{"out-of-range": true, "other-file": true, "generated": true}
	if len(exclude) != len(want) {
		t.Fatalf("exclude = %v, want %d entries", exclude, len(want))
	}
	for _, c := range exclude {
		if !want[c] {
			t.Errorf("unexpected exclusion %s", c)
		}
	}
}

func TestCollectMutants(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "sub")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(pkg, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go.original", originalFixture)
	write("a.go.0", replaceLine(originalFixture, 4, "\treturn a - b"))
	write("a.go.1", replaceLine(originalFixture, 11, "\treturn a / b"))

	got, err := collectMutants(dir, map[string]string{"sub/a.go": "examples/adder/a.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("found %d mutants, want 2", len(got))
	}
	for _, mu := range got {
		if mu.Workspace != "examples/adder/a.go" {
			t.Errorf("workspace = %q, want the mapped path", mu.Workspace)
		}
		if len(mu.Lines) != 1 {
			t.Errorf("mutant %s changed %v, want one line", mu.Checksum, mu.Lines)
		}
	}
}

func TestParseChangedLinesTreatsBlankAsUnset(t *testing.T) {
	// An unset spec means "mutate everything", so anything empty has to come
	// back nil rather than as a spec that selects nothing.
	for _, spec := range []string{"", "  \n", "{}", `{"files":{}}`} {
		got, err := parseChangedLines(spec)
		if err != nil {
			t.Fatalf("spec %q: %v", spec, err)
		}
		if got != nil {
			t.Errorf("spec %q produced %v, want nil", spec, got)
		}
	}
}

func TestParseChangedLinesRejectsGarbage(t *testing.T) {
	// A malformed spec must not silently degrade into a full run.
	if _, err := parseChangedLines("not json"); err == nil {
		t.Error("expected an error for malformed JSON")
	}
}

func TestParseChangedLines(t *testing.T) {
	got, err := parseChangedLines(`{"files":{"a.go":[[1,2],[9,9]]}}`)
	if err != nil {
		t.Fatal(err)
	}
	if !got.covers("a.go", []int{2}) {
		t.Error("line 2 should be within range [1,2]")
	}
	if !got.covers("a.go", []int{9}) {
		t.Error("line 9 should be within range [9,9]")
	}
	if got.covers("a.go", []int{5}) {
		t.Error("line 5 is outside every range")
	}
	if got.covers("b.go", []int{1}) {
		t.Error("an unlisted file has no changed lines")
	}
}

// replaceLine swaps the 1-based line n of s for replacement.
func replaceLine(s string, n int, replacement string) string {
	lines := splitLines(s)
	lines[n-1] = replacement
	return joinLines(lines)
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
