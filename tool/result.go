package tool

import (
	"encoding/json"
	"fmt"
)

// Artifact points to output retained outside the model-facing result.
type Artifact struct {
	URI         string `json:"uri"`
	MediaType   string `json:"media_type,omitempty"`
	Description string `json:"description,omitempty"`
}

// Truncation explains a bounded result and how to continue reading it.
type Truncation struct {
	Reason        string `json:"reason,omitempty"`
	ReturnedBytes int64  `json:"returned_bytes,omitempty"`
	TotalBytes    int64  `json:"total_bytes,omitempty"`
	ReturnedLines int64  `json:"returned_lines,omitempty"`
	TotalLines    int64  `json:"total_lines,omitempty"`
	Continuation  string `json:"continuation,omitempty"`
}

// Result is a transport-neutral tool result.
type Result struct {
	// Content is compact text intended for the model.
	Content string
	// Structured is the canonical machine-readable result when one exists.
	Structured any
	// Detail is a short one-line summary suitable for a tool row.
	Detail string
	// Output is a richer display body; empty falls back to Content.
	Output     string
	Artifacts  []Artifact
	Truncation *Truncation
}

// ModelContent returns explicit model text or JSON for Structured.
func (r Result) ModelContent() (string, error) {
	if r.Content != "" {
		return r.Content, nil
	}
	if r.Structured == nil {
		return "", nil
	}
	raw, err := json.Marshal(r.Structured)
	if err != nil {
		return "", fmt.Errorf("encode structured tool result: %w", err)
	}
	return string(raw), nil
}

// DisplayOutput returns the explicit display body or model content.
func (r Result) DisplayOutput() (string, error) {
	if r.Output != "" {
		return r.Output, nil
	}
	return r.ModelContent()
}
