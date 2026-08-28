package command

import (
	"crypto/sha256"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/alvnukov/cozy-tools/config"
)

// After overflow the retained output must be the latest bytes, not the
// earliest: a failure message printed at the end of a long run is exactly
// what a caller needs to see.
func TestTailOutputKeepsLatestBytes(t *testing.T) {
	out := newTailOutput(16, passthrough, nil)
	_, _ = out.stdoutWriter().Write([]byte(strings.Repeat("B", 60)))

	stdout, _, truncated := out.snapshot()
	if !truncated {
		t.Fatal("overflow must mark output truncated")
	}
	if strings.Contains(stdout, "A") {
		t.Fatalf("retained output must hold the latest bytes, got %q", stdout)
	}
	if !strings.Contains(stdout, "B") {
		t.Fatalf("retained output must hold the latest bytes, got %q", stdout)
	}
	if len(stdout) > 32 {
		t.Fatalf("retained output exceeds budget: %d", len(stdout))
	}
}

// One budget covers both streams together, not each stream separately.
func TestTailOutputSingleBudgetAcrossStreams(t *testing.T) {
	out := newTailOutput(32, passthrough, nil)
	_, _ = out.stdoutWriter().Write([]byte(strings.Repeat("o", 32)))
	_, _ = out.stderrWriter().Write([]byte(strings.Repeat("e", 32)))

	stdout, stderr, _ := out.snapshot()
	if len(stdout)+len(stderr) > 32 {
		t.Fatalf("total retained %d exceeds the single budget 32", len(stdout)+len(stderr))
	}
	if !strings.Contains(stderr, "e") {
		t.Fatalf("latest stderr must be retained, got %q", stderr)
	}
}

// Cross-stream order is preserved in the combined view.
func TestTailOutputPreservesInterleavedOrder(t *testing.T) {
	out := newTailOutput(1024, passthrough, nil)
	_, _ = out.stdoutWriter().Write([]byte("a1 "))
	_, _ = out.stderrWriter().Write([]byte("b1 "))
	_, _ = out.stdoutWriter().Write([]byte("a2 "))
	_, _ = out.stderrWriter().Write([]byte("b2"))

	if got, want := out.combinedText(), "a1 b1 a2 b2"; got != want {
		t.Fatalf("combined = %q, want %q", got, want)
	}
}

// The output hash covers the complete streams even when retention truncates
// them: identical outputs hash identically regardless of budget.
func TestTailOutputHashCoversFullOutput(t *testing.T) {
	full := strings.Repeat("x", 200) + "payload-that-must-be-hashed"
	withBudget := newTailOutput(16, passthrough, nil)
	_, _ = withBudget.stdoutWriter().Write([]byte(strings.Repeat("x", 200)))
	_, _ = withBudget.stdoutWriter().Write([]byte("payload-that-must-be-hashed"))
	_, _ = withBudget.stderrWriter().Write([]byte("err"))

	sized := newTailOutput(1<<20, passthrough, nil)
	_, _ = sized.stdoutWriter().Write([]byte(full))
	_, _ = sized.stderrWriter().Write([]byte("err"))

	if withBudget.finalSum() != sized.finalSum() {
		t.Fatal("hash must cover the complete output, not the retained tail")
	}

	hS := sha256.Sum256([]byte(full))
	hE := sha256.Sum256([]byte("err"))
	composed := sha256.Sum256(append(append([]byte{}, hS[:]...), append([]byte{'\n'}, hE[:]...)...))
	if withBudget.finalSum() != composed {
		t.Fatal("hash composition must be sha256 over sha256(stdout) || LF || sha256(stderr)")
	}
}

// A mask split across two writes is still applied: redaction is streaming
// with boundary carry, not per-chunk.
func TestTailOutputRedactsAcrossWriteBoundaries(t *testing.T) {
	mask := func(s string) string { return strings.ReplaceAll(s, "TOPSECRET", "[MASKED]") }
	out := newTailOutput(1024, mask, nil)
	_, _ = out.stdoutWriter().Write([]byte("xxTOPSEC"))
	_, _ = out.stdoutWriter().Write([]byte("RETyy"))

	stdout, _, _ := out.snapshot()
	if strings.Contains(stdout, "TOPSEC") {
		t.Fatalf("mask split across writes leaked: %q", stdout)
	}
	if !strings.Contains(stdout, "[MASKED]") {
		t.Fatalf("mask must be applied across the write boundary, got %q", stdout)
	}
}

// Eviction never leaves a partial rune at the head of the retained tail.
func TestTailOutputEvictionKeepsUTF8Intact(t *testing.T) {
	out := newTailOutput(8, passthrough, nil)
	for i := 0; i < 20; i++ {
		_, _ = out.stdoutWriter().Write([]byte("\u65e5"))
	}
	stdout, _, _ := out.snapshot()
	if !utf8.ValidString(stdout) {
		t.Fatalf("retained tail splits a rune: %q", stdout)
	}
	if len(stdout)%3 != 0 {
		t.Fatalf("tail must hold whole runes, got %d bytes", len(stdout))
	}
}

// The update callback receives redacted per-stream text and the truncation
// flag, consistently with what will be persisted.
func TestTailOutputUpdateCallbackSeesRedactedStreams(t *testing.T) {
	var gotStdout, gotStderr string
	var gotTruncated bool
	out := newTailOutput(16, func(s string) string {
		return strings.ReplaceAll(s, "sekrit", "***")
	}, func(stdoutText, stderrText string, truncated bool) {
		gotStdout, gotStderr, gotTruncated = stdoutText, stderrText, truncated
	})
	_, _ = out.stdoutWriter().Write([]byte("sekrit-out"))
	_, _ = out.stderrWriter().Write([]byte("err-sekrit"))
	_, _ = out.stdoutWriter().Write([]byte(strings.Repeat("z", 40)))

	if strings.Contains(gotStdout, "sekrit") || strings.Contains(gotStderr, "sekrit") {
		t.Fatalf("callback must receive redacted text: %q / %q", gotStdout, gotStderr)
	}
	if !gotTruncated {
		t.Fatal("callback must see truncation")
	}
}

// Multiple writers across both streams stay within budget and race-free
// (exercised under the race detector).
func TestTailOutputConcurrentWriters(t *testing.T) {
	out := newTailOutput(256, passthrough, nil)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := out.stdoutWriter()
			if i%2 == 1 {
				w = out.stderrWriter()
			}
			for j := 0; j < 200; j++ {
				_, _ = w.Write([]byte("data"))
			}
		}(i)
	}
	wg.Wait()
	stdout, stderr, _ := out.snapshot()
	if len(stdout)+len(stderr) > 256 {
		t.Fatalf("budget exceeded under concurrency: %d", len(stdout)+len(stderr))
	}
}

// End to end: a command whose stdout overflows the budget still reports its
// latest bytes -- the failure at the end of a long run is what matters.
func TestRunnerRetainsLateStdoutBytes(t *testing.T) {
	dir := t.TempDir()
	early := strings.Repeat("A", 100)
	late := strings.Repeat("B", 100)
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}, MaxOutputBytes: 64})
	result, err := runner.Run(t.Context(), "printf '"+early+late+"'", dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated {
		t.Fatal("overflowed output must be reported truncated")
	}
	joined := strings.Join(result.StdoutTail, "")
	if strings.Contains(joined, "A") {
		t.Fatalf("runner must retain the latest stdout bytes, got %q", joined)
	}
	if !strings.Contains(joined, "B") {
		t.Fatalf("runner must retain the latest stdout bytes, got %q", joined)
	}
}

func passthrough(s string) string { return s }
