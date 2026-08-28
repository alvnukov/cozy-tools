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

func TestMask_ApplyOverlappingSecretsLongestFirst(t *testing.T) {
	m := NewMask()
	m.Add("secret")
	m.AddNamed("TOKEN", "secret-token")
	if got := m.Apply("value=secret-token"); got != "value=[HELPER_SECRET:TOKEN]" {
		t.Fatalf("got %q; shorter secret exposed part of the longer secret", got)
	}
}

func TestMask_EqualIgnoresInsertionOrder(t *testing.T) {
	first := NewMask()
	first.AddNamed("A", "first-secret")
	first.AddNamed("B", "second-secret")
	second := NewMask()
	second.AddNamed("B", "second-secret")
	second.AddNamed("A", "first-secret")
	if !first.Equal(second) {
		t.Fatal("masks with the same secrets are not equal")
	}
	second.Add("third-secret")
	if first.Equal(second) {
		t.Fatal("masks with different secrets are equal")
	}
	if !((*Mask)(nil)).Equal(NewMask()) {
		t.Fatal("nil and empty masks should be equivalent")
	}
}
