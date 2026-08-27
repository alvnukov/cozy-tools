package filesystem

import (
	"bytes"
	"go/format"
	"os"
	"path/filepath"
	"testing"
)

func TestLineAnchorCozyCompatibility(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]string{
		"":    "zyb",
		"---": "gmw",
	} {
		if got := lineAnchor(text); got != want {
			t.Errorf("lineAnchor(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestSourcesAreFormatted(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		formatted, err := format.Source(raw)
		if err != nil {
			t.Fatalf("format %s: %v", name, err)
		}
		if !bytes.Equal(raw, formatted) {
			t.Errorf("%s is not gofmt-formatted", name)
		}
	}
}
