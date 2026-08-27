// Package evidence extracts and validates compact evidence references.
package evidence

import (
	"regexp"
	"strconv"
	"strings"
)

// Line is one numbered evidence item.
type Line struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Text   string `json:"text"`
}

// Summary contains evidence extracted from a larger artifact.
type Summary struct {
	EvidenceLines []Line `json:"evidence_lines"`
	Truncated     bool   `json:"truncated"`
}

// Validation reports whether analysis is grounded in known evidence ids.
type Validation struct {
	Valid      bool     `json:"valid"`
	Status     string   `json:"status"`
	References []string `json:"references"`
	Problems   []string `json:"problems,omitempty"`
}

var evidenceRefPattern = regexp.MustCompile(`\[(E[0-9]+)\]|\b(E[0-9]+)\b`)

// Select returns a bounded set of relevant evidence lines.
func Select(lines []string, limit int) []Line {
	selected, _ := SelectDistilled(lines, limit)
	return selected
}

// SelectDistilled returns evidence lines and reports whether they distil
// anything.
//
// When some line reports a failure, the result is a real selection. When none
// does, there is nothing to distil and the fallback is the tail of the output —
// the same lines the caller usually already holds. Only the code that does the
// matching can tell those apart, so it says which happened here. A caller
// cannot recover this downstream by comparing text: that cannot tell a failure
// line which happens to sit in the tail from a copy of the tail.
func SelectDistilled(lines []string, limit int) ([]Line, bool) {
	if limit <= 0 {
		limit = 30
	}
	keywords := []string{"error", "failed", "failure", "panic", "fatal", "exception", "denied", "timeout", "traceback", "undefined", "cannot", "no such file"}
	selected := make([]string, 0, limit)
	seen := map[string]struct{}{}
	for _, line := range lines {
		lower := strings.ToLower(line)
		matched := false
		for _, keyword := range keywords {
			if strings.Contains(lower, keyword) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		selected = append(selected, trimmed)
		if len(selected) >= limit {
			break
		}
	}
	distilled := len(selected) > 0
	if !distilled {
		for _, line := range tail(lines, limit) {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				selected = append(selected, trimmed)
			}
		}
	}
	out := make([]Line, 0, len(selected))
	for i, line := range selected {
		out = append(out, Line{ID: "E" + strconv.Itoa(i+1), Source: "command_output", Text: line})
	}
	return out, distilled
}

// ValidateLinks checks that analysis only cites evidence ids present in summary.
func ValidateLinks(summary Summary, analysis string, requireEvidence bool) Validation {
	known := make(map[string]struct{}, len(summary.EvidenceLines))
	for _, line := range summary.EvidenceLines {
		known[line.ID] = struct{}{}
	}
	matches := evidenceRefPattern.FindAllStringSubmatch(analysis, -1)
	refs := make([]string, 0, len(matches))
	problems := []string{}
	seen := map[string]struct{}{}
	for _, match := range matches {
		ref := match[1]
		if ref == "" {
			ref = match[2]
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
		if _, ok := known[ref]; !ok {
			problems = append(problems, "unknown evidence reference "+ref)
		}
	}
	if requireEvidence && strings.TrimSpace(analysis) != "" && len(refs) == 0 {
		problems = append(problems, "analysis has no evidence references")
	}
	valid := len(problems) == 0
	status := "ok"
	if !valid {
		status = "INSUFFICIENT_DATA"
	}
	return Validation{Valid: valid, Status: status, References: refs, Problems: problems}
}

func tail(values []string, limit int) []string {
	if len(values) <= limit {
		return values
	}
	return values[len(values)-limit:]
}
