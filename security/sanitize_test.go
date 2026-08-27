package security

import "testing"

func TestMask_ApplyNamed(t *testing.T) {
	m := NewMask()
	m.AddNamed("TOKEN", "secret-token-123")
	m.AddNamed("OTHER", "another-secret")
	tests := []struct{ input, expected string }{
		{"error: secret-token-123 failed", "error: [HELPER_SECRET:TOKEN] failed"},
		{"no secrets here", "no secrets here"},
		{"both secret-token-123 and another-secret", "both [HELPER_SECRET:TOKEN] and [HELPER_SECRET:OTHER]"},
	}
	for _, tt := range tests {
		got := m.Apply(tt.input)
		if got != tt.expected {
			t.Errorf("Apply(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestMask_AddNamed(t *testing.T) {
	m := NewMask()
	m.AddNamed("NEW", "new-secret")
	got := m.Apply("error: new-secret")
	if got != "error: [HELPER_SECRET:NEW]" {
		t.Errorf("got %q", got)
	}
}

func TestMask_NoSecrets(t *testing.T) {
	m := NewMask()
	got := m.Apply("clean text")
	if got != "clean text" {
		t.Errorf("got %q", got)
	}
}
