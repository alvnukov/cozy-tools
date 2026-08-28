package command

import (
	"crypto/sha256"
	"io"
	"sync"
	"unicode/utf8"
)

// streamChunk is one retained slice of raw output, tagged with the stream it
// came from so the combined view can preserve cross-stream order.
type streamChunk struct {
	stream string
	data   []byte
}

// tailOutput captures command output as an ordered, bounded tail under one
// total budget shared by both streams, while hashing the complete output of
// each stream. It replaces the older per-stream prefix buffers, which
// retained the earliest bytes -- exactly the wrong end when a command fails
// at the end of a long run -- and let the two streams together exceed the
// budget.
//
// Retention holds raw bytes; redaction is applied when a view is assembled,
// over each contiguous same-stream run, so a secret split across writes is
// still masked. A secret whose halves are separated by output from the other
// stream is not a contiguous occurrence and is not masked in the combined
// view. The per-stream hashers digest raw bytes as they arrive, so the
// output hash covers the complete output even when retention truncates it;
// the hash is an opaque digest and never exposes the text it covers.
type tailOutput struct {
	mu        sync.Mutex
	budget    int
	chunks    []streamChunk
	keptBytes int
	truncated bool
	hashes    map[string]io.Writer
	redact    func(string) string
	update    func(stdoutText, stderrText string, truncated bool)
}

// defaultOutputBudget is the retention budget when the policy leaves
// MaxOutputBytes unset, matching the buffer this type replaced.
const defaultOutputBudget = 200000

// newTailOutput creates a tail with the given total byte budget. An unset
// or negative budget falls back to defaultOutputBudget; a budget of zero
// cannot be expressed, so retention always holds the tail of at least the
// default budget. redact is applied when snapshots are assembled; update,
// when set, is invoked after every write with the current redacted
// per-stream tail and truncation flag.
func newTailOutput(budget int, redact func(string) string, update func(stdoutText, stderrText string, truncated bool)) *tailOutput {
	if budget <= 0 {
		budget = defaultOutputBudget
	}
	if redact == nil {
		redact = func(s string) string { return s }
	}
	return &tailOutput{
		budget: budget,
		hashes: map[string]io.Writer{"stdout": sha256.New(), "stderr": sha256.New()},
		redact: redact,
		update: update,
	}
}

func (o *tailOutput) stdoutWriter() io.Writer { return &tailWriter{output: o, stream: "stdout"} }

func (o *tailOutput) stderrWriter() io.Writer { return &tailWriter{output: o, stream: "stderr"} }

type tailWriter struct {
	output *tailOutput
	stream string
}

func (w *tailWriter) Write(p []byte) (int, error) {
	return w.output.write(w.stream, p)
}

// write consumes one chunk of stream output: the raw bytes feed the
// full-coverage hasher and the ordered tail, the oldest bytes are evicted
// over budget, and the update callback sees the new redacted snapshot.
func (o *tailOutput) write(stream string, p []byte) (int, error) {
	o.mu.Lock()
	_, _ = o.hashes[stream].Write(p)
	if len(p) > 0 {
		data := append([]byte(nil), p...)
		o.chunks = append(o.chunks, streamChunk{stream: stream, data: data})
		o.keptBytes += len(data)
		o.evictLocked()
	}
	stdoutText, stderrText, truncated := o.snapshotLocked()
	o.mu.Unlock()
	if o.update != nil {
		o.update(stdoutText, stderrText, truncated)
	}
	return len(p), nil
}

// evictLocked drops the oldest retained bytes until the tail fits the
// budget, keeping whole runes at the head of what remains.
func (o *tailOutput) evictLocked() {
	for o.keptBytes > o.budget && len(o.chunks) > 0 {
		first := &o.chunks[0]
		over := o.keptBytes - o.budget
		if over < len(first.data) {
			cut := headRuneCut(first.data, over)
			first.data = first.data[cut:]
			o.keptBytes -= cut
			o.truncated = true
			return
		}
		o.keptBytes -= len(first.data)
		o.chunks = o.chunks[1:]
		o.truncated = true
	}
}

// headRuneCut advances the cut past a rune start so trimming the head of b
// never leaves a partial rune at the beginning of the remainder.
func headRuneCut(b []byte, limit int) int {
	cut := limit
	for cut < len(b) && !utf8.RuneStart(b[cut]) {
		cut++
	}
	return cut
}

// snapshot returns the current per-stream tails, redacted, with the
// truncation flag. Per-stream text is in stream order; cross-stream order
// lives in combinedText.
func (o *tailOutput) snapshot() (string, string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.snapshotLocked()
}

func (o *tailOutput) snapshotLocked() (string, string, bool) {
	var stdout, stderr []byte
	for _, chunk := range o.chunks {
		if chunk.stream == "stderr" {
			stderr = append(stderr, chunk.data...)
		} else {
			stdout = append(stdout, chunk.data...)
		}
	}
	return o.redact(string(stdout)), o.redact(string(stderr)), o.truncated
}

// combinedText returns the retained tail as one interleaved text in the
// order the bytes arrived, redacted over each contiguous same-stream run.
func (o *tailOutput) combinedText() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var combined []byte
	runStream := ""
	var run []byte
	flushRun := func() {
		if len(run) > 0 {
			combined = append(combined, o.redact(string(run))...)
			run = run[:0]
		}
	}
	for _, chunk := range o.chunks {
		if chunk.stream != runStream {
			flushRun()
			runStream = chunk.stream
		}
		run = append(run, chunk.data...)
	}
	flushRun()
	return string(combined)
}

// currentSum composes the output hash from the per-stream hashers as they
// stand now; finalSum is the same composition once every write has landed.
func (o *tailOutput) currentSum() [sha256.Size]byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.composedSumLocked()
}

// finalSum composes the final output hash: sha256 over
// sha256(stdout) || LF || sha256(stderr). It covers the complete output of
// both streams even when retention truncated it, and is independent of how
// writes were chunked.
func (o *tailOutput) finalSum() [sha256.Size]byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.composedSumLocked()
}

// sha256Summer is the narrow view of a digest needed to compose the final
// hash without exposing the hasher type.
type sha256Summer interface{ Sum([]byte) []byte }

func (o *tailOutput) composedSumLocked() [sha256.Size]byte {
	var stdoutSum, stderrSum []byte
	if s, ok := o.hashes["stdout"].(sha256Summer); ok {
		stdoutSum = s.Sum(nil)
	}
	if s, ok := o.hashes["stderr"].(sha256Summer); ok {
		stderrSum = s.Sum(nil)
	}
	composed := append(append([]byte{}, stdoutSum...), '\n')
	composed = append(composed, stderrSum...)
	return sha256.Sum256(composed)
}
