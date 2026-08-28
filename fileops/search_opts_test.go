package fileops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regex mode treats the pattern as a regular expression; the default stays a
// literal substring, so the same pattern text must answer differently per mode.
func TestSearchFilesRegexPattern(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("func main() {}\nfunc  spaced() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	byRegex, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: `func\s+spaced`, Regex: true, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if byRegex.Total != 1 || len(byRegex.Matches) != 1 || !strings.Contains(byRegex.Matches[0], "a.go:2:func  spaced()") {
		t.Fatalf("regex result = %#v, want the one regex match", byRegex)
	}

	asLiteral, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: `func\s+spaced`, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if asLiteral.Total != 0 {
		t.Fatalf("literal result = %#v, want zero matches for regex syntax read literally", asLiteral)
	}
}

// An unparsable regex must fail loudly: a silent empty answer from a typo'd
// pattern reads as "nothing matches anywhere in the repo".
func TestSearchFilesInvalidRegex(t *testing.T) {
	dir := t.TempDir()
	if _, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: `func(`, Regex: true, MaxMatches: 10}); err == nil {
		t.Fatal("expected an error for an invalid regex pattern")
	}
}

// smart_case matches case-insensitively while the pattern is all-lowercase and
// case-sensitively once it carries an uppercase letter (rg -S); ignore_case is
// case-insensitive regardless.
func TestSearchFilesCaseModes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("Hello helper\nhelper talks\nHELPER shouts\nHelper exact\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	smart, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "helper", SmartCase: true, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if smart.Total != 4 {
		t.Fatalf("smart_case total = %d, want all four case-tolerant lines", smart.Total)
	}

	smartUpper, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "Helper", SmartCase: true, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if smartUpper.Total != 1 || !strings.Contains(smartUpper.Matches[0], "notes.md:4:Helper exact") {
		t.Fatalf("smart_case with uppercase result = %#v, want only the exact-case line", smartUpper)
	}

	ignore, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "Helper", IgnoreCase: true, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if ignore.Total != 4 {
		t.Fatalf("ignore_case total = %d, want all four case variants", ignore.Total)
	}
}

// Glob narrows the walk to matching files; GlobExclude drops matches. A
// pattern without a separator matches the base name, one with a separator the
// repo-relative path.
func TestSearchFilesGlobFilters(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"a.go":          "needle here\n",
		"b.md":          "needle there\n",
		"pkg/c.go":      "needle deep\n",
		"pkg/notes.txt": "needle wider\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	goOnly, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "needle", Glob: []string{"*.go"}, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if goOnly.Total != 2 || !strings.Contains(strings.Join(goOnly.Matches, "\n"), "pkg/c.go") {
		t.Fatalf("glob *.go = %#v, want a.go and pkg/c.go only", goOnly)
	}

	byPath, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "needle", Glob: []string{"pkg/*.go"}, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if byPath.Total != 1 || !strings.Contains(byPath.Matches[0], "pkg/c.go:1:needle deep") {
		t.Fatalf("glob pkg/*.go = %#v, want only the deep file", byPath)
	}

	noMd, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "needle", GlobExclude: []string{"*.md"}, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if noMd.Total != 3 || strings.Contains(strings.Join(noMd.Matches, "\n"), "b.md") {
		t.Fatalf("glob_exclude *.md = %#v, want every file except b.md", noMd)
	}
}

// Context lines ride along with each match: matches keep ':' as the line
// separator, context lines use '-', and overlapping windows merge instead of
// repeating a line.
func TestSearchFilesContextLines(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "log.txt"), []byte("one\ntwo\nhit\nfour\nhit\nsix\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "hit", ContextBefore: 1, ContextAfter: 1, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 {
		t.Fatalf("total = %d, context must not add matches", result.Total)
	}
	want := []string{
		"log.txt-2:two",
		"log.txt:3:hit",
		"log.txt-4:four",
		"log.txt:5:hit",
		"log.txt-6:six",
	}
	if len(result.Matches) != len(want) {
		t.Fatalf("matches = %#v, want %d merged lines", result.Matches, len(want))
	}
	for i, wantLine := range want {
		if result.Matches[i] != wantLine {
			t.Fatalf("matches[%d] = %q, want %q", i, result.Matches[i], wantLine)
		}
	}
}

// files_only answers with the paths of files holding matches, not the lines —
// the cheap first pass when scouting a large tree.
func TestSearchFilesFilesOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("needle\nneedle again\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "two.txt"), []byte("nothing\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "needle", FilesOnly: true, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Matches) != 1 || result.Matches[0] != "one.txt" {
		t.Fatalf("files_only result = %#v, want exactly one.txt", result)
	}
}

// Invert flips the predicate per line (rg -v): every non-matching line is
// reported, matching ones are not.
func TestSearchFilesInvert(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mix.txt"), []byte("keep this\nskip me\nkeep that\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "skip", Invert: true, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 {
		t.Fatalf("invert total = %d, want the two non-matching lines", result.Total)
	}
	joined := strings.Join(result.Matches, "\n")
	if !strings.Contains(joined, "mix.txt:1:keep this") || strings.Contains(joined, "skip me") {
		t.Fatalf("invert matches = %#v, want non-matching lines only", result.Matches)
	}
}

// rg-style globs reach through directories: **/*.go must find a .go file at
// any depth, which path.Match alone cannot express.
func TestSearchFilesGlobstar(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "b", "deep.go"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shallow.md"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "needle", Glob: []string{"**/*.go"}, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || !strings.Contains(result.Matches[0], "a/b/deep.go") {
		t.Fatalf("globstar result = %#v, want the deep .go file", result)
	}
}

// Consecutive matching lines must each keep their ':' match marker: a merged
// context window may not relabel a matching line as '-' context.
func TestSearchFilesContextAdjacentHits(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "log.txt"), []byte("one\ntwo\nhit\nhit\nfive\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "hit", ContextAfter: 1, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(result.Matches, "\n")
	if result.Total != 2 || !strings.Contains(joined, "log.txt:3:hit") || !strings.Contains(joined, "log.txt:4:hit") {
		t.Fatalf("adjacent hits = %#v, want both hit lines marked with ':'", result)
	}
	if strings.Contains(joined, "log.txt-4:") {
		t.Fatalf("adjacent hits = %#v, line 4 must not be context", result)
	}
}

// Types fold into globs (rg -t/-T): an include keeps one language, an alias
// resolves, an exclude drops one, and an unknown name fails loudly instead
// of filtering everything out.
func TestSearchFileTypeFilters(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"a.go": "needle\n",
		"b.md": "needle\n",
		"c.py": "needle\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	goOnly, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "needle", Type: []string{"go"}, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if goOnly.Total != 1 || !strings.Contains(goOnly.Matches[0], "a.go") {
		t.Fatalf("type go = %#v, want a.go only", goOnly)
	}

	alias, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "needle", Type: []string{"python"}, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if alias.Total != 1 || !strings.Contains(alias.Matches[0], "c.py") {
		t.Fatalf("type python = %#v, want c.py via alias", alias)
	}

	noGo, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "needle", TypeNot: []string{"go"}, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if noGo.Total != 2 || strings.Contains(strings.Join(noGo.Matches, "\n"), "a.go") {
		t.Fatalf("type_not go = %#v, want b.md and c.py", noGo)
	}

	if _, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "needle", Type: []string{"nope"}, MaxMatches: 10}); err == nil {
		t.Fatal("expected an error for an unknown type name")
	}
}

// Binary files are sniffed by content — one NUL byte is enough — whatever
// the extension says.
func TestSearchFilesSkipsBinaryByContent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blob.txt"), []byte("needle\x00tail\nneedle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "needle", MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 || !strings.Contains(res.Matches[0], "ok.txt") {
		t.Fatalf("binary sniff = %#v, want ok.txt only", res)
	}
}

// word_match demands word boundaries and line_regexp a whole-line match —
// both keep working on a literal pattern.
func TestSearchFilesWordAndLineMatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "w.txt"), []byte("mainly\nmain\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	word, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "main", WordMatch: true, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if word.Total != 1 || !strings.Contains(word.Matches[0], "w.txt:2:main") {
		t.Fatalf("word_match = %#v, want only the bare main line", word)
	}

	line, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "main", LineRegexp: true, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if line.Total != 1 {
		t.Fatalf("line_regexp = %#v, want only the whole-line match", line)
	}
}

// count_only summarises per file as path:count; only_matching extracts each
// match's own text, and replace rewrites it through capture groups.
func TestSearchFilesCountOnlyAndExtraction(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("needle\nneedle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "two.txt"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	counts, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "needle", CountOnly: true, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(counts.Matches, "\n")
	if counts.Total != 2 || !strings.Contains(joined, "one.txt:2") || !strings.Contains(joined, "two.txt:1") {
		t.Fatalf("count_only = %#v", counts)
	}

	if err := os.WriteFile(filepath.Join(dir, "gap.txt"), []byte("AlphaGap tail\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	only, err := SearchFilesInRepoWithOptions(dir, "gap.txt", SearchOptions{Pattern: `(\w+)Gap`, Regex: true, OnlyMatching: true, MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if only.Total != 1 || only.Matches[0] != "gap.txt:1:AlphaGap" {
		t.Fatalf("only_matching = %#v, want gap.txt:1:AlphaGap", only)
	}

	replaced, err := SearchFilesInRepoWithOptions(dir, "gap.txt", SearchOptions{Pattern: `(\w+)Gap`, Regex: true, OnlyMatching: true, Replace: "$1", MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.Total != 1 || replaced.Matches[0] != "gap.txt:1:Alpha" {
		t.Fatalf("replace = %#v, want the capture group alone", replaced)
	}

	spanReplaced, err := SearchFilesInRepoWithOptions(dir, "gap.txt", SearchOptions{Pattern: `(\w+)Gap`, Regex: true, Replace: "[$1]", MaxMatches: 10})
	if err != nil {
		t.Fatal(err)
	}
	if spanReplaced.Total != 1 || spanReplaced.Matches[0] != "gap.txt:1:[Alpha] tail" {
		t.Fatalf("replace in line = %#v, want the matched span rewritten", spanReplaced)
	}

	if _, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "x", OnlyMatching: true, Invert: true, MaxMatches: 10}); err == nil {
		t.Fatal("expected an error for only_matching with invert")
	}
	if _, err := SearchFilesInRepoWithOptions(dir, "", SearchOptions{Pattern: "x", CountOnly: true, FilesOnly: true, MaxMatches: 10}); err == nil {
		t.Fatal("expected an error for count_only with files_only")
	}
}

// A search scoped to a path that does not exist must fail loudly and name
// the nearest directory that does, so the caller lists that instead of
// concluding nothing matches or hunting for another root cause.
func TestSearchFilesMissingPathTeaches(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src", "vega"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "vega", "real.txt"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := SearchFilesInRepoWithOptions(dir, "src/vega/platform-release/.helm/values", SearchOptions{Pattern: "needle", MaxMatches: 10})
	if err == nil {
		t.Fatal("expected an error for a missing search path")
	}
	msg := err.Error()
	for _, want := range []string{`src/vega/platform-release/.helm/values`, `"src/vega"`, "action=list"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q must mention %q", msg, want)
		}
	}

	if _, err := SearchFilesInRepoWithOptions(dir, "src/vega/real.txt", SearchOptions{Pattern: "needle", MaxMatches: 10}); err != nil {
		t.Fatalf("existing single-file search must keep working: %v", err)
	}
}
