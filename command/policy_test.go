package command

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alvnukov/cozy-tools/config"
)

// Protected-path denial is policy-supplied, not baked in: a marker an
// embedding host cares about denies the command only for runners whose
// policy carries it.
func TestProtectedMarkersArePolicySupplied(t *testing.T) {
	dir := t.TempDir()
	marker := "host-private-state.lean"
	denying := NewRunner(config.CommandPolicy{
		AllowedCWDs:      []string{dir},
		ProtectedMarkers: []string{marker},
		DenyMessage:      "use the host configuration tool instead",
	})
	allowing := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}})

	_, err := denying.Run(t.Context(), "cat "+marker, dir, 5)
	if err == nil || !strings.Contains(err.Error(), "use the host configuration tool instead") {
		t.Fatalf("err = %v, want policy denial", err)
	}
	if _, err := allowing.Run(t.Context(), "printf ok", dir, 5); err != nil {
		t.Fatalf("marker-free runner must not inherit another policy's markers: %v", err)
	}
}

// With no policy carrying the old hardcoded paths, commands touching them
// are not denied: the shared package holds no host namespace of its own.
func TestNoHostNamespaceProtectedByDefault(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}})

	result, err := runner.Run(t.Context(), "printf '%s' '~/.mcp-ai-helper/config.yaml'", dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(result.StdoutTail, "\n"); !strings.Contains(joined, "mcp-ai-helper") {
		t.Fatalf("host path must be plain data without policy, got %q", joined)
	}
}

// The default denial message names no host actions: remediation text is the
// host's to supply through DenyMessage.
func TestDefaultDenyMessageIsHostNeutral(t *testing.T) {
	dir := t.TempDir()
	protected := filepath.Join(dir, "protected.yaml")
	runner := NewRunner(config.CommandPolicy{
		AllowedCWDs:         []string{dir},
		ProtectedConfigPath: protected,
	})

	_, err := runner.Run(t.Context(), "cat "+protected, dir, 5)
	if err == nil {
		t.Fatal("protected path must still be denied")
	}
	for _, host := range []string{"config action=", "option_set", "needs_user_action", "mcp-ai-helper"} {
		if strings.Contains(err.Error(), host) {
			t.Fatalf("default denial message carries host text %q: %v", host, err)
		}
	}
}

// Two runners with different policies deny different commands: the policy
// is instance state, not shared global state.
func TestIndependentRunnersUseOwnPolicies(t *testing.T) {
	dir := t.TempDir()
	first := NewRunner(config.CommandPolicy{
		AllowedCWDs:      []string{dir},
		ProtectedMarkers: []string{"marker-one"},
	})
	second := NewRunner(config.CommandPolicy{
		AllowedCWDs:      []string{dir},
		ProtectedMarkers: []string{"marker-two"},
	})

	if _, err := first.Run(t.Context(), "echo marker-one", dir, 5); err == nil {
		t.Fatal("first runner must deny its own marker")
	}
	if _, err := first.Run(t.Context(), "echo marker-two", dir, 5); err != nil {
		t.Fatalf("first runner must not deny the second policy's marker: %v", err)
	}
	if _, err := second.Run(t.Context(), "echo marker-two", dir, 5); err == nil {
		t.Fatal("second runner must deny its own marker")
	}
}
