package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// session carries everything one mutation run needs.
type session struct {
	manifest *Manifest
	settings *Settings
	staged   *staged
	tool     string
	env      []string
	scratch  string
	config   string
	targets  []string
	outputs  outputs
}

// runAll mutates the whole selection in a single pass.
func (s *session) runAll() error {
	args := s.settings.toolArgs(s.config, s.targets)
	code, log, err := s.invoke(args)
	if err != nil {
		return err
	}
	return s.finish(code, log)
}

// runChangedLines restricts the run to mutations that fall on changed lines.
//
// go-mutesting has no line-range option, and its own --git-diff-lines mode
// needs a git repository, which a sandboxed action does not have. Instead the
// run happens in two passes: the first generates every mutation without
// running any tests, and the second runs only those that survive the line
// filter, using the tool's exclusion list to drop the rest. Generating
// mutations is an AST-level pass, so the cost of the extra phase is small
// next to the test executions it avoids.
func (s *session) runChangedLines(changed *ChangedLines) error {
	// Narrow the pass to files the diff actually touches, so unrelated files
	// in the package are never mutated at all.
	targets := s.changedTargets(changed)
	if len(targets) == 0 {
		return s.finishEmpty("no changed lines belong to this target")
	}

	// The generation pass has to run in the staged module like any other, so
	// its mutants are separated by pointing it at its own temporary directory
	// instead of its own working directory.
	generated := filepath.Join(s.scratch, "generated")
	if err := os.MkdirAll(generated, 0o755); err != nil {
		return err
	}
	genArgs := append([]string{"--no-exec", "--do-not-remove-tmp-folder"},
		withoutScoring(s.settings.toolArgs(s.config, targets))...)
	genLog, err := s.invokeIn(genArgs, s.staged.Root, "TMPDIR="+generated)
	if err != nil {
		return fmt.Errorf("generating mutations failed: %w\ncommand: %s %s\n%s",
			err, s.tool, strings.Join(genArgs, " "), genLog)
	}

	scratchDirs, err := filepath.Glob(filepath.Join(generated, "go-mutesting-*"))
	if err != nil {
		return err
	}
	var mutants []mutant
	for _, dir := range scratchDirs {
		found, err := collectMutants(dir, s.staged.FileMap)
		if err != nil {
			return err
		}
		mutants = append(mutants, found...)
	}
	if len(mutants) == 0 {
		return s.finishEmpty("no mutations were generated for the changed lines")
	}

	exclude, kept := exclusionsForChangedLines(mutants, changed)
	if kept == 0 {
		return s.finishEmpty(fmt.Sprintf(
			"none of the %d mutations in this package fall on changed lines", len(mutants)))
	}

	exclusions := filepath.Join(s.scratch, "excluded_mutants.txt")
	if err := writeExclusions(exclusions, exclude); err != nil {
		return err
	}

	args := s.settings.toolArgs(s.config, targets)
	if prior := s.settings.fileOrEmpty("exclude_mutants"); prior != "" {
		// Keep the user's own exclusions in force alongside ours.
		merged, err := mergeExclusionFiles(s.scratch, prior, exclusions)
		if err != nil {
			return err
		}
		args = replaceFlag(args, "--blacklist", merged)
	} else {
		args = append([]string{"--blacklist=" + exclusions}, args...)
	}

	code, log, err := s.invoke(args)
	if err != nil {
		return err
	}
	header := fmt.Sprintf("Changed-lines mode: %d of %d mutations fall on changed lines.\n",
		kept, len(mutants))
	return s.finish(code, append([]byte(genLogHeader(genLog)+header), log...))
}

// scoringFlags gate the tool on the mutation score.
var scoringFlags = []string{"--min-msi", "--min-covered-msi", "--fail-on-escaped"}

// withoutScoring drops the flags that gate on the score. The generation pass
// executes no tests, so its score is zero by construction and a threshold
// would fail it -- "MSI 0.00% is below minimum required" -- before the run
// that actually measures anything gets to start.
func withoutScoring(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		gating := false
		for _, f := range scoringFlags {
			if a == f || strings.HasPrefix(a, f+"=") {
				gating = true
				break
			}
		}
		if !gating {
			out = append(out, a)
		}
	}
	return out
}

func genLogHeader(genLog []byte) string {
	if len(bytes.TrimSpace(genLog)) == 0 {
		return ""
	}
	return "--- mutation generation pass ---\n" + string(genLog) + "--- mutation run ---\n"
}

// changedTargets returns the staged files of this package that the diff
// touches, as positional arguments for the tool.
func (s *session) changedTargets(changed *ChangedLines) []string {
	var targets []string
	for staged, workspace := range s.staged.FileMap {
		if !strings.HasSuffix(staged, ".go") || strings.HasSuffix(staged, "_test.go") {
			continue
		}
		if _, ok := changed.Files[workspace]; ok {
			targets = append(targets, "./"+staged)
		}
	}
	return targets
}

func mergeExclusionFiles(scratch string, paths ...string) (string, error) {
	var b bytes.Buffer
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		b.Write(raw)
		if len(raw) > 0 && !bytes.HasSuffix(raw, []byte("\n")) {
			b.WriteByte('\n')
		}
	}
	merged := filepath.Join(scratch, "merged_exclusions.txt")
	return merged, os.WriteFile(merged, b.Bytes(), 0o644)
}

func replaceFlag(args []string, flag, value string) []string {
	out := make([]string, 0, len(args)+1)
	replaced := false
	for _, a := range args {
		if strings.HasPrefix(a, flag+"=") {
			out = append(out, flag+"="+value)
			replaced = true
			continue
		}
		out = append(out, a)
	}
	if !replaced {
		out = append([]string{flag + "=" + value}, out...)
	}
	return out
}

// invoke runs the tool in the staged module directory.
func (s *session) invoke(args []string) (int, []byte, error) {
	log, err := s.invokeIn(args, s.staged.Root)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), log, nil
		}
		return 0, log, err
	}
	return exitOK, log, nil
}

// invokeIn runs the tool with a given working directory and optional
// environment overrides, which are appended so that they win over the defaults.
func (s *session) invokeIn(args []string, dir string, envOverrides ...string) ([]byte, error) {
	cmd := exec.Command(s.tool, args...)
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, s.env...), envOverrides...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

// finish relocates the tool's fixed-name reports onto the declared outputs and
// applies the exit policy.
func (s *session) finish(code int, log []byte) error {
	if err := s.writeLog(log); err != nil {
		return err
	}
	if err := s.collectReports(); err != nil {
		return err
	}

	switch code {
	case exitOK:
		return nil
	case exitThresholdBad:
		// The reports are already written, but Bazel discards the outputs of a
		// failing action, so the summary has to reach the user through stderr.
		os.Stderr.Write(summaryLines(log))
		return fmt.Errorf("%s: mutation score below the configured threshold", s.manifest.Label)
	case exitToolError:
		os.Stderr.Write(log)
		return fmt.Errorf("%s: go-mutesting failed", s.manifest.Label)
	default:
		os.Stderr.Write(log)
		return fmt.Errorf("%s: go-mutesting exited with code %d", s.manifest.Label, code)
	}
}

// finishEmpty records a run that correctly had nothing to measure. That is not
// a threshold failure: a target with no mutations in scope must not fail a
// build that gates on the mutation score.
func (s *session) finishEmpty(reason string) error {
	msg := fmt.Sprintf("%s: %s\n", s.manifest.Label, reason)
	if err := s.writeLog([]byte(msg)); err != nil {
		return err
	}
	if err := writeEmptyReport(s.outputs.report, reason); err != nil {
		return err
	}
	for _, p := range []string{s.outputs.html, s.outputs.summary, s.outputs.agentic, s.outputs.gitlab} {
		if p == "" {
			continue
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			return err
		}
	}
	fmt.Fprint(os.Stderr, msg)
	return nil
}

func (s *session) writeLog(log []byte) error {
	if s.outputs.log == "" {
		return nil
	}
	header := fmt.Sprintf("target: %s\noptions: %s\n\n", s.manifest.Label, s.settings.describe())
	return os.WriteFile(s.outputs.log, append([]byte(header), log...), 0o644)
}

// summaryLines extracts the score lines from a run log for stderr reporting.
func summaryLines(log []byte) []byte {
	var keep [][]byte
	for _, line := range bytes.Split(log, []byte("\n")) {
		if bytes.Contains(line, []byte("mutation score")) ||
			bytes.HasPrefix(line, []byte("ESCAPED")) ||
			bytes.Contains(line, []byte("is below minimum required")) {
			keep = append(keep, line)
		}
	}
	if len(keep) == 0 {
		return log
	}
	return append(bytes.Join(keep, []byte("\n")), '\n')
}

// reportFiles maps the tool's fixed output names to the declared outputs.
func (s *session) reportFiles() map[string]string {
	return map[string]string{
		"report.json":               s.outputs.report,
		"go-mutesting-report.html":  s.outputs.html,
		"go-mutesting-summary.json": s.outputs.summary,
		"go-mutesting-agentic.json": s.outputs.agentic,
		"go-mutesting-gitlab.json":  s.outputs.gitlab,
	}
}

func (s *session) collectReports() error {
	for name, dest := range s.reportFiles() {
		if dest == "" {
			continue
		}
		src := filepath.Join(s.staged.Root, name)
		if _, err := os.Stat(src); err != nil {
			if name == "report.json" {
				return fmt.Errorf("go-mutesting produced no report.json; see %s", s.outputs.log)
			}
			// An optional report the run had no reason to write.
			if err := os.WriteFile(dest, nil, 0o644); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(src, dest); err != nil {
			return err
		}
	}
	return nil
}
