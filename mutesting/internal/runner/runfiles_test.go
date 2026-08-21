package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStageRunfilesLayout(t *testing.T) {
	in := t.TempDir()
	m := &Manifest{
		RepoMapping: writeSource(t, in, "repo_mapping", ",postgres_binaries,postgres_binaries+\n"),
		Runfiles: []Source{
			{Path: writeSource(t, in, "fixture.json", "{}"), Name: "_main/storage/pgdb/testdata/fixture.json"},
			// An external repository sits beside the main workspace at the
			// runfiles root, not inside it.
			{Path: writeSource(t, in, "postgres.txz", "\x00"), Name: "postgres_binaries+/postgres.txz"},
		},
	}
	root := filepath.Join(t.TempDir(), "runfiles")
	got, err := stageRunfiles(m, root)
	if err != nil {
		t.Fatal(err)
	}
	if got != root {
		t.Errorf("stageRunfiles = %q, want %q", got, root)
	}
	for _, want := range []string{
		"_repo_mapping",
		"_main/storage/pgdb/testdata/fixture.json",
		"postgres_binaries+/postgres.txz",
	} {
		if _, err := os.Stat(filepath.Join(root, want)); err != nil {
			t.Errorf("expected %s in the runfiles tree: %v", want, err)
		}
	}
}

func TestStageRunfilesWithoutData(t *testing.T) {
	root := filepath.Join(t.TempDir(), "runfiles")
	got, err := stageRunfiles(&Manifest{}, root)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("stageRunfiles = %q, want no runfiles tree", got)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Error("a target without data must not get a runfiles directory")
	}
}
