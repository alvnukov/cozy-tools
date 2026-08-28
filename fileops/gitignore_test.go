package fileops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeIgnoreTestFile(t *testing.T, dir string, rel string, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The matcher table pins gitignore semantics rule by rule: negation with
// last-match-wins, dir-only trailing slash, anchoring by leading or inner
// slash, basename rules at any depth, and the ** forms.
func TestParseIgnoreRulesMatching(t *testing.T) {
	rules := parseIgnoreRules(strings.Join([]string{
		"# comment line",
		"*.log",
		"!keep.log",
		"build/",
		"/top.go",
		"sub/x.go",
		"gen.md",
		"docs/**",
		"[tT]mp",
	}, "\n"))
	stack := ignoreStack{{path: ".", rules: rules}}
	cases := []struct {
		path string
		dir  bool
		want bool
	}{
		{"a.log", false, true},
		{"deep/a.log", false, true},
		{"keep.log", false, false},
		{"nested/keep.log", false, false},
		{"build", true, true},
		{"nested/build", true, true},
		{"build", false, false},
		{"build/x.go", false, false},
		{"top.go", false, true},
		{"sub/top.go", false, false},
		{"sub/x.go", false, true},
		{"x.go", false, false},
		{"gen.md", false, true},
		{"deep/gen.md", false, true},
		{"docs/a.md", false, true},
		{"docs/a/b.md", false, true},
		{"docs", true, false},
		{"Tmp", false, true},
		{"tmp", false, true},
	}
	for _, tc := range cases {
		if got := stack.ignores(tc.path, tc.dir); got != tc.want {
			t.Errorf("ignores(%q, dir=%v) = %v, want %v", tc.path, tc.dir, got, tc.want)
		}
	}
}

// The walk honours the cascade: ignored files vanish, !negation re-includes,
// an ignored directory is pruned whole, and no_ignore brings everything
// back.
func TestSearchFilesGitignoreCascade(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreTestFile(t, dir, ".gitignore", "*.log\n!keep.log\nbuild/\n")
	writeIgnoreTestFile(t, dir, "a.log", "needle\n")
	writeIgnoreTestFile(t, dir, "keep.log", "needle\n")
	writeIgnoreTestFile(t, dir, "build/out.go", "needle\n")
	writeIgnoreTestFile(t, dir, "src/main.go", "needle\n")

	res, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "needle", MaxMatches: 50})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 {
		t.Fatalf("cascade total = %d, want keep.log and src/main.go only: %#v", res.Total, res.Matches)
	}
	joined := strings.Join(res.Matches, "\n")
	if !strings.Contains(joined, "keep.log") || !strings.Contains(joined, "src/main.go") {
		t.Fatalf("cascade matches = %#v", res.Matches)
	}

	all, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "needle", NoIgnore: true, MaxMatches: 50})
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 4 {
		t.Fatalf("no_ignore total = %d, want all four files: %#v", all.Total, all.Matches)
	}
}

// A deeper ignore file overrides a shallower one, and .rgignore outranks
// .gitignore in the same directory — the two precedence rules of the
// cascade.
func TestSearchFilesGitignorePrecedence(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreTestFile(t, dir, ".gitignore", "*.gen\nsecret.md\n")
	writeIgnoreTestFile(t, dir, "sub/.gitignore", "!keep.gen\n")
	writeIgnoreTestFile(t, dir, ".rgignore", "!secret.md\n")
	writeIgnoreTestFile(t, dir, "sub/keep.gen", "needle\n")
	writeIgnoreTestFile(t, dir, "sub/drop.gen", "needle\n")
	writeIgnoreTestFile(t, dir, "secret.md", "needle\n")

	res, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "needle", MaxMatches: 50})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 2 {
		t.Fatalf("precedence total = %d, want keep.gen and secret.md: %#v", res.Total, res.Matches)
	}
	joined := strings.Join(res.Matches, "\n")
	if !strings.Contains(joined, "keep.gen") || !strings.Contains(joined, "secret.md") {
		t.Fatalf("precedence matches = %#v", res.Matches)
	}
}

// A walk scoped below the root still applies the root's ignore files, and a
// search naming one file bypasses the cascade the way `rg pattern file`
// does.
func TestSearchFilesGitignoreScopeAndBypass(t *testing.T) {
	dir := t.TempDir()
	writeIgnoreTestFile(t, dir, ".gitignore", "gen.md\n")
	writeIgnoreTestFile(t, dir, "docs/gen.md", "needle\n")
	writeIgnoreTestFile(t, dir, "docs/real.md", "needle\n")

	scoped, err := SearchFilesInRepoWithOptions(dir, "docs", SearchOptions{Pattern: "needle", MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if scoped.Total != 1 || !strings.Contains(scoped.Matches[0], "real.md") {
		t.Fatalf("scoped = %#v, want only docs/real.md", scoped)
	}

	named, err := SearchFilesInRepoWithOptions(dir, "docs/gen.md", SearchOptions{Pattern: "needle", MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if named.Total != 1 {
		t.Fatalf("named-file search = %#v, want the ignored file searched anyway", named)
	}
}
