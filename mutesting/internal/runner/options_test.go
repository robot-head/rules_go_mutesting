package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestToolArgsMapsEverySettingKind(t *testing.T) {
	s := &Settings{
		Bools:   map[string]bool{"verbose": true, "coverage": true, "quiet": false},
		Strings: map[string]string{"match": "^Add", "min_msi": "80", "test_flags": ""},
		Ints:    map[string]int{"workers": 4, "exec_timeout": 0},
		Lists: map[string][]string{
			"disable":    {"arithmetic/*", "branch/if"},
			"extra_args": {"--some-future-flag"},
		},
	}
	args := s.toolArgs("/tmp/config.yaml", []string{"."})
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"--verbose",
		"--coverage",
		"--match=^Add",
		"--min-msi=80",
		"--workers=4",
		"--disable=arithmetic/*",
		"--disable=branch/if",
		"--config=/tmp/config.yaml",
		"--some-future-flag",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in %v", want, args)
		}
	}
	for _, unwanted := range []string{"--quiet", "--test-flags", "--exec-timeout"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("unset option %s should be omitted: %v", unwanted, args)
		}
	}
	if args[len(args)-1] != "." {
		t.Errorf("positional target must come last, got %v", args)
	}
}

func TestToolArgsIsDeterministic(t *testing.T) {
	// Action command lines feed Bazel's cache key, so the same settings have
	// to produce byte-identical arguments across runs.
	s := &Settings{
		Bools:   map[string]bool{"verbose": true, "debug": true, "coverage": true},
		Strings: map[string]string{"match": "x", "min_msi": "1"},
	}
	first := strings.Join(s.toolArgs("c.yaml", []string{"."}), " ")
	for i := 0; i < 20; i++ {
		if got := strings.Join(s.toolArgs("c.yaml", []string{"."}), " "); got != first {
			t.Fatalf("argument order varies between calls:\n%s\n%s", first, got)
		}
	}
}

func TestWriteConfigForcesJSONReport(t *testing.T) {
	// The tool only writes report.json unconditionally when no config is
	// given. Because the runner always passes one, it has to opt back in.
	dir := t.TempDir()
	out := filepath.Join(dir, "config.yaml")
	s := &Settings{
		Bools: map[string]bool{"skip_without_test": true},
		Lists: map[string][]string{"enable": {"branch/if"}, "exclude_dirs": {"vendor/"}},
		Files: map[string]string{},
	}
	if err := s.writeConfig(out); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{}
	if err := yaml.Unmarshal([]byte(readFile(t, out)), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["json_output"] != true {
		t.Errorf("json_output = %v, want true", cfg["json_output"])
	}
	if cfg["skip_without_test"] != true {
		t.Errorf("skip_without_test = %v, want true", cfg["skip_without_test"])
	}
	if got, ok := cfg["enable_mutators"].([]any); !ok || len(got) != 1 || got[0] != "branch/if" {
		t.Errorf("enable_mutators = %v, want [branch/if]", cfg["enable_mutators"])
	}
}

func TestWriteConfigMergesUserConfig(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.yaml")
	if err := os.WriteFile(user, []byte("exclude_dirs:\n  - testdata/\nsilent_mode: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "config.yaml")
	s := &Settings{
		Bools: map[string]bool{"skip_with_build_tags": true},
		Files: map[string]string{"config": user},
	}
	if err := s.writeConfig(out); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{}
	if err := yaml.Unmarshal([]byte(readFile(t, out)), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["silent_mode"] != true {
		t.Error("keys from the user's config must survive the merge")
	}
	if cfg["skip_with_build_tags"] != true {
		t.Error("settings must be layered onto the user's config")
	}
	if cfg["json_output"] != true {
		t.Error("json_output must still be forced on")
	}
}

func TestFileSettingTreatsEmptyAsUnset(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	filled := filepath.Join(dir, "filled")
	if err := os.WriteFile(filled, []byte("data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Settings{Files: map[string]string{
		"baseline":        empty,
		"exclude_mutants": filled,
		"config":          "",
	}}
	if got := s.fileOrEmpty("baseline"); got != "" {
		t.Errorf("a zero-length sentinel means unset, got %q", got)
	}
	if got := s.fileOrEmpty("exclude_mutants"); got != filled {
		t.Errorf("file setting = %q, want %q", got, filled)
	}
	if got := s.fileOrEmpty("config"); got != "" {
		t.Errorf("missing entry should be unset, got %q", got)
	}

	// A sentinel must not reach the tool as a flag.
	args := strings.Join(s.toolArgs("c.yaml", []string{"."}), " ")
	if strings.Contains(args, "--baseline") {
		t.Errorf("sentinel leaked into the command line: %s", args)
	}
	if !strings.Contains(args, "--blacklist="+filled) {
		t.Errorf("real exclusion file missing from: %s", args)
	}
}

func TestReplaceFlag(t *testing.T) {
	args := []string{"--verbose", "--blacklist=/old", "."}
	got := strings.Join(replaceFlag(args, "--blacklist", "/new"), " ")
	if got != "--verbose --blacklist=/new ." {
		t.Errorf("replaceFlag = %q", got)
	}
	got = strings.Join(replaceFlag([]string{"--verbose", "."}, "--blacklist", "/new"), " ")
	if got != "--blacklist=/new --verbose ." {
		t.Errorf("replaceFlag on absent flag = %q", got)
	}
}
