package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// staged describes the synthetic Go module built for one mutation run.
type staged struct {
	// Root is the module directory; the package under test sits at its top.
	Root string
	// FileMap maps a path inside the module to the workspace-relative path of
	// the source it came from. The changed-lines filter needs this to line up
	// mutants with the diff.
	FileMap map[string]string
}

// stageModule materializes an offline, self-contained Go module: the package
// under test at the module root, and every dependency either nested under the
// module (when its import path lives beneath the module path) or vendored as
// its own synthetic module.
//
// Nesting matters: Go resolves an import that starts with the main module's
// path from within the main module, never from vendor/, so a dependency like
// example.com/foo/internal/util must be placed at internal/util rather than
// vendor/example.com/foo/internal/util.
func stageModule(m *Manifest, root string) (*staged, error) {
	st := &staged{Root: root, FileMap: map[string]string{}}

	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}

	// The package under test.
	for _, s := range m.Srcs {
		if err := st.place(s, "."); err != nil {
			return nil, err
		}
	}
	for _, s := range m.EmbedSrcs {
		if err := st.place(s, "."); err != nil {
			return nil, err
		}
	}

	var vendored []string
	for _, d := range m.Deps {
		if d.ImportPath == "" || d.ImportPath == m.ImportPath {
			continue
		}
		if nested, ok := nestedUnder(m.ImportPath, d.ImportPath); ok {
			if err := st.placeAll(d, nested); err != nil {
				return nil, err
			}
			continue
		}
		if err := st.placeAll(d, filepath.Join("vendor", filepath.FromSlash(d.ImportPath))); err != nil {
			return nil, err
		}
		vendored = append(vendored, d.ImportPath)
	}
	sort.Strings(vendored)

	if err := st.writeGoMod(m, vendored); err != nil {
		return nil, err
	}
	if len(vendored) > 0 {
		if err := st.writeModulesTxt(m, vendored); err != nil {
			return nil, err
		}
	}
	return st, nil
}

// nestedUnder reports whether dep lives beneath the main module path, and if
// so returns its directory relative to the module root.
func nestedUnder(modulePath, dep string) (string, bool) {
	if !strings.HasPrefix(dep, modulePath+"/") {
		return "", false
	}
	return filepath.FromSlash(strings.TrimPrefix(dep, modulePath+"/")), true
}

// placeAll stages a dependency's sources and its embedded files together, in
// the directory the dependency occupies in the module.
func (st *staged) placeAll(d Dep, dir string) error {
	for _, s := range append(append([]Source{}, d.Srcs...), d.EmbedSrcs...) {
		if err := st.place(s, dir); err != nil {
			return err
		}
	}
	return nil
}

func (st *staged) place(s Source, dir string) error {
	name := s.Name
	if name == "" {
		name = filepath.Base(s.Path)
	}
	rel := filepath.Join(dir, name)
	dest := filepath.Join(st.Root, rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := copyFile(s.Path, dest); err != nil {
		return fmt.Errorf("staging %s: %w", s.Path, err)
	}
	if s.Workspace != "" {
		st.FileMap[filepath.ToSlash(rel)] = s.Workspace
	}
	return nil
}

// copyFile copies rather than symlinks: go-mutesting rewrites sources through
// a formatting pass and compares them byte for byte, and a copy keeps the
// staged tree independent of Bazel's read-only input tree.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func (st *staged) writeGoMod(m *Manifest, vendored []string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "module %s\n\ngo %s\n", m.ImportPath, m.GoVersion)
	if len(vendored) > 0 {
		b.WriteString("\nrequire (\n")
		for _, ip := range vendored {
			fmt.Fprintf(&b, "\t%s %s\n", ip, syntheticVersion(ip))
		}
		b.WriteString(")\n")
	}
	return os.WriteFile(filepath.Join(st.Root, "go.mod"), []byte(b.String()), 0o644)
}

// syntheticVersion is the version stamped on a vendored dependency. In vendor
// mode the go command never resolves or verifies these versions, but it does
// insist that a module path carrying a major version suffix be required at
// that same major version, so gopkg.in/yaml.v3 has to be v3.x.x rather than
// v0.0.0. Everything else can be v0.0.0.
func syntheticVersion(modulePath string) string {
	major := ""
	if i := strings.LastIndex(modulePath, "/"); i >= 0 {
		last := modulePath[i+1:]
		if strings.HasPrefix(modulePath, "gopkg.in/") {
			// gopkg.in spells the major version as a .vN suffix.
			if j := strings.LastIndex(last, ".v"); j >= 0 {
				major = last[j+2:]
			}
		} else if strings.HasPrefix(last, "v") {
			major = last[1:]
		}
	}
	if major == "" || !allDigits(major) || major == "0" {
		return "v0.0.0"
	}
	return "v" + major + ".0.0"
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func (st *staged) writeModulesTxt(m *Manifest, vendored []string) error {
	var b strings.Builder
	for _, ip := range vendored {
		fmt.Fprintf(&b, "# %s %s\n", ip, syntheticVersion(ip))
		fmt.Fprintf(&b, "## explicit; go %s\n", m.GoVersion)
		fmt.Fprintf(&b, "%s\n", ip)
	}
	path := filepath.Join(st.Root, "vendor", "modules.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
