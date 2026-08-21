package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Manifest is the contract between the aspect and this runner. The aspect
// resolves rules_go providers into plain paths so that no Go-toolchain
// knowledge has to live in Starlark.
type Manifest struct {
	// ImportPath of the package under test. Becomes the synthetic module path.
	ImportPath string `json:"importpath"`
	// GoVersion written into the generated go.mod files.
	GoVersion string `json:"go_version"`
	// Srcs are the sources of the package under test, including _test.go files.
	Srcs []Source `json:"srcs"`
	// EmbedSrcs are non-Go files referenced by //go:embed in the package.
	EmbedSrcs []Source `json:"embedsrcs"`
	// Deps are the transitive dependency packages.
	Deps []Dep `json:"deps"`
	// GoRoot is the directory of the rules_go SDK (contains bin/go).
	GoRoot string `json:"goroot"`
	// HasCgo reports whether the package or a dependency needs cgo.
	HasCgo bool `json:"has_cgo"`
	// Label of the target, for diagnostics.
	Label string `json:"label"`
}

// Source is one input file: where Bazel put it, and where it belongs in the
// synthetic module.
type Source struct {
	// Path is the Bazel exec-root-relative path of the file.
	Path string `json:"path"`
	// Name is the file's basename inside its package directory.
	Name string `json:"name"`
	// Workspace is the source-tree-relative path, empty for generated files.
	// The changed-lines filter matches against this.
	Workspace string `json:"workspace,omitempty"`
}

// Dep is one dependency package.
type Dep struct {
	ImportPath string   `json:"importpath"`
	Srcs       []Source `json:"srcs"`
}

func loadManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest %s: %w", path, err)
	}
	if m.ImportPath == "" {
		return nil, fmt.Errorf("manifest %s has no importpath", path)
	}
	if m.GoVersion == "" {
		m.GoVersion = "1.26"
	}
	return &m, nil
}

// absolutize rewrites every input path to an absolute one. The runner changes
// directory into the staged module before invoking the tool, so relative
// exec-root paths would otherwise stop resolving.
func (m *Manifest) absolutize(root string) error {
	abs := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(root, p)
	}
	for i := range m.Srcs {
		m.Srcs[i].Path = abs(m.Srcs[i].Path)
	}
	for i := range m.EmbedSrcs {
		m.EmbedSrcs[i].Path = abs(m.EmbedSrcs[i].Path)
	}
	for i := range m.Deps {
		for j := range m.Deps[i].Srcs {
			m.Deps[i].Srcs[j].Path = abs(m.Deps[i].Srcs[j].Path)
		}
	}
	m.GoRoot = abs(m.GoRoot)
	return nil
}
