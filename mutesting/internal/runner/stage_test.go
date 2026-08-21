package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSource(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStageModuleLayout(t *testing.T) {
	in := t.TempDir()
	m := &Manifest{
		ImportPath: "example.com/app",
		GoVersion:  "1.26",
		Srcs: []Source{
			{Path: writeSource(t, in, "app.go", "package app\n"), Name: "app.go", Workspace: "src/app/app.go"},
			{Path: writeSource(t, in, "app_test.go", "package app\n"), Name: "app_test.go", Workspace: "src/app/app_test.go"},
		},
		Deps: []Dep{
			{
				ImportPath: "example.com/other",
				Srcs:       []Source{{Path: writeSource(t, in, "other.go", "package other\n"), Name: "other.go"}},
			},
			{
				// Lives beneath the module path, so it belongs inside the
				// module rather than in vendor/.
				ImportPath: "example.com/app/internal/util",
				Srcs:       []Source{{Path: writeSource(t, in, "util.go", "package util\n"), Name: "util.go"}},
			},
			{
				// A dependency whose //go:embed pattern does not match fails
				// to compile, which mutation testing would score as a killed
				// mutant, so its embedded files have to be staged too.
				ImportPath: "example.com/embedder",
				Srcs:       []Source{{Path: writeSource(t, in, "embedder.go", "package embedder\n"), Name: "embedder.go"}},
				EmbedSrcs: []Source{
					{Path: writeSource(t, in, "defaults.binpb", "\x00"), Name: "defaults.binpb"},
					{Path: writeSource(t, in, "tmpl/page.html", "<p>"), Name: "tmpl/page.html"},
				},
			},
		},
	}

	root := filepath.Join(t.TempDir(), "module")
	st, err := stageModule(m, root)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"app.go",
		"app_test.go",
		"internal/util/util.go",
		"vendor/example.com/other/other.go",
		"vendor/example.com/embedder/defaults.binpb",
		// An embed pattern can name a subdirectory, so the layout under the
		// package has to survive staging.
		"vendor/example.com/embedder/tmpl/page.html",
		"go.mod",
		"vendor/modules.txt",
	} {
		if _, err := os.Stat(filepath.Join(root, want)); err != nil {
			t.Errorf("expected %s in the staged module: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "vendor/example.com/app/internal/util/util.go")); err == nil {
		t.Error("a dependency under the module path must not be vendored")
	}

	gomod := readFile(t, filepath.Join(root, "go.mod"))
	if !strings.Contains(gomod, "module example.com/app") {
		t.Errorf("go.mod is missing the module path:\n%s", gomod)
	}
	if !strings.Contains(gomod, "example.com/other v0.0.0") {
		t.Errorf("go.mod is missing the vendored requirement:\n%s", gomod)
	}
	if strings.Contains(gomod, "example.com/app/internal/util") {
		t.Errorf("a nested dependency must not become a requirement:\n%s", gomod)
	}

	// The go command rejects a vendor directory whose modules.txt disagrees
	// with go.mod, so every requirement needs a matching explicit entry.
	modules := readFile(t, filepath.Join(root, "vendor/modules.txt"))
	want := "# example.com/embedder v0.0.0\n## explicit; go 1.26\nexample.com/embedder\n" +
		"# example.com/other v0.0.0\n## explicit; go 1.26\nexample.com/other\n"
	if modules != want {
		t.Errorf("modules.txt = %q, want %q", modules, want)
	}

	if got := st.FileMap["app.go"]; got != "src/app/app.go" {
		t.Errorf("file map for app.go = %q, want the workspace path", got)
	}
	if _, ok := st.FileMap["vendor/example.com/other/other.go"]; ok {
		t.Error("dependency sources have no workspace path and must not be mapped")
	}
}

func TestStageModuleWithoutDeps(t *testing.T) {
	in := t.TempDir()
	m := &Manifest{
		ImportPath: "example.com/solo",
		GoVersion:  "1.26",
		Srcs:       []Source{{Path: writeSource(t, in, "solo.go", "package solo\n"), Name: "solo.go"}},
	}
	root := filepath.Join(t.TempDir(), "module")
	if _, err := stageModule(m, root); err != nil {
		t.Fatal(err)
	}
	// An empty vendor directory would make the go command complain about a
	// missing modules.txt, so it must not be created at all.
	if _, err := os.Stat(filepath.Join(root, "vendor")); !os.IsNotExist(err) {
		t.Error("no dependencies should mean no vendor directory")
	}
	if gomod := readFile(t, filepath.Join(root, "go.mod")); strings.Contains(gomod, "require") {
		t.Errorf("go.mod should have no requirements:\n%s", gomod)
	}
}

func TestNestedUnder(t *testing.T) {
	cases := []struct {
		module, dep string
		wantDir     string
		wantOK      bool
	}{
		{"example.com/a", "example.com/a/b", "b", true},
		{"example.com/a", "example.com/a/b/c", filepath.Join("b", "c"), true},
		{"example.com/a", "example.com/ab", "", false},
		{"example.com/a", "example.com/a", "", false},
		{"example.com/a", "other.com/a/b", "", false},
	}
	for _, tc := range cases {
		dir, ok := nestedUnder(tc.module, tc.dep)
		if ok != tc.wantOK || dir != tc.wantDir {
			t.Errorf("nestedUnder(%q, %q) = (%q, %v), want (%q, %v)",
				tc.module, tc.dep, dir, ok, tc.wantDir, tc.wantOK)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestSyntheticVersionMatchesMajorSuffix(t *testing.T) {
	// The go command rejects a requirement whose major version disagrees with
	// the major version suffix in the module path.
	cases := map[string]string{
		"example.com/plain":           "v0.0.0",
		"gopkg.in/yaml.v3":            "v3.0.0",
		"gopkg.in/check.v1":           "v1.0.0",
		"github.com/foo/bar/v2":       "v2.0.0",
		"github.com/foo/bar/v12":      "v12.0.0",
		"github.com/foo/bar/v2/inner": "v0.0.0",
		"example.com/version":         "v0.0.0",
		"example.com/v0":              "v0.0.0",
	}
	for path, want := range cases {
		if got := syntheticVersion(path); got != want {
			t.Errorf("syntheticVersion(%q) = %q, want %q", path, got, want)
		}
	}
}
