// Command runner executes one go-mutesting run inside a Bazel action.
//
// It turns the aspect's manifest into a self-contained offline Go module,
// applies the resolved build settings, runs the mutation engine, and copies
// the engine's fixed-name reports to the outputs the aspect declared.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Exit codes used by go-mutesting itself.
const (
	exitOK           = 0
	exitToolError    = 3
	exitThresholdBad = 4
)

type outputs struct {
	report  string
	log     string
	html    string
	summary string
	agentic string
	gitlab  string
}

func main() {
	var (
		manifestPath = flag.String("manifest", "", "path to the aspect's manifest JSON")
		settingsPath = flag.String("settings", "", "path to the resolved build settings JSON")
		toolPath     = flag.String("tool", "", "path to the go-mutesting binary")
		out          outputs
	)
	flag.StringVar(&out.report, "report-out", "", "declared output for report.json")
	flag.StringVar(&out.log, "log-out", "", "declared output for the run log")
	flag.StringVar(&out.html, "html-out", "", "declared output for the HTML report")
	flag.StringVar(&out.summary, "summary-out", "", "declared output for the summary JSON")
	flag.StringVar(&out.agentic, "agentic-out", "", "declared output for the agentic JSON")
	flag.StringVar(&out.gitlab, "gitlab-out", "", "declared output for the GitLab report")
	flag.Parse()

	if err := run(*manifestPath, *settingsPath, *toolPath, out); err != nil {
		fmt.Fprintf(os.Stderr, "go_mutesting: %v\n", err)
		os.Exit(1)
	}
}

func run(manifestPath, settingsPath, toolPath string, out outputs) error {
	if manifestPath == "" || settingsPath == "" || toolPath == "" {
		return fmt.Errorf("--manifest, --settings and --tool are all required")
	}
	execRoot, err := os.Getwd()
	if err != nil {
		return err
	}

	m, err := loadManifest(manifestPath)
	if err != nil {
		return err
	}
	settings, err := loadSettings(settingsPath)
	if err != nil {
		return err
	}
	for _, p := range []*string{&toolPath, &out.report, &out.log, &out.html, &out.summary, &out.agentic, &out.gitlab} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(execRoot, *p)
		}
	}
	if err := m.absolutize(execRoot); err != nil {
		return err
	}
	for name, p := range settings.Files {
		if p != "" && !filepath.IsAbs(p) {
			settings.Files[name] = filepath.Join(execRoot, p)
		}
	}

	if m.HasCgo {
		// Mutating a cgo package would need the staged module to reproduce the
		// C toolchain setup rules_go arranges; report the skip rather than
		// failing a repo-wide sweep.
		return writeSkipped(out, m, "package uses cgo, which mutation testing does not support")
	}

	scratch, err := os.MkdirTemp("", "go-mutesting-run-")
	if err != nil {
		return err
	}
	if settings.Bools["keep_tmp"] {
		// Same option that keeps the engine's mutants around also keeps the
		// staged module, which is where staging problems are diagnosed.
		fmt.Fprintf(os.Stderr, "go_mutesting: keeping staged module at %s\n", scratch)
	} else {
		defer os.RemoveAll(scratch)
	}

	st, err := stageModule(m, filepath.Join(scratch, "module"))
	if err != nil {
		return fmt.Errorf("staging module: %w", err)
	}

	env, err := buildEnv(m, scratch)
	if err != nil {
		return err
	}

	if reason := compileStaged(m, env, st.Root); reason != "" {
		return writeSkipped(out, m, reason)
	}

	configPath := filepath.Join(scratch, "config.yaml")
	if err := settings.writeConfig(configPath); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	if err := settings.validateFiles(); err != nil {
		return err
	}
	changed, err := parseChangedLines(settings.Strings["changed_lines_spec"])
	if err != nil {
		return err
	}

	targets, err := mutationTargets(settings, st)
	if errors.Is(err, errNoMatchingFiles) {
		return writeSkipped(out, m, "no file selected by only_files belongs to this target")
	} else if err != nil {
		return err
	}

	session := &session{
		manifest: m,
		settings: settings,
		staged:   st,
		tool:     toolPath,
		env:      env,
		scratch:  scratch,
		config:   configPath,
		targets:  targets,
		outputs:  out,
	}
	if changed != nil {
		return session.runChangedLines(changed)
	}
	return session.runAll()
}

// mutationTargets returns the positional arguments naming what to mutate:
// the whole package by default, or the subset named by the only_files setting.
func mutationTargets(s *Settings, st *staged) ([]string, error) {
	only := s.Lists["only_files"]
	if len(only) == 0 {
		return []string{"."}, nil
	}
	byWorkspace := map[string]string{}
	for staged, workspace := range st.FileMap {
		byWorkspace[workspace] = staged
	}
	var targets []string
	for _, f := range only {
		staged, ok := byWorkspace[f]
		if !ok {
			// Not part of this target; another target owns it.
			continue
		}
		targets = append(targets, "./"+staged)
	}
	if len(targets) == 0 {
		return nil, errNoMatchingFiles
	}
	return targets, nil
}

// errNoMatchingFiles signals that only_files selected nothing in this target,
// which is a successful no-op rather than a failure.
var errNoMatchingFiles = fmt.Errorf("no requested file belongs to this target")

// compileStaged builds the package's test binary in the staged module,
// returning the reason to skip the run when it does not build.
//
// Mutation testing counts a mutant that fails to compile as killed, on the
// grounds that the compiler caught it. A staged module that does not compile
// at all therefore reports every mutant killed and a flawless score, which is
// the worst possible failure mode for a tool whose whole output is a number.
// One compile up front turns that into a reported skip: a dependency needing
// a C toolchain the sandbox does not have, a source file the aspect failed to
// stage, or tests that no longer build all land here.
func compileStaged(m *Manifest, env []string, root string) string {
	cmd := exec.Command(filepath.Join(m.GoRoot, "bin", "go"), "test", "-c", "-o", os.DevNull, ".")
	cmd.Dir = root
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil {
		return ""
	}
	return fmt.Sprintf("the staged module does not compile:\n%s", out)
}

func writeSkipped(out outputs, m *Manifest, reason string) error {
	msg := fmt.Sprintf("%s: skipped: %s\n", m.Label, reason)
	if out.log != "" {
		if err := os.WriteFile(out.log, []byte(msg), 0o644); err != nil {
			return err
		}
	}
	if err := writeEmptyReport(out.report, reason); err != nil {
		return err
	}
	for _, p := range []string{out.html, out.summary, out.agentic, out.gitlab} {
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

// writeEmptyReport produces a report shaped like the tool's own, for runs that
// legitimately had nothing to do.
func writeEmptyReport(path, reason string) error {
	if path == "" {
		return nil
	}
	body := fmt.Sprintf(`{
  "stats": {
    "totalMutantsCount": 0,
    "killedCount": 0,
    "notCoveredCount": 0,
    "escapedCount": 0,
    "errorCount": 0,
    "skippedCount": 0,
    "msi": 0,
    "coveredCodeMsi": 0
  },
  "mutatorStats": [],
  "escaped": [],
  "killed": [],
  "skipped": [],
  "errored": [],
  "notCovered": [],
  "rules_go_mutesting": {"skipped": %q}
}
`, reason)
	return os.WriteFile(path, []byte(body), 0o644)
}

// goflagsXDefs renders x_defs as a GOFLAGS fragment, empty when there are
// none. GOFLAGS rather than a tool option because every go command the engine
// runs has to link the same values, and the go command splits GOFLAGS with
// shell-like quoting, which is what lets one -ldflags carry several -X.
//
// A value containing a quote or a newline is dropped: it cannot survive that
// splitting, and no rules_go target stamps one today.
func goflagsXDefs(xDefs map[string]string) string {
	var defs []string
	for _, k := range sortedKeys(xDefs) {
		if v := xDefs[k]; !strings.ContainsAny(v, "'\"\n") {
			defs = append(defs, fmt.Sprintf("-X %s=%s", k, v))
		}
	}
	if len(defs) == 0 {
		return ""
	}
	return " '-ldflags=" + strings.Join(defs, " ") + "'"
}

// buildEnv assembles the environment for the mutation engine. The engine
// shells out to the go command, so the staged module has to look like an
// ordinary module with no network available.
func buildEnv(m *Manifest, scratch string) ([]string, error) {
	goroot := m.GoRoot
	if goroot == "" {
		return nil, fmt.Errorf("manifest has no goroot")
	}
	home := filepath.Join(scratch, "home")
	gopath := filepath.Join(scratch, "gopath")
	gocache := filepath.Join(scratch, "gocache")
	gotmp := filepath.Join(scratch, "gotmp")
	for _, d := range []string{home, gopath, gocache, gotmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}

	// The engine runs `diff` for its patch output, so keep the system paths
	// on PATH alongside the SDK.
	path := strings.Join([]string{filepath.Join(goroot, "bin"), "/usr/bin", "/bin"}, string(os.PathListSeparator))

	runfiles, err := stageRunfiles(m, filepath.Join(scratch, "runfiles"))
	if err != nil {
		return nil, fmt.Errorf("staging runfiles: %w", err)
	}

	env := []string{
		"GOROOT=" + goroot,
		"HOME=" + home,
		"GOPATH=" + gopath,
		"GOCACHE=" + gocache,
		"GOTMPDIR=" + gotmp,
		"TMPDIR=" + gotmp,
		"PATH=" + path,
		"GOFLAGS=-mod=vendor" + goflagsXDefs(m.XDefs),
		"GOPROXY=off",
		"GO111MODULE=on",
		"CGO_ENABLED=0",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
	}
	if runfiles != "" {
		env = append(env, "RUNFILES_DIR="+runfiles)
	}
	return env, nil
}
