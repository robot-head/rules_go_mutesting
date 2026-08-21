package main

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ChangedLines is the diff spec produced by the :changed runner: workspace
// relative file path to inclusive [start, end] line ranges.
type ChangedLines struct {
	Files map[string][][2]int `json:"files"`
}

// parseChangedLines reads the spec the :changed runner passes as a build
// setting. An empty value means the run is not restricted to changed lines.
func parseChangedLines(spec string) (*ChangedLines, error) {
	if len(strings.TrimSpace(spec)) == 0 {
		return nil, nil
	}
	var c ChangedLines
	if err := json.Unmarshal([]byte(spec), &c); err != nil {
		return nil, fmt.Errorf("parsing changed-lines spec: %w", err)
	}
	if len(c.Files) == 0 {
		return nil, nil
	}
	return &c, nil
}

func (c *ChangedLines) covers(workspacePath string, lines []int) bool {
	ranges, ok := c.Files[workspacePath]
	if !ok {
		return false
	}
	for _, ln := range lines {
		for _, r := range ranges {
			if ln >= r[0] && ln <= r[1] {
				return true
			}
		}
	}
	return false
}

// mutant is one generated mutation found in the tool's scratch directory.
type mutant struct {
	// Checksum is the tool's stable identifier for the mutation.
	Checksum string
	// Workspace is the source-tree path of the file it mutates, when known.
	Workspace string
	// Lines are the 1-based line numbers it alters in the original file.
	Lines []int
}

// stableMutationKey reproduces go-mutesting's mutation identifier: an MD5 over
// only the lines that differ between the formatted original and the mutant.
//
// Because the key covers content and not file names or offsets, it is
// identical whenever the same textual mutation is produced, which is what lets
// a checksum computed here select mutations in a later run of the tool. This
// mirrors stableMutationKey in the tool's internal/engine package; it is
// covered by a test that runs the real tool.
func stableMutationKey(original, mutated []byte) (string, []int) {
	h := md5.New()
	oLines := strings.Split(string(original), "\n")
	mLines := strings.Split(string(mutated), "\n")
	n := len(oLines)
	if len(mLines) > n {
		n = len(mLines)
	}
	var changed []int
	for i := 0; i < n; i++ {
		var o, m string
		if i < len(oLines) {
			o = oLines[i]
		}
		if i < len(mLines) {
			m = mLines[i]
		}
		if o != m {
			fmt.Fprintf(h, "-%s\n+%s\n", o, m)
			changed = append(changed, i+1)
		}
	}
	return fmt.Sprintf("%x", h.Sum(nil)), changed
}

// collectMutants walks the scratch directory left behind by a generate-only
// run. The tool writes each mutation next to a copy of the formatted original,
// as <file>.original and <file>.<n>.
func collectMutants(scratch string, fileMap map[string]string) ([]mutant, error) {
	var out []mutant
	err := filepath.WalkDir(scratch, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".original") {
			return nil
		}
		original, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		base := strings.TrimSuffix(path, ".original")
		rel, err := filepath.Rel(scratch, base)
		if err != nil {
			return err
		}
		matches, err := filepath.Glob(base + ".*")
		if err != nil {
			return err
		}
		sort.Strings(matches)
		for _, mp := range matches {
			if strings.HasSuffix(mp, ".original") {
				continue
			}
			mutated, err := os.ReadFile(mp)
			if err != nil {
				return err
			}
			sum, lines := stableMutationKey(original, mutated)
			out = append(out, mutant{
				Checksum:  sum,
				Workspace: fileMap[filepath.ToSlash(rel)],
				Lines:     lines,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// exclusionsForChangedLines returns the checksums to skip so that only
// mutations touching changed lines run, plus the number that remain.
//
// A mutation's checksum depends only on the text it changes, so the same
// checksum can arise from two places at once: one on a changed line and one
// elsewhere. The tool generates only the first of any such pair, so excluding
// a checksum seen out of scope would also drop its in-scope twin. Subtracting
// the in-scope set keeps those.
func exclusionsForChangedLines(mutants []mutant, changed *ChangedLines) (exclude []string, kept int) {
	inScope := map[string]bool{}
	for _, mu := range mutants {
		if mu.Workspace != "" && changed.covers(mu.Workspace, mu.Lines) {
			inScope[mu.Checksum] = true
		}
	}
	seen := map[string]bool{}
	for _, mu := range mutants {
		if inScope[mu.Checksum] || seen[mu.Checksum] {
			continue
		}
		seen[mu.Checksum] = true
		exclude = append(exclude, mu.Checksum)
	}
	sort.Strings(exclude)
	return exclude, len(inScope)
}

func writeExclusions(path string, checksums []string) error {
	var b strings.Builder
	for _, c := range checksums {
		b.WriteString(c)
		b.WriteString("\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
