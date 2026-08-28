package fileops

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ApplyGuardedReplace used to decide all of this inline, against text it had
// just read from a file. ReplaceUniqueSpan is that decision on its own, so an
// editor holding a document from elsewhere reaches the same one. These cases are
// what both callers now inherit.
func TestReplaceUniqueSpanDecidesWhatBothCallersInherit(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		text, old, replacement string
		wantStatus             string
		wantText               string
		wantChanged            bool
		wantReason             string
	}{
		"one occurrence is replaced": {
			text: "a b c", old: "b", replacement: "B",
			wantStatus: "ok", wantText: "a B c", wantChanged: true,
		},
		"a second occurrence leaves the target ambiguous": {
			text: "a b b", old: "b", replacement: "B",
			wantStatus: "conflict", wantReason: "old text is not unique",
		},
		"an edit already applied is success, not a miss": {
			text: "a B c", old: "b", replacement: "B",
			wantStatus: "ok", wantText: "a B c", wantReason: "desired text already present",
		},
		"replacing text with itself changes nothing": {
			text: "a b c", old: "b", replacement: "b",
			wantStatus: "ok", wantText: "a b c",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := ReplaceUniqueSpan(testCase.text, testCase.old, testCase.replacement)
			if got.Status != testCase.wantStatus {
				t.Fatalf("status = %q, want %q (%+v)", got.Status, testCase.wantStatus, got)
			}
			if got.Text != testCase.wantText {
				t.Errorf("text = %q, want %q", got.Text, testCase.wantText)
			}
			if got.Changed != testCase.wantChanged {
				t.Errorf("changed = %v, want %v", got.Changed, testCase.wantChanged)
			}
			if got.Reason != testCase.wantReason {
				t.Errorf("reason = %q, want %q", got.Reason, testCase.wantReason)
			}
		})
	}
}

// The near-miss tells a caller which of its assumptions about the document was
// wrong, which is the difference between a retry that can work and one that
// repeats the same guess. It is part of the answer, not decoration.
func TestReplaceUniqueSpanQuotesTheNearestMissWhenNothingMatches(t *testing.T) {
	t.Parallel()

	got := ReplaceUniqueSpan("the quick brown fox", "quick brwn", "slow")
	if got.Status != "conflict" {
		t.Fatalf("status = %q, want conflict", got.Status)
	}
	if !strings.Contains(got.Reason, "old text not found") {
		t.Errorf("reason = %q, want it to say the text was not found", got.Reason)
	}
	if !strings.Contains(got.Reason, "quick br") {
		t.Errorf("reason = %q, want it to quote how far the match got", got.Reason)
	}
}

func TestApplyGuardedReplaceWithBase64(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("line1\nline2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	oldB64 := base64.StdEncoding.EncodeToString([]byte("line1"))
	newB64 := base64.StdEncoding.EncodeToString([]byte("replaced"))
	result, err := ApplyGuardedReplace(ReplaceRequest{
		Path:         path,
		ExpectedHash: snapshot.Hash,
		OldB64:       oldB64,
		NewB64:       newB64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %+v, want ok changed", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "replaced\nline2\n" {
		t.Fatalf("file content = %q", string(data))
	}
}

func TestReadSnapshotReportsHashChunks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("chunked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.HashChunks) != 8 {
		t.Fatalf("hash_chunks = %#v, want eight groups", snapshot.HashChunks)
	}
	if joined := strings.Join(snapshot.HashChunks, ""); joined != snapshot.Hash {
		t.Fatalf("chunks join to %q, want the reported hash %q", joined, snapshot.Hash)
	}
}

// Chunked transport must behave exactly like the single-string digest: the
// happy path writes, a corrupted chunk conflicts, and a malformed chunk list
// is rejected as a transport error before the file is touched.
func TestApplyGuardedReplaceWithHashChunks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyGuardedReplace(ReplaceRequest{
		Path:               path,
		ExpectedHashChunks: snapshot.HashChunks,
		Old:                "alpha",
		New:                "gamma",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %+v, want ok changed", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "gamma\nbeta\n" {
		t.Fatalf("file content = %q", string(data))
	}
}

func TestApplyGuardedReplaceCorruptedChunkConflicts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadSnapshot(path)
	corrupted := append([]string(nil), snapshot.HashChunks...)
	// One chunk changed the way single-character transport corruption
	// changes a long string: the format still holds, the digest does not.
	corrupted[3] = "ffffffff"
	if corrupted[3] == snapshot.HashChunks[3] {
		t.Fatal("corruption collided with the real chunk; test is vacuous")
	}
	result, err := ApplyGuardedReplace(ReplaceRequest{
		Path:               path,
		ExpectedHashChunks: corrupted,
		Old:                "alpha",
		New:                "gamma",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "conflict" || result.Reason != "file hash changed after snapshot" {
		t.Fatalf("result = %+v, want the hash conflict", result)
	}
}

func TestApplyGuardedReplaceRejectsMalformedHashChunks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyGuardedReplace(ReplaceRequest{
		Path:               path,
		ExpectedHashChunks: []string{"0123456789abcdef"},
		Old:                "alpha",
		New:                "beta",
	}); err == nil {
		t.Fatal("malformed expected_hash_chunks accepted; want a transport error")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "alpha\n" {
		t.Fatalf("file changed despite malformed chunks: %q", string(data))
	}
}

func TestValidateReplaceEncodingRejectsMalformedHashChunks(t *testing.T) {
	if err := ValidateReplaceEncoding(ReplaceRequest{Path: "x.txt", Old: "a", New: "b", ExpectedHashChunks: []string{"zz"}}); err == nil {
		t.Fatal("expected a transport error for non-hex chunks")
	}
}

func TestApplyGuardedReplaceBase64BackslashText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	// Content with backslashes — difficult to pass through JSON escaping.
	original := []byte("pattern: \\s and \\d\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadSnapshot(path)
	// old text contains literal backslash-s backslash-d
	oldText := `\s and \d`
	newText := `\S and \D`
	result, err := ApplyGuardedReplace(ReplaceRequest{
		Path:         path,
		ExpectedHash: snapshot.Hash,
		OldB64:       base64.StdEncoding.EncodeToString([]byte(oldText)),
		NewB64:       base64.StdEncoding.EncodeToString([]byte(newText)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %+v, want ok changed. Reason: %s", result, result.Reason)
	}
	data, _ := os.ReadFile(path)
	expected := "pattern: \\S and \\D\n"
	if string(data) != expected {
		t.Fatalf("file content = %q, want %q", string(data), expected)
	}
}

func TestApplyGuardedReplaceBase64FallbackToOld(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadSnapshot(path)
	result, err := ApplyGuardedReplace(ReplaceRequest{
		Path:         path,
		ExpectedHash: snapshot.Hash,
		Old:          "hello",
		New:          "world",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %+v, want ok changed", result)
	}
}

func TestApplyGuardedReplaceInvalidBase64(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("data\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadSnapshot(path)
	_, err := ApplyGuardedReplace(ReplaceRequest{
		Path:         path,
		ExpectedHash: snapshot.Hash,
		OldB64:       "!!!not-base64!!!",
		NewB64:       base64.StdEncoding.EncodeToString([]byte("ok")),
	})
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestApplyGuardedReplaceDiagnosticsOnMiss(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadSnapshot(path)
	result, err := ApplyGuardedReplace(ReplaceRequest{
		Path:         path,
		ExpectedHash: snapshot.Hash,
		OldB64:       base64.StdEncoding.EncodeToString([]byte("func main() { println(\"hello\") }")),
		NewB64:       base64.StdEncoding.EncodeToString([]byte("func main() {}")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "conflict" {
		t.Fatalf("status = %q, want conflict", result.Status)
	}
	if !strings.Contains(result.Reason, "best partial match near:") {
		t.Fatalf("reason should contain diagnostic hint: %q", result.Reason)
	}
}

func TestApplyGuardedReplaceRejectsHashMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := ApplyGuardedReplace(ReplaceRequest{Path: path, ExpectedHash: "bad", Old: "one", New: "two"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "conflict" {
		t.Fatalf("status = %q, want conflict", result.Status)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "one\n" {
		t.Fatalf("file was modified: %q", string(data))
	}
}

func TestApplyGuardedReplaceIsIdempotentWhenDesiredTextPresent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.txt")
	if err := os.WriteFile(path, []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyGuardedReplace(ReplaceRequest{Path: path, ExpectedHash: snapshot.Hash, Old: "one", New: "two"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || result.Changed {
		t.Fatalf("result = %+v, want ok unchanged", result)
	}
}

func TestReadSnapshotInRepoRejectsEscapingPath(t *testing.T) {
	_, err := ReadSnapshotInRepo(t.TempDir(), "../x.txt")
	if err == nil {
		t.Fatal("expected repo escape error")
	}
}
func TestReadFileContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fc, err := ReadFileContent(path)
	if err != nil {
		t.Fatal(err)
	}
	if !fc.Exists {
		t.Fatal("file should exist")
	}
	if fc.Size == 0 {
		t.Fatal("size should be > 0")
	}
	if len(fc.Lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(fc.Lines))
	}
	if fc.Lines[0].Number != 1 || fc.Lines[0].Text != "line1" {
		t.Fatalf("line[0] = %+v", fc.Lines[0])
	}
	if fc.Lines[1].Number != 2 || fc.Lines[1].Text != "line2" {
		t.Fatalf("line[1] = %+v", fc.Lines[1])
	}
}

func TestReadFileContentNotExist(t *testing.T) {
	fc, err := ReadFileContent("/nonexistent/path/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if fc.Exists {
		t.Fatal("file should not exist")
	}
}

func TestRepoFileOpsRejectLeanSourceFiles(t *testing.T) {
	dir := t.TempDir()
	leanRel := filepath.Join("MCPAIHelperProject", "ActiveTasks.lean")
	writePath := filepath.Join(dir, leanRel)
	if err := os.MkdirAll(filepath.Dir(writePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(writePath, []byte("def secret := 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadFileContentInRepo(dir, leanRel); err == nil || !strings.Contains(err.Error(), "policy_denied") || strings.Contains(err.Error(), "task-owned") {
		t.Fatalf("ReadFileContentInRepo error = %v, want local policy denial", err)
	}
	if _, err := ReadSnapshotInRepo(dir, leanRel); err == nil || !strings.Contains(err.Error(), "protected task registry source") || strings.Contains(err.Error(), "task-owned") {
		t.Fatalf("ReadSnapshotInRepo error = %v, want local policy denial", err)
	}
	if _, err := ApplyGuardedReplace(ReplaceRequest{RepoPath: dir, Path: leanRel, ExpectedHash: "deadbeef", Old: "def", New: "theorem"}); err == nil || !strings.Contains(err.Error(), "protected task registry source") || strings.Contains(err.Error(), "task-owned") {
		t.Fatalf("ApplyGuardedReplace error = %v, want local policy denial", err)
	}
}

func TestSearchFilesAllowsRegularLeanSourceFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Visible.go"), []byte("package p\n// needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Hidden.lean"), []byte("-- needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := SearchFilesInRepo(dir, "", "needle", 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 || len(result.Matches) != 2 {
		t.Fatalf("matches = %#v, want Go and regular Lean matches", result.Matches)
	}
}

func TestSearchFilesSkipsTaskRegistryDirectories(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{
		filepath.Join("obsidian-tasks", "task-001.md"),
		filepath.Join("tasks", "task-001.lean"),
		filepath.Join("MCPAIHelperProject", "ActiveTasks.lean"),
	} {
		writePath := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(writePath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(writePath, []byte("hidden-task-needle\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	visiblePath := filepath.Join(dir, "internal", "visible.go")
	if err := os.MkdirAll(filepath.Dir(visiblePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(visiblePath, []byte("package internal\n// hidden-task-needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := SearchFilesInRepo(dir, "", "hidden-task-needle", 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Matches) != 1 || !strings.HasPrefix(result.Matches[0], "internal/visible.go:") {
		t.Fatalf("matches = %#v, want only non-task project files", result.Matches)
	}
}

func TestRepoFileOpsAllowRegularLeanSourceFiles(t *testing.T) {
	dir := t.TempDir()
	leanRel := filepath.Join("src", "Module.lean")
	writePath := filepath.Join(dir, leanRel)
	if err := os.MkdirAll(filepath.Dir(writePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(writePath, []byte("def visible := 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fc, err := ReadFileContentInRepo(dir, leanRel)
	if err != nil {
		t.Fatal(err)
	}
	if !fc.Exists || fc.RelativePath != filepath.ToSlash(leanRel) {
		t.Fatalf("file content = %#v", fc)
	}
}

func TestReadFileContentInRepo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fc, err := ReadFileContentInRepo(dir, "src/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !fc.Exists {
		t.Fatal("file should exist")
	}
	if fc.RelativePath != "src/main.go" {
		t.Fatalf("RelativePath = %q", fc.RelativePath)
	}
}

func TestReadFileContentInRepoRejectsEscape(t *testing.T) {
	_, err := ReadFileContentInRepo(t.TempDir(), "../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path escape")
	}
}
func TestSearchFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\n\nfunc Foo() {\n\tprintln(\"hello\")\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package p\n\nfunc Bar() {\n\tFoo()\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := SearchFiles(dir, "Foo", 100)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total < 2 {
		t.Fatalf("total = %d, want >= 2", result.Total)
	}
	for _, m := range result.Matches {
		file, rest, ok := strings.Cut(m, ":")
		if !ok || file == "" {
			t.Fatalf("match should start with a file: %q", m)
		}
		lineNumber, text, ok := strings.Cut(rest, ":")
		if !ok || !strings.Contains(text, "Foo") {
			t.Fatalf("match should contain pattern: %q", m)
		}
		if n, err := strconv.Atoi(lineNumber); err != nil || n < 1 {
			t.Fatalf("line number should be >= 1: %q", m)
		}
	}
}

func TestSearchFilesMaxMatches(t *testing.T) {
	dir := t.TempDir()
	for i := range 5 {
		name := filepath.Join(dir, fmt.Sprintf("f%d.go", i))
		data := fmt.Appendf(nil, "package p\nvar x%d = 1\nvar y%d = 2\n", i, i)
		if err := os.WriteFile(name, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := SearchFiles(dir, "var", 3)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total > 3 {
		t.Fatalf("total = %d, want <= 3 (max)", result.Total)
	}
}

// A search that stops at the cap has to say so. Without the flag Total reads as
// a count of everything there is, and a reader draws its conclusion from a
// partial answer without ever learning that it was partial.
func TestSearchFilesReportsTruncationAtCap(t *testing.T) {
	dir := t.TempDir()
	for i := range 5 {
		name := filepath.Join(dir, fmt.Sprintf("f%d.go", i))
		data := fmt.Appendf(nil, "package p\nvar x%d = 1\nvar y%d = 2\n", i, i)
		if err := os.WriteFile(name, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	capped, err := SearchFiles(dir, "var", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !capped.Truncated {
		t.Fatalf("search stopped at the cap without reporting it: %#v", capped)
	}
	full, err := SearchFiles(dir, "var", 100)
	if err != nil {
		t.Fatal(err)
	}
	if full.Truncated {
		t.Fatalf("search saw every match but reported truncation: %#v", full)
	}
}

func TestSearchFilesSkipsHidden(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("secret=foo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("var secret = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := SearchFiles(dir, "secret", 10)
	if err != nil {
		t.Fatal(err)
	}
	// Should find main.go but not .git/config
	for _, m := range result.Matches {
		if file, _, _ := strings.Cut(m, ":"); strings.Contains(file, ".git") {
			t.Fatalf("should skip .git dir: %s", m)
		}
	}
}

func TestSearchFilesInRepo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := SearchFilesInRepo(dir, "", "func main", 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("total = %d, want 1", result.Total)
	}
}

func TestReadFileContentInRepoRejectsSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	linkPath := filepath.Join(dir, "link.txt")
	if err := os.Symlink("/etc/passwd", linkPath); err != nil {
		t.Fatal(err)
	}
	_, err := ReadFileContentInRepo(dir, "link.txt")
	if err == nil {
		t.Fatal("expected error for symlink escape")
	}
}

// --- ListDir tests ---

// childOf builds the repo-relative path of an entry the way a reader of the
// result has to: the listing names its directory once, and the entry adds its
// name to it.
func childOf(result ListDirResult, entry DirEntry) string {
	if result.RelativePath == "" {
		return entry.Name
	}
	return result.RelativePath + "/" + entry.Name
}

func TestListDirEntriesAreAddressableFromTheListing(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := ListDir(ListDirRequest{RepoPath: dir, Path: "internal"})
	if err != nil {
		t.Fatal(err)
	}
	if result.RelativePath != "internal" {
		t.Fatalf("relative_path = %q, want %q", result.RelativePath, "internal")
	}
	if len(result.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(result.Entries))
	}
	// Every entry has to be nameable back to the repo-scoped readers, which
	// reject an absolute path. That round trip is what a per-entry path used
	// to spend most of the payload on without ever achieving.
	for _, entry := range result.Entries {
		child := childOf(result, entry)
		if entry.IsDir {
			_, err = ListDir(ListDirRequest{RepoPath: dir, Path: child})
		} else {
			_, err = ReadSnapshotInRepo(dir, child)
		}
		if err != nil {
			t.Fatalf("entry %q is not addressable as %q: %v", entry.Name, child, err)
		}
	}
}

// Listing a path that does not exist must name the nearest directory that
// does, so the caller lists that instead of guessing at the tree layout.
func TestListDirMissingPathTeaches(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src", "vega"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := ListDir(ListDirRequest{RepoPath: dir, Path: "src/vega/platform-release/.helm"})
	if err == nil {
		t.Fatal("expected an error for a missing list path")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"src/vega"`) || !strings.Contains(msg, `"src/vega/platform-release/.helm"`) {
		t.Fatalf("error %q must name the missing path and the nearest existing directory", msg)
	}
}

func TestListDirOfRepoRootNamesEntriesByNameAlone(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := ListDir(ListDirRequest{RepoPath: dir})
	if err != nil {
		t.Fatal(err)
	}
	if result.RelativePath != "" {
		t.Fatalf("relative_path = %q, want empty at the repo root", result.RelativePath)
	}
	if result.RepoPath != dir {
		t.Fatalf("repo_path = %q, want %q", result.RepoPath, dir)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(result.Entries))
	}
	if _, err := ReadSnapshotInRepo(dir, childOf(result, result.Entries[0])); err != nil {
		t.Fatalf("root entry is not addressable from the listing: %v", err)
	}
}

func TestListDirWithoutRepoPathStillReportsTheDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := ListDir(ListDirRequest{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != dir {
		t.Fatalf("path = %q, want %q", result.Path, dir)
	}
	if result.RepoPath != "" || result.RelativePath != "" {
		t.Fatalf("unscoped listing claimed a repo: %#v", result)
	}
}

// --- ReadFilesInRepo tests ---

func TestReadFilesNamesItsRepoOnceAndFilesRelativeToIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal"), 0o700); err != nil {
		t.Fatal(err)
	}
	paths := []string{"go.mod", "internal/main.go"}
	for _, rel := range paths {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte("package x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := ReadFilesInRepo(dir, paths)
	if err != nil {
		t.Fatal(err)
	}
	if result.RepoPath != dir {
		t.Fatalf("repo_path = %q, want %q", result.RepoPath, dir)
	}
	if len(result.Files) != len(paths) {
		t.Fatalf("files = %d, want %d", len(result.Files), len(paths))
	}
	// What the batch reports about a file has to be enough to reach it again.
	for _, file := range result.Files {
		if file.Error != "" {
			t.Fatalf("file %q: %s", file.RelativePath, file.Error)
		}
		if _, err := ReadSnapshotInRepo(result.RepoPath, file.RelativePath); err != nil {
			t.Fatalf("file %q is not addressable from the batch: %v", file.RelativePath, err)
		}
	}
}

// A path that could not be read still has to say which path it was.
func TestReadFilesNamesTheFileItCouldNotRead(t *testing.T) {
	dir := t.TempDir()
	result, err := ReadFilesInRepo(dir, []string{"missing.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].RelativePath != "missing.txt" {
		t.Fatalf("files = %#v, want the requested path named", result.Files)
	}
	if result.Files[0].Error == "" {
		t.Fatalf("missing file reported no error: %#v", result.Files[0])
	}
}

// --- CreateIfAbsent tests ---

func TestCreateIfAbsentCreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")
	result, err := CreateIfAbsent(CreateIfAbsentRequest{Path: path, Content: "hello\n"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %+v, want ok changed", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello\n" {
		t.Fatalf("content = %q", string(data))
	}
}

func TestCreateIfAbsentSkipsExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := CreateIfAbsent(CreateIfAbsentRequest{Path: path, Content: "new\n"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "already_present" || result.Changed {
		t.Fatalf("result = %+v, want already_present unchanged", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "original\n" {
		t.Fatalf("content should be unchanged: %q", string(data))
	}
}

func TestCreateIfAbsentRequiresPath(t *testing.T) {
	_, err := CreateIfAbsent(CreateIfAbsentRequest{Content: "x"})
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestCreateIfAbsentRequiresContent(t *testing.T) {
	dir := t.TempDir()
	_, err := CreateIfAbsent(CreateIfAbsentRequest{Path: filepath.Join(dir, "x.txt")})
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

// --- AppendUnique tests ---

func TestAppendUniqueAppendsNewContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("line1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadSnapshot(path)
	result, err := AppendUnique(AppendUniqueRequest{
		Path:         path,
		ExpectedHash: snapshot.Hash,
		Content:      "line2\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %+v, want ok changed", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "line1\nline2\n" {
		t.Fatalf("content = %q", string(data))
	}
}

func TestAppendUniqueSkipsExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("line1\nline2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadSnapshot(path)
	result, err := AppendUnique(AppendUniqueRequest{
		Path:         path,
		ExpectedHash: snapshot.Hash,
		Content:      "line2\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || result.Changed {
		t.Fatalf("result = %+v, want ok unchanged", result)
	}
}

func TestAppendUniqueDetectsHashMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := AppendUnique(AppendUniqueRequest{
		Path:         path,
		ExpectedHash: "deadbeef",
		Content:      "new\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "conflict" {
		t.Fatalf("status = %q, want conflict", result.Status)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "original\n" {
		t.Fatalf("file should be unchanged: %q", string(data))
	}
}

func TestAppendUniqueRequiresPath(t *testing.T) {
	_, err := AppendUnique(AppendUniqueRequest{ExpectedHash: "x", Content: "y"})
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestAppendUniqueRequiresExpectedHash(t *testing.T) {
	dir := t.TempDir()
	_, err := AppendUnique(AppendUniqueRequest{Path: filepath.Join(dir, "f.txt"), Content: "y"})
	if err == nil {
		t.Fatal("expected error for empty expected_hash")
	}
}

func TestAppendUniqueRequiresContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadSnapshot(path)
	_, err := AppendUnique(AppendUniqueRequest{Path: path, ExpectedHash: snapshot.Hash})
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestAppendUniqueWithBase64(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("line1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadSnapshot(path)
	result, err := AppendUnique(AppendUniqueRequest{
		Path:         path,
		ExpectedHash: snapshot.Hash,
		ContentB64:   base64.StdEncoding.EncodeToString([]byte("line2\n")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %+v, want ok changed", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "line1\nline2\n" {
		t.Fatalf("content = %q", string(data))
	}
}

func TestAppendUniqueHandlesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadSnapshot(path)
	result, err := AppendUnique(AppendUniqueRequest{
		Path:         path,
		ExpectedHash: snapshot.Hash,
		Content:      "first\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %+v, want ok changed", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "first\n" {
		t.Fatalf("content = %q (no separator for empty file)", string(data))
	}
}

// --- DeleteExactBlock tests ---

func TestDeleteExactBlockRemovesBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("before\nblock start\nblock end\nafter\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadSnapshot(path)
	result, err := DeleteExactBlock(DeleteExactBlockRequest{
		Path:         path,
		ExpectedHash: snapshot.Hash,
		Block:        "block start\nblock end\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %+v, want ok changed", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "before\nafter\n" {
		t.Fatalf("content = %q", string(data))
	}
}

func TestDeleteExactBlockIdempotentWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("before\nafter\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadSnapshot(path)
	result, err := DeleteExactBlock(DeleteExactBlockRequest{
		Path:         path,
		ExpectedHash: snapshot.Hash,
		Block:        "nonexistent\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || result.Changed {
		t.Fatalf("result = %+v, want ok unchanged (idempotent)", result)
	}
}

func TestDeleteExactBlockDetectsHashMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := DeleteExactBlock(DeleteExactBlockRequest{
		Path:         path,
		ExpectedHash: "deadbeef",
		Block:        "content\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "conflict" {
		t.Fatalf("status = %q, want conflict", result.Status)
	}
}

func TestDeleteExactBlockRejectsNonUniqueBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("a\nblock\nb\nblock\nc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadSnapshot(path)
	result, err := DeleteExactBlock(DeleteExactBlockRequest{
		Path:         path,
		ExpectedHash: snapshot.Hash,
		Block:        "block\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "conflict" {
		t.Fatalf("status = %q, want conflict (non-unique)", result.Status)
	}
}

func TestDeleteExactBlockRequiresPath(t *testing.T) {
	_, err := DeleteExactBlock(DeleteExactBlockRequest{ExpectedHash: "x", Block: "y"})
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestDeleteExactBlockRequiresExpectedHash(t *testing.T) {
	dir := t.TempDir()
	_, err := DeleteExactBlock(DeleteExactBlockRequest{Path: filepath.Join(dir, "f.txt"), Block: "y"})
	if err == nil {
		t.Fatal("expected error for empty expected_hash")
	}
}

func TestDeleteExactBlockRequiresBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadSnapshot(path)
	_, err := DeleteExactBlock(DeleteExactBlockRequest{Path: path, ExpectedHash: snapshot.Hash})
	if err == nil {
		t.Fatal("expected error for empty block")
	}
}

func TestDeleteExactBlockWithBase64(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("before\nremove me\nafter\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadSnapshot(path)
	result, err := DeleteExactBlock(DeleteExactBlockRequest{
		Path:         path,
		ExpectedHash: snapshot.Hash,
		BlockB64:     base64.StdEncoding.EncodeToString([]byte("remove me\n")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %+v, want ok changed", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "before\nafter\n" {
		t.Fatalf("content = %q", string(data))
	}
}

// --- WriteFile tests ---

func TestWriteFileCreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "new.txt")
	result, err := WriteFile(WriteFileRequest{Path: path, Content: "hello\n"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %+v, want ok changed", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello\n" {
		t.Fatalf("content = %q", string(data))
	}
}

func TestWriteFileCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "file.txt")
	result, err := WriteFile(WriteFileRequest{Path: path, Content: "deep\n"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %+v, want ok changed", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "deep\n" {
		t.Fatalf("content = %q", string(data))
	}
}

func TestWriteFileOverwritesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := WriteFile(WriteFileRequest{Path: path, Content: "new\n"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %+v, want ok changed", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new\n" {
		t.Fatalf("content = %q", string(data))
	}
}

func TestWriteFileIdempotentWhenContentMatches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("same\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := WriteFile(WriteFileRequest{Path: path, Content: "same\n"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || result.Changed {
		t.Fatalf("result = %+v, want ok unchanged (idempotent)", result)
	}
}

func TestWriteFileGuardsOnExpectedHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := ReadSnapshot(path)
	result, err := WriteFile(WriteFileRequest{
		Path:         path,
		Content:      "overwritten\n",
		ExpectedHash: snapshot.Hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %+v, want ok changed", result)
	}
}

func TestWriteFileConflictOnHashMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := WriteFile(WriteFileRequest{
		Path:         path,
		Content:      "new\n",
		ExpectedHash: "deadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "conflict" {
		t.Fatalf("status = %q, want conflict", result.Status)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "original\n" {
		t.Fatalf("file should be unchanged: %q", string(data))
	}
}

func TestWriteFileWithBase64(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	result, err := WriteFile(WriteFileRequest{
		Path:       path,
		ContentB64: base64.StdEncoding.EncodeToString([]byte("encoded\n")),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" || !result.Changed {
		t.Fatalf("result = %+v, want ok changed", result)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "encoded\n" {
		t.Fatalf("content = %q", string(data))
	}
}

func TestWriteFileRequiresPath(t *testing.T) {
	_, err := WriteFile(WriteFileRequest{Content: "x"})
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestWriteFileRequiresContent(t *testing.T) {
	dir := t.TempDir()
	_, err := WriteFile(WriteFileRequest{Path: filepath.Join(dir, "f.txt")})
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestFileMutationsRejectUnsafeMode(t *testing.T) {
	testCases := []struct {
		name string
		mode int
	}{
		{name: "negative", mode: -1},
		{name: "special bit", mode: 0o1000},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := CreateIfAbsent(CreateIfAbsentRequest{RepoPath: dir, Path: "create.txt", Content: "x", Mode: testCase.mode}); err == nil {
				t.Fatal("CreateIfAbsent accepted unsafe mode")
			}
			if _, err := WriteFile(WriteFileRequest{RepoPath: dir, Path: "write.txt", Content: "x", Mode: testCase.mode}); err == nil {
				t.Fatal("WriteFile accepted unsafe mode")
			}
		})
	}
}

func TestRepoMutationsRejectSymlinkedFileEscape(t *testing.T) {
	repoPath := t.TempDir()
	outsidePath := filepath.Join(t.TempDir(), "outside.txt")
	original := []byte("original\n")
	if err := os.WriteFile(outsidePath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(repoPath, "escape.txt")); err != nil {
		t.Fatal(err)
	}

	operations := []struct {
		name string
		run  func() error
	}{
		{
			name: "guarded replace",
			run: func() error {
				_, err := ApplyGuardedReplace(ReplaceRequest{
					RepoPath: repoPath, Path: "escape.txt", ExpectedHash: Hash(original),
					Old: "original", New: "changed",
				})
				return err
			},
		},
		{
			name: "create if absent",
			run: func() error {
				_, err := CreateIfAbsent(CreateIfAbsentRequest{
					RepoPath: repoPath, Path: "escape.txt", Content: "changed",
				})
				return err
			},
		},
		{
			name: "append unique",
			run: func() error {
				_, err := AppendUnique(AppendUniqueRequest{
					RepoPath: repoPath, Path: "escape.txt", ExpectedHash: Hash(original), Content: "changed",
				})
				return err
			},
		},
		{
			name: "delete exact block",
			run: func() error {
				_, err := DeleteExactBlock(DeleteExactBlockRequest{
					RepoPath: repoPath, Path: "escape.txt", ExpectedHash: Hash(original), Block: "original\n",
				})
				return err
			},
		},
		{
			name: "write file",
			run: func() error {
				_, err := WriteFile(WriteFileRequest{
					RepoPath: repoPath, Path: "escape.txt", Content: "changed",
				})
				return err
			},
		},
	}

	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); err == nil {
				t.Fatal("operation unexpectedly followed a symlink outside the repository")
			}
			data, err := os.ReadFile(outsidePath)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != string(original) {
				t.Fatalf("outside file changed to %q", data)
			}
		})
	}
}

func TestRepoOperationsRejectSymlinkedParentEscape(t *testing.T) {
	repoPath := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(repoPath, "linked")); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteFile(WriteFileRequest{
		RepoPath: repoPath,
		Path:     "linked/nested/file.txt",
		Content:  "changed",
	}); err == nil {
		t.Fatal("WriteFile unexpectedly created a file through a symlinked parent")
	}
	if _, err := CreateIfAbsent(CreateIfAbsentRequest{
		RepoPath: repoPath,
		Path:     "linked/file.txt",
		Content:  "changed",
	}); err == nil {
		t.Fatal("CreateIfAbsent unexpectedly created a file through a symlinked parent")
	}
	if _, err := ListDir(ListDirRequest{RepoPath: repoPath, Path: "linked"}); err == nil {
		t.Fatal("ListDir unexpectedly traversed a symlinked directory")
	}
	if _, err := SearchFilesInRepo(repoPath, "linked", "secret", 10); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatal("SearchFilesInRepo must refuse a symlinked directory as a walk root instead of traversing it")
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "file.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside file error = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "nested", "file.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside nested file error = %v, want not exist", err)
	}
}
