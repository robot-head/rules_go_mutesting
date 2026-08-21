package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// stageRunfiles rebuilds the target's runfiles tree next to the staged module
// and returns its root, or "" when the target has no runfiles.
//
// A go_test that reads a data dependency finds it through the runfiles
// libraries, which resolve paths against a directory whose layout Bazel
// normally produces for the test action. The staged module is not that action,
// so without this a test that opens a fixture -- or, as with an embedded
// database binary, execs one -- fails for every mutant and reports a perfect
// score. The files are symlinked: they are read-only inputs, and a runfiles
// tree can be far larger than the sources.
func stageRunfiles(m *Manifest, root string) (string, error) {
	if len(m.Runfiles) == 0 {
		return "", nil
	}
	for _, s := range append([]Source{{Path: m.RepoMapping, Name: "_repo_mapping"}}, m.Runfiles...) {
		if s.Path == "" || s.Name == "" {
			continue
		}
		dest := filepath.Join(root, filepath.FromSlash(s.Name))
		if !strings.HasPrefix(dest, root+string(os.PathSeparator)) {
			return "", fmt.Errorf("runfile %q escapes the runfiles root", s.Name)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", err
		}
		if err := os.Symlink(s.Path, dest); err != nil && !os.IsExist(err) {
			return "", err
		}
	}
	return root, nil
}
