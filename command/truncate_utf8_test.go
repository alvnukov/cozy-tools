package command

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Retained output is cut at a byte budget; a cut inside a multi-byte rune
// corrupts the last line into U+FFFD when the tail is JSON-encoded. The cut
// has to back up to a rune boundary.
func TestLimitBufferCutsOnRuneBoundary(t *testing.T) {
	buf := newLimitBuffer(10)
	if _, err := buf.Write([]byte("ab\n" + strings.Repeat("あ", 10))); err != nil {
		t.Fatal(err)
	}
	if !buf.Truncated() {
		t.Fatal("buffer should be truncated")
	}
	got := buf.String()
	if !utf8.ValidString(got) {
		t.Fatalf("retained tail splits a rune: %q", got)
	}
	if got != "ab\nああ" {
		t.Fatalf("retained tail = %q", got)
	}
}

func TestTruncateUTF8BacksUpToRuneStart(t *testing.T) {
	if got := TruncateUTF8("あいう", 4); got != "あ" {
		t.Fatalf("TruncateUTF8(あいう, 4) = %q, want one full rune", got)
	}
	if got := TruncateUTF8("abc", 4); got != "abc" {
		t.Fatalf("TruncateUTF8(abc, 4) = %q, want the whole short string", got)
	}
	if got := TruncateUTF8("abc", 0); got != "" {
		t.Fatalf("TruncateUTF8(abc, 0) = %q, want empty", got)
	}
}
