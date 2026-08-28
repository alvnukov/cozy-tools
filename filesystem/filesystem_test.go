package filesystem_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/alvnukov/cozy-tools/filesystem"
)

func openService(t *testing.T, options ...filesystem.Option) (string, *filesystem.Service) {
	t.Helper()
	root := t.TempDir()
	service, err := filesystem.Open(root, options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return root, service
}

func putFile(t *testing.T, root, name string, data []byte, mode os.FileMode) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, mode); err != nil {
		t.Fatal(err)
	}
}

func sha(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func text(value string) *string { return &value }

func TestConfinementRejectsEscapesAndOutsideSymlinks(t *testing.T) {
	root, service := openService(t)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"../outside.txt", "/etc/passwd", "a/../../outside"} {
		if _, err := service.Read(context.Background(), filesystem.ReadRequest{Path: name}); !errors.Is(err, filesystem.ErrInvalidInput) {
			t.Errorf("Read(%q) error = %v, want invalid input", name, err)
		}
	}
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges")
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Read(context.Background(), filesystem.ReadRequest{Path: "escape"}); filesystem.CodeOf(err) != filesystem.CodePermission {
		t.Fatalf("outside symlink Read error = %v (%s)", err, filesystem.CodeOf(err))
	}
	if _, err := service.Write(context.Background(), filesystem.WriteRequest{Path: "escape", Content: "overwrite"}); !errors.Is(err, filesystem.ErrPermission) {
		t.Fatalf("outside symlink Write error = %v, want permission", err)
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "secret" {
		t.Fatalf("outside file changed: %q, %v", got, err)
	}
}

func TestWriteAtomicParentsModeCreateOnlyAndIdempotency(t *testing.T) {
	root, service := openService(t)
	created, err := service.Write(context.Background(), filesystem.WriteRequest{Path: "a/b/file.txt", Content: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if !created.Changed || created.Status != "created" {
		t.Fatalf("created = %+v", created)
	}
	if err := os.Chmod(filepath.Join(root, "a/b/file.txt"), 0o640); err != nil {
		t.Fatal(err)
	}
	updated, err := service.Write(context.Background(), filesystem.WriteRequest{Path: "a/b/file.txt", Content: "second", ExpectedSHA256: created.NewSHA256[:8]})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "a/b/file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o, want 640", info.Mode().Perm())
	}
	unchanged, err := service.Write(context.Background(), filesystem.WriteRequest{Path: "a/b/file.txt", Content: "second", Mode: filesystem.WriteCreateOnly})
	if err != nil || unchanged.Changed || unchanged.Status != "unchanged" {
		t.Fatalf("idempotent create-only = %+v, %v", unchanged, err)
	}
	if _, err := service.Write(context.Background(), filesystem.WriteRequest{Path: "a/b/file.txt", Content: "third", Mode: filesystem.WriteCreateOnly}); !errors.Is(err, filesystem.ErrConflict) {
		t.Fatalf("create-only error = %v", err)
	}
	if updated.OldSHA256 != created.NewSHA256 || updated.NewSHA256 != sha([]byte("second")) {
		t.Fatalf("hashes = %+v", updated)
	}
	entries, err := os.ReadDir(filepath.Join(root, "a/b"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".cozy-tmp-") {
			t.Errorf("temporary file remained: %s", entry.Name())
		}
	}
}

func TestReadPaginationRawHashAnchorsCRLFAndCaps(t *testing.T) {
	root, service := openService(t)
	raw := []byte("alpha\r\n\r\nomega\r\n")
	putFile(t, root, "crlf.txt", raw, 0o600)
	result, err := service.Read(context.Background(), filesystem.ReadRequest{Path: "crlf.txt", Offset: 2, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.SHA256 != sha(raw) || len(result.HashChunks) != 8 || result.HashChunks[0] != result.SHA256[:8] {
		t.Fatalf("hash metadata = %+v", result)
	}
	if result.Binary || result.TotalLines != 3 || len(result.Lines) != 1 || result.Lines[0].Number != 2 || result.Lines[0].Text != "" || result.Lines[0].Anchor != "zyb" {
		t.Fatalf("line result = %+v", result)
	}
	if !result.Truncation.Truncated || result.NextOffset != 3 || result.Truncation.Continuation != "offset=3" {
		t.Fatalf("pagination = %+v", result.Truncation)
	}

	putFile(t, root, "long.txt", []byte("12345\nsecond\n"), 0o600)
	bounded, err := service.Read(context.Background(), filesystem.ReadRequest{Path: "long.txt", MaxBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(bounded.Lines) != 0 || !bounded.Truncation.Truncated || bounded.Truncation.ReturnedBytes > 4 {
		t.Fatalf("bounded read split a line: %+v", bounded)
	}
	putFile(t, root, "empty.txt", nil, 0o600)
	empty, err := service.Read(context.Background(), filesystem.ReadRequest{Path: "empty.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if empty.TotalLines != 0 || len(empty.Lines) != 0 || empty.SHA256 != sha(nil) {
		t.Fatalf("empty = %+v", empty)
	}
}

func TestEditGuardsRangesIdempotencyAndNewlines(t *testing.T) {
	root, service := openService(t)
	raw := []byte("one\r\ntwo\r\nthree\r\n")
	putFile(t, root, "edit.txt", raw, 0o640)
	read, err := service.Read(context.Background(), filesystem.ReadRequest{Path: "edit.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Edit(context.Background(), filesystem.EditRequest{Path: "edit.txt", ExpectedSHA256: strings.Repeat("0", 64), Replace: &filesystem.ReplaceEdit{Old: "two", New: "TWO"}}); !errors.Is(err, filesystem.ErrConflict) {
		t.Fatalf("stale hash error = %v", err)
	}
	bad := read.Lines[1]
	if _, err := service.Edit(context.Background(), filesystem.EditRequest{Path: "edit.txt", ExpectedSHA256: read.SHA256, Edits: []filesystem.HashlineEdit{{From: "2#aaa", To: "2#aaa", Content: text("TWO")}}}); !errors.Is(err, filesystem.ErrConflict) {
		t.Fatalf("anchor mismatch error = %v", err)
	}
	from1 := "1#" + read.Lines[0].Anchor
	to2 := "2#" + bad.Anchor
	from2 := to2
	to3 := "3#" + read.Lines[2].Anchor
	if _, err := service.Edit(context.Background(), filesystem.EditRequest{Path: "edit.txt", ExpectedSHA256: read.SHA256, Edits: []filesystem.HashlineEdit{{From: from1, To: to2, Content: text("x")}, {From: from2, To: to3, Content: text("y")}}}); !errors.Is(err, filesystem.ErrConflict) {
		t.Fatalf("overlap error = %v", err)
	}

	edited, err := service.Edit(context.Background(), filesystem.EditRequest{Path: "edit.txt", ExpectedSHA256: read.SHA256, Edits: []filesystem.HashlineEdit{{From: from2, To: from2, Content: text("TWO")}}})
	if err != nil {
		t.Fatal(err)
	}
	if !edited.Changed {
		t.Fatalf("edited = %+v", edited)
	}
	got, err := os.ReadFile(filepath.Join(root, "edit.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "one\r\nTWO\r\nthree\r\n" {
		t.Fatalf("CRLF/final newline not preserved: %q", got)
	}
	info, _ := os.Stat(filepath.Join(root, "edit.txt"))
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("edit mode = %o", info.Mode().Perm())
	}
	noop, err := service.Edit(context.Background(), filesystem.EditRequest{Path: "edit.txt", Replace: &filesystem.ReplaceEdit{Old: "two", New: "TWO"}})
	if err != nil || noop.Changed || noop.Status != "unchanged" {
		t.Fatalf("idempotent edit = %+v, %v", noop, err)
	}

	putFile(t, root, "no-final.txt", []byte("a\nb"), 0o600)
	changed, err := service.Edit(context.Background(), filesystem.EditRequest{Path: "no-final.txt", Replace: &filesystem.ReplaceEdit{Old: "b", New: "B\n"}})
	if err != nil || !changed.Changed {
		t.Fatalf("no-final edit = %+v, %v", changed, err)
	}
	noFinal, _ := os.ReadFile(filepath.Join(root, "no-final.txt"))
	if string(noFinal) != "a\nB" {
		t.Fatalf("final newline introduced: %q", noFinal)
	}

	putFile(t, root, "multi.txt", []byte("a\nb\nc\nd\n"), 0o600)
	multi, err := service.Read(context.Background(), filesystem.ReadRequest{Path: "multi.txt"})
	if err != nil {
		t.Fatal(err)
	}
	multiEdit, err := service.Edit(context.Background(), filesystem.EditRequest{
		Path:           "multi.txt",
		ExpectedSHA256: multi.SHA256,
		Edits: []filesystem.HashlineEdit{
			{From: "2#" + multi.Lines[1].Anchor, Content: nil},
			{From: "4#" + multi.Lines[3].Anchor, Content: text("D")},
		},
	})
	if err != nil || !multiEdit.Changed {
		t.Fatalf("multi-range edit = %+v, %v", multiEdit, err)
	}
	multiRaw, _ := os.ReadFile(filepath.Join(root, "multi.txt"))
	if string(multiRaw) != "a\nc\nD\n" {
		t.Fatalf("multi-range result = %q", multiRaw)
	}
	putFile(t, root, "duplicate.txt", []byte("x x"), 0o600)
	if _, err := service.Edit(context.Background(), filesystem.EditRequest{Path: "duplicate.txt", Replace: &filesystem.ReplaceEdit{Old: "x", New: "y"}}); !errors.Is(err, filesystem.ErrConflict) {
		t.Fatalf("duplicate unique replacement error = %v", err)
	}
}

func TestListFindHiddenIgnoreDeterministicAndCaps(t *testing.T) {
	limits := filesystem.DefaultLimits()
	limits.ListEntries = 3
	limits.FindMatches = 2
	root, service := openService(t, filesystem.WithLimits(limits))
	putFile(t, root, ".gitignore", []byte("ignored/\n!ignored/keep.go\nonlydir/\n*.tmp\n"), 0o600)
	putFile(t, root, "onlydir", nil, 0o600)
	putFile(t, root, "z.go", nil, 0o600)
	putFile(t, root, "a/a.go", nil, 0o600)
	putFile(t, root, "a/b.txt", nil, 0o600)
	putFile(t, root, ".hidden.go", nil, 0o600)
	putFile(t, root, "ignored/drop.go", nil, 0o600)
	putFile(t, root, "ignored/keep.go", nil, 0o600)
	putFile(t, root, "scratch.tmp", nil, 0o600)
	putFile(t, root, ".git/config", nil, 0o600)
	if runtime.GOOS != "windows" {
		outside := t.TempDir()
		putFile(t, outside, "leak.go", nil, 0o600)
		if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
			t.Fatal(err)
		}
	}

	first, err := service.List(context.Background(), filesystem.ListRequest{MaxDepth: 10})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.List(context.Background(), filesystem.ListRequest{MaxDepth: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Entries, second.Entries) {
		t.Fatalf("list is nondeterministic:\n%+v\n%+v", first.Entries, second.Entries)
	}
	if len(first.Entries) != 3 || !first.Truncation.Truncated || first.Truncation.Reason != "list entry limit" {
		t.Fatalf("list cap = %+v", first)
	}
	for _, entry := range first.Entries {
		if strings.Contains(entry.Path, ".hidden") || strings.Contains(entry.Path, "drop.go") || strings.Contains(entry.Path, ".git/") || strings.Contains(entry.Path, "scratch.tmp") {
			t.Errorf("ignored entry listed: %s", entry.Path)
		}
	}
	found, err := service.Find(context.Background(), filesystem.FindRequest{Pattern: "**/*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Paths) != 2 || !found.Truncation.Truncated {
		t.Fatalf("find cap = %+v", found)
	}
	if !sortStrings(found.Paths) {
		t.Fatalf("find paths not sorted: %v", found.Paths)
	}
	for _, name := range found.Paths {
		if name == "ignored/drop.go" || name == ".hidden.go" || strings.Contains(name, "leak.go") {
			t.Errorf("unsafe/ignored find result: %s", name)
		}
	}
	hidden, err := service.Find(context.Background(), filesystem.FindRequest{Pattern: ".hidden.go", ShowHidden: true, Limit: 1})
	if err != nil || len(hidden.Paths) != 1 || hidden.Paths[0] != ".hidden.go" {
		t.Fatalf("explicit hidden find = %+v, %v", hidden, err)
	}
	directoryPatternFile, err := service.Find(context.Background(), filesystem.FindRequest{Pattern: "onlydir", Limit: 1})
	if err != nil || len(directoryPatternFile.Paths) != 1 || directoryPatternFile.Paths[0] != "onlydir" {
		t.Fatalf("directory-only ignore hid regular file = %+v, %v", directoryPatternFile, err)
	}
}

func sortStrings(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] > values[index] {
			return false
		}
	}
	return true
}

func TestSearchRegexLiteralContextBinaryAndBudgets(t *testing.T) {
	root, service := openService(t)
	putFile(t, root, "a.txt", []byte("before\nNeedle one\nafter\nneedle two\n"), 0o600)
	putFile(t, root, "b.go", []byte("package b\n// Needle three\n"), 0o600)
	putFile(t, root, "binary.txt", []byte{'N', 'e', 'e', 'd', 'l', 'e', 0, 'x'}, 0o600)

	regex, err := service.Search(context.Background(), filesystem.SearchRequest{Pattern: `(?i)^needle`, Glob: "**/*.txt", Before: 1, After: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(regex.Matches) != 2 || regex.SkippedFiles != 1 {
		t.Fatalf("regex search = %+v", regex)
	}
	if len(regex.Matches[0].Before) != 1 || len(regex.Matches[0].After) != 1 || regex.Matches[0].Line != 2 {
		t.Fatalf("context = %+v", regex.Matches[0])
	}
	literal, err := service.Search(context.Background(), filesystem.SearchRequest{Pattern: "NEEDLE", Literal: true, CaseInsensitive: true, Glob: "**/*.go"})
	if err != nil || len(literal.Matches) != 1 || literal.Matches[0].Path != "b.go" {
		t.Fatalf("literal search = %+v, %v", literal, err)
	}
	if _, err := service.Search(context.Background(), filesystem.SearchRequest{Pattern: "["}); !errors.Is(err, filesystem.ErrInvalidInput) {
		t.Fatalf("invalid regexp error = %v", err)
	}

	limitedRoot := t.TempDir()
	putFile(t, limitedRoot, "a.txt", []byte("hit\n"), 0o600)
	putFile(t, limitedRoot, "b.txt", []byte("hit\n"), 0o600)
	limits := filesystem.DefaultLimits()
	limits.ScanFiles = 1
	limited, err := filesystem.Open(limitedRoot, filesystem.WithLimits(limits))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = limited.Close() }()
	budgeted, err := limited.Search(context.Background(), filesystem.SearchRequest{Pattern: "hit", Literal: true})
	if err != nil {
		t.Fatal(err)
	}
	if !budgeted.Truncation.Truncated || budgeted.Truncation.Reason != "scan file limit" {
		t.Fatalf("scan cap = %+v", budgeted)
	}
}

func TestLimitsAndCancellation(t *testing.T) {
	defaults := filesystem.DefaultLimits()
	tooHigh := defaults
	tooHigh.ReadLines++
	if _, err := filesystem.Open(t.TempDir(), filesystem.WithLimits(tooHigh)); !errors.Is(err, filesystem.ErrInvalidInput) {
		t.Fatalf("raised limits error = %v", err)
	}
	_, service := openService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operations := []func() error{
		func() error { _, err := service.Read(ctx, filesystem.ReadRequest{Path: "x"}); return err },
		func() error { _, err := service.List(ctx, filesystem.ListRequest{}); return err },
		func() error { _, err := service.Find(ctx, filesystem.FindRequest{Pattern: "**"}); return err },
		func() error { _, err := service.Search(ctx, filesystem.SearchRequest{Pattern: "x"}); return err },
		func() error {
			_, err := service.Write(ctx, filesystem.WriteRequest{Path: "x", Content: "x"})
			return err
		},
		func() error {
			_, err := service.Edit(ctx, filesystem.EditRequest{Path: "x", Replace: &filesystem.ReplaceEdit{Old: "x", New: "y"}})
			return err
		},
	}
	for index, operation := range operations {
		if err := operation(); !errors.Is(err, context.Canceled) {
			t.Errorf("operation %d error = %v, want context canceled", index, err)
		}
	}
}
