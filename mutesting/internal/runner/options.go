package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Settings holds the resolved value of every build setting. The aspect writes
// this as JSON; the tables below are the single place that knows how each one
// reaches go-mutesting, either as a command line flag or as a key in the
// generated YAML config.
type Settings struct {
	Bools   map[string]bool     `json:"bools"`
	Strings map[string]string   `json:"strings"`
	Ints    map[string]int      `json:"ints"`
	Lists   map[string][]string `json:"lists"`
	// Files maps a canonical name to a path. A zero-length file means the
	// option is not set; the aspect always passes a sentinel so that action
	// inputs stay unconditional.
	Files map[string]string `json:"files"`
}

// boolFlags maps a boolean setting to the tool flag it turns on.
var boolFlags = map[string]string{
	"debug":                        "--debug",
	"verbose":                      "--verbose",
	"quiet":                        "--quiet",
	"no_diffs":                     "--no-diffs",
	"noop":                         "--noop",
	"dry_run":                      "--dry-run",
	"keep_tmp":                     "--do-not-remove-tmp-folder",
	"html_output":                  "--html-output",
	"no_exec":                      "--no-exec",
	"coverage":                     "--coverage",
	"per_test":                     "--per-test",
	"test_recursive":               "--test-recursive",
	"git_diff_lines":               "--git-diff-lines",
	"logger_github":                "--logger-github",
	"logger_gitlab":                "--logger-gitlab",
	"logger_summary_json":          "--logger-summary-json",
	"logger_agentic_json":          "--logger-agentic-json",
	"ignore_msi_with_no_mutations": "--ignore-msi-with-no-mutations",
	"fail_on_escaped":              "--fail-on-escaped",
}

// stringFlags maps a string setting to its tool flag. Empty values are omitted
// so the tool keeps its own defaults.
var stringFlags = map[string]string{
	"output_statuses":     "--output-statuses",
	"match":               "--match",
	"run_mutant_id":       "--run-mutant-id",
	"test_flags":          "--test-flags",
	"timeout_coefficient": "--timeout-coefficient",
	"git_diff_base":       "--git-diff-base",
	"min_msi":             "--min-msi",
	"min_covered_msi":     "--min-covered-msi",
}

var intFlags = map[string]string{
	"workers":      "--workers",
	"exec_timeout": "--exec-timeout",
}

// fileFlags maps a file-valued setting to its tool flag. The config file is
// handled separately because the runner always generates one.
var fileFlags = map[string]string{
	"exclude_mutants": "--blacklist",
	"exec_script":     "--exec",
	"baseline":        "--baseline",
}

// configBools and configLists are options the tool only accepts through its
// YAML config, never on the command line.
var configBools = map[string]string{
	"skip_without_test":    "skip_without_test",
	"skip_with_build_tags": "skip_with_build_tags",
}

var configLists = map[string]string{
	"enable":              "enable_mutators",
	"exclude_dirs":        "exclude_dirs",
	"ignore_source_lines": "ignore_source_lines",
}

func loadSettings(path string) (*Settings, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Settings
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("parsing settings %s: %w", path, err)
	}
	return &s, nil
}

// file returns the path of a file-valued setting, or "" when it is unset.
// The aspect always passes a sentinel path so that the set of action inputs
// does not depend on flag values; a zero-length file is the "unset" marker.
//
// Every one of these paths is a declared action input, so a file that cannot
// be read is a wiring bug rather than an unset option. Treating it as unset
// would quietly downgrade a changed-lines run into a full one, so it fails
// instead. Call sites that cannot return an error use fileOrEmpty.
func (s *Settings) file(name string) (string, error) {
	p := s.Files[name]
	if p == "" {
		return "", nil
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("setting %s points at %s, which is not readable: %w", name, p, err)
	}
	if fi.Size() == 0 {
		return "", nil
	}
	return p, nil
}

// fileOrEmpty reports the path of a file-valued setting, treating an
// unreadable file as unset. Only used where the value has already been
// validated by an earlier call to file.
func (s *Settings) fileOrEmpty(name string) string {
	p, err := s.file(name)
	if err != nil {
		return ""
	}
	return p
}

// validateFiles checks every file-valued setting up front, so a wiring problem
// surfaces as a clear message instead of a surprising fallback.
func (s *Settings) validateFiles() error {
	for _, name := range sortedKeys(s.Files) {
		if _, err := s.file(name); err != nil {
			return err
		}
	}
	return nil
}

// writeConfig generates the YAML config for a run, merging the user's config
// (when provided) with the config-only settings.
//
// The tool writes report.json unconditionally only when no --config is given;
// once a config is in play the report becomes opt-in. Since the runner always
// passes a config, it always forces json_output on.
func (s *Settings) writeConfig(path string) error {
	cfg := map[string]any{}
	if user := s.fileOrEmpty("config"); user != "" {
		raw, err := os.ReadFile(user)
		if err != nil {
			return err
		}
		if err := yaml.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("parsing config %s: %w", user, err)
		}
		if cfg == nil {
			cfg = map[string]any{}
		}
	}
	for name, key := range configBools {
		if s.Bools[name] {
			cfg[key] = true
		}
	}
	for name, key := range configLists {
		if v := s.Lists[name]; len(v) > 0 {
			cfg[key] = v
		}
	}
	cfg["json_output"] = true

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// toolArgs builds the tool's command line. targets are the positional
// arguments naming what to mutate.
func (s *Settings) toolArgs(configPath string, targets []string) []string {
	var args []string

	for _, name := range sortedKeys(boolFlags) {
		if s.Bools[name] {
			args = append(args, boolFlags[name])
		}
	}
	for _, name := range sortedKeys(stringFlags) {
		if v := s.Strings[name]; v != "" {
			args = append(args, stringFlags[name]+"="+v)
		}
	}
	for _, name := range sortedKeys(intFlags) {
		// Zero means "tool default" for both integer options: workers falls
		// back to one per CPU, and a zero timeout would be meaningless.
		if v := s.Ints[name]; v != 0 {
			args = append(args, fmt.Sprintf("%s=%d", intFlags[name], v))
		}
	}
	for _, m := range s.Lists["disable"] {
		args = append(args, "--disable="+m)
	}
	for _, name := range sortedKeys(fileFlags) {
		if p := s.fileOrEmpty(name); p != "" {
			args = append(args, fileFlags[name]+"="+p)
		}
	}
	args = append(args, "--config="+configPath)
	args = append(args, s.Lists["extra_args"]...)
	return append(args, targets...)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// describe renders the effective options for the run log.
func (s *Settings) describe() string {
	var parts []string
	for _, name := range sortedKeys(s.Bools) {
		if s.Bools[name] {
			parts = append(parts, name)
		}
	}
	for _, name := range sortedKeys(s.Strings) {
		v := s.Strings[name]
		if v == "" {
			continue
		}
		if name == "changed_lines_spec" {
			// The spec is machine-generated JSON; the run summary reports how
			// many mutations it selected, which is the useful part.
			continue
		}
		parts = append(parts, name+"="+v)
	}
	for _, name := range sortedKeys(s.Lists) {
		if v := s.Lists[name]; len(v) > 0 {
			parts = append(parts, name+"="+strings.Join(v, ","))
		}
	}
	return strings.Join(parts, " ")
}
