"""Single source of truth for the go-mutesting option surface.

Every option the tool supports is declared here exactly once. `BUILD.bazel`
uses these tables to declare the build settings, and `aspect.bzl` uses them to
declare the matching private aspect attributes and to serialize resolved values
into the settings JSON that the runner consumes.

The mapping from a canonical setting name to the tool's command line flag or
YAML config key lives in the runner (Go), not here: it is logic worth unit
testing, and keeping it in one language avoids two half-authoritative tables.
"""

# Boolean options. (name, default, doc)
BOOL_SETTINGS = [
    ("debug", False, "Tool debug log output (implies verbose)."),
    ("verbose", False, "Tool verbose log output."),
    ("quiet", False, "Only print escaped mutants and the summary."),
    ("no_diffs", False, "Suppress diff output for all mutation results."),
    ("noop", False, "Run the test suite once without mutations first; fail if it does not pass."),
    ("dry_run", False, "Count mutations per file and mutator without running tests."),
    ("keep_tmp", False, "Keep the temporary folder holding generated mutants (debugging)."),
    ("html_output", False, "Produce an HTML report as an additional output."),
    ("no_exec", False, "Generate mutants but do not run any tests."),
    ("coverage", False, "Run coverage before mutating to compute covered-code MSI."),
    ("per_test", False, "Build a per-test coverage map and run only covering tests per mutant."),
    ("test_recursive", False, "Test recursively (appends /... to the package path)."),
    ("git_diff_lines", False, "Tool-native changed-line mode. Requires git and a repository in the action; use the :changed runner instead."),
    ("logger_github", False, "Emit escaped mutants as GitHub Actions warning annotations."),
    ("logger_gitlab", False, "Produce a GitLab Code Quality report as an additional output."),
    ("logger_summary_json", False, "Produce a compact stats-only JSON report as an additional output."),
    ("logger_agentic_json", False, "Produce the LLM-oriented escaped-mutant JSON as an additional output."),
    ("ignore_msi_with_no_mutations", False, "Pass MSI gates when no mutations were generated."),
    ("fail_on_escaped", False, "Fail if any mutant escapes, without setting an MSI threshold."),
    ("skip_without_test", False, "Skip source files that have no sibling _test.go file."),
    ("skip_with_build_tags", False, "Skip files whose test file carries build tags."),
]

# String options. (name, default, doc)
STRING_SETTINGS = [
    ("output_statuses", "", "Show only these result statuses: k=killed e=escaped s=skipped n=not-covered x=errored."),
    ("match", "", "Only mutate functions whose name matches this regex."),
    ("run_mutant_id", "", "Run only the mutant with this stable id."),
    ("test_flags", "", "Extra flags passed to each go test invocation. Ignored when :exec_script is set."),
    ("timeout_coefficient", "", "Per-mutation timeout as a multiple of the baseline test run time. Overrides :exec_timeout."),
    ("git_diff_base", "", "Git ref for the tool-native :git_diff_lines mode."),
    ("min_msi", "", "Minimum required mutation score, 0-100. Empty means no gate."),
    ("min_covered_msi", "", "Minimum required covered-code mutation score, 0-100. Requires :coverage. Empty means no gate."),
    # Carried as JSON text rather than a file so that no repository has to be
    # injected and consumers need no build setup: the :changed runner sets it,
    # and its value is small enough to live on a command line.
    ("changed_lines_spec", "", "JSON of changed line ranges, set by the :changed runner. Not intended to be written by hand."),
]

# Integer options. (name, default, doc)
INT_SETTINGS = [
    ("workers", 0, "Parallel mutation workers. 0 means one per CPU. Forced to 1 when :exec_script is set."),
    ("exec_timeout", 10, "Timeout in seconds for each mutant's test run."),
]

# Repeatable string options. (name, doc)
STRING_LIST_SETTINGS = [
    ("disable", "Mutators to disable, by name or trailing-* prefix pattern."),
    ("enable", "Mutators to enable; when non-empty acts as an allowlist. :disable applies on top."),
    ("exclude_dirs", "Path prefixes excluded from mutation."),
    ("ignore_source_lines", "Regexes; matching source lines are not mutated."),
    ("only_files", "Workspace-relative .go files to mutate. Empty means the whole package."),
    ("extra_args", "Escape hatch: extra arguments appended verbatim to the tool invocation."),
]

# File-valued options, declared as label_flag. (name, sentinel, doc)
LABEL_SETTINGS = [
    ("config", ":empty_config", "A go-mutesting YAML config file to use as the base."),
    ("exclude_mutants", ":empty_exclusions", "File of mutation checksums to skip, one per line (the tool's --blacklist)."),
    ("exec_script", ":empty_exec_script", "Executable run for every mutant instead of the built-in go test runner."),
    ("baseline", ":empty_baseline", "Baseline file of known-surviving mutants to tolerate."),
]

def all_setting_names():
    """Returns every canonical setting name, for cross-checking against the runner."""
    return (
        [n for n, _, _ in BOOL_SETTINGS] +
        [n for n, _, _ in STRING_SETTINGS] +
        [n for n, _, _ in INT_SETTINGS] +
        [n for n, _ in STRING_LIST_SETTINGS] +
        [n for n, _, _ in LABEL_SETTINGS]
    )

def setting_label(name):
    """Returns the label of the build setting for a canonical option name."""
    return Label("//mutesting:" + name)

def attr_name(name):
    """Returns the private aspect attribute name for a canonical option name."""
    return "_" + name
