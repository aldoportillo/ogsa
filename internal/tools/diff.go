package tools

import (
	"fmt"
	"strings"
)

const (
	diffContext  = 3
	maxDiffLines = 200
	maxLCSCells  = 1_000_000 // beyond this, show the changed region as remove-all/add-all
)

// unifiedDiff renders before→after as a single-hunk unified diff: the lines
// both share at the start and end are trimmed, and only the region between
// is compared.
func unifiedDiff(path, before, after string, isNew bool) string {
	a, b := splitLines(before), splitLines(after)
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	if pre == len(a) && pre == len(b) {
		return "(no changes)"
	}

	ctxStart := max(0, pre-diffContext)
	ctxEnd := min(len(a), len(a)-suf+diffContext) // in a's line numbers
	lead := a[ctxStart:pre]
	trail := a[len(a)-suf : ctxEnd]
	midA, midB := a[pre:len(a)-suf], b[pre:len(b)-suf]

	var lines []string
	for _, l := range lead {
		lines = append(lines, " "+l)
	}
	lines = append(lines, diffLines(midA, midB)...)
	for _, l := range trail {
		lines = append(lines, " "+l)
	}
	if len(lines) > maxDiffLines {
		lines = append(lines[:maxDiffLines], fmt.Sprintf("… (%d more lines)", len(lines)-maxDiffLines))
	}

	oldName := "a/" + path
	if isNew {
		oldName = "/dev/null"
	}
	oldCount := len(lead) + len(midA) + len(trail)
	newCount := len(lead) + len(midB) + len(trail)
	return fmt.Sprintf("--- %s\n+++ b/%s\n@@ -%d,%d +%d,%d @@\n%s",
		oldName, path, hunkStart(ctxStart, oldCount), oldCount, hunkStart(ctxStart, newCount), newCount,
		strings.Join(lines, "\n"))
}

// hunkStart follows the unified format: 1-based, or 0 for an empty side.
func hunkStart(offset, count int) int {
	if count == 0 {
		return offset
	}
	return offset + 1
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// diffLines compares two line slices with a longest-common-subsequence table.
func diffLines(a, b []string) []string {
	var out []string
	if len(a)*len(b) > maxLCSCells {
		for _, l := range a {
			out = append(out, "-"+l)
		}
		for _, l := range b {
			out = append(out, "+"+l)
		}
		return out
	}
	// lcs[i][j] = LCS length of a[i:] and b[j:]
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			out = append(out, " "+a[i])
			i, j = i+1, j+1
		case i < len(a) && (j == len(b) || lcs[i+1][j] >= lcs[i][j+1]):
			out = append(out, "-"+a[i]) // removals first, like git
			i++
		default:
			out = append(out, "+"+b[j])
			j++
		}
	}
	return out
}
