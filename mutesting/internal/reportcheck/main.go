// Command reportcheck turns a go-mutesting report into a test result.
//
// The aspect can already fail a build when a score gate is not met, but that
// gate is a global flag. This command applies a per-target threshold, which is
// what a test rule needs, and prints the summary so the score is visible in
// test output.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

type report struct {
	Stats struct {
		Total      int     `json:"totalMutantsCount"`
		Killed     int     `json:"killedCount"`
		Escaped    int     `json:"escapedCount"`
		Errored    int     `json:"errorCount"`
		Skipped    int     `json:"skippedCount"`
		NotCovered int     `json:"notCoveredCount"`
		MSI        float64 `json:"msi"`
	} `json:"stats"`
	Escaped []struct {
		Mutator struct {
			Name string `json:"mutatorName"`
			File string `json:"originalFilePath"`
			Line int    `json:"originalStartLine"`
		} `json:"mutator"`
	} `json:"escaped"`
	Skip *struct {
		Reason string `json:"skipped"`
	} `json:"rules_go_mutesting,omitempty"`
}

func main() {
	reportPath := flag.String("report", "", "path to the mutation report")
	minMSI := flag.Float64("min-msi", -1, "minimum mutation score, 0-100; negative disables the check")
	label := flag.String("label", "", "target the report belongs to")
	flag.Parse()

	if err := run(*reportPath, *minMSI, *label); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run(path string, minMSI float64, label string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading report: %w", err)
	}
	var r report
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("parsing report %s: %w", path, err)
	}

	if r.Skip != nil && r.Skip.Reason != "" {
		fmt.Printf("%s: %s\n", label, r.Skip.Reason)
		return nil
	}

	// The report stores the score as a ratio; thresholds read as percentages.
	msi := r.Stats.MSI * 100
	fmt.Printf("%s: mutation score %.2f%% (%d killed, %d escaped, %d errored, %d skipped, %d total)\n",
		label, msi, r.Stats.Killed, r.Stats.Escaped, r.Stats.Errored, r.Stats.Skipped, r.Stats.Total)

	for _, e := range r.Escaped {
		fmt.Printf("  escaped: %s:%d (%s)\n", e.Mutator.File, e.Mutator.Line, e.Mutator.Name)
	}

	if minMSI < 0 {
		return nil
	}
	if r.Stats.Total == 0 {
		// No mutations means nothing to score; failing here would punish a
		// package for having no mutable code.
		fmt.Printf("%s: no mutations were generated; skipping the score check\n", label)
		return nil
	}
	if msi < minMSI {
		return fmt.Errorf("%s: mutation score %.2f%% is below the required %.2f%%", label, msi, minMSI)
	}
	return nil
}
