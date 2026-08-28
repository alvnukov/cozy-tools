package command

// The tests in this file pin the trust model documented in doc.go. They are
// contract tests: each asserts a property the documentation promises,
// including the promises about what the package deliberately does not do.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alvnukov/cozy-tools/config"
	"github.com/alvnukov/cozy-tools/vars"
)

// A guard that scans command text stops the accidental heredoc and, just as
// reliably, lets an equivalent indirect form through. That gap is the
// documented line between advisory policy and a security boundary.
func TestTrustModelShellSourceGuardIsAdvisory(t *testing.T) {
	repo := t.TempDir()

	direct := "echo draft > notes.md"
	if err := rejectShellSourceWrite(direct, repo); err == nil {
		t.Fatal("direct redirect into repository source must be denied")
	}

	indirect := `f=notes.md; printf draft >> "$f"`
	if err := rejectShellSourceWrite(indirect, repo); err != nil {
		t.Fatalf("guard must stay advisory; the documented contract allows indirect writes: %v", err)
	}
}

func TestTrustModelProtectedConfigGuardIsAdvisory(t *testing.T) {
	policy := config.CommandPolicy{ProtectedConfigPath: "/etc/host/config.yaml"}
	direct := "cat /etc/host/config.yaml"
	if err := rejectProtectedCommand(direct, policy); err == nil {
		t.Fatal("direct protected-config reference must be denied")
	}

	indirect := "d=/etc/host; cat $d/config.yaml"
	if err := rejectProtectedCommand(indirect, policy); err != nil {
		t.Fatalf("guard must stay advisory; the documented contract allows indirect reads: %v", err)
	}
}

// Substitution is literal, and that is exactly why a substituted value is
// shell source rather than data. This is the legacy raw channel the trust
// model tells untrusted values to stay out of.
func TestTrustModelSubstitutedValuesBecomeShellSource(t *testing.T) {
	const hostile = `a'; touch /tmp/cozy-tools-pwned; echo '`
	got, err := vars.Substitute("echo {{V}}", map[string]string{"V": hostile})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, hostile) {
		t.Fatalf("substitution = %q; the raw channel is documented as literal insertion", got)
	}
}

// Containment is a real guarantee, and it is about where a command starts:
// the requested working directory is checked against resolved real paths,
// so a symlinked cwd cannot point outside the allowlist.
func TestTrustModelWorkingDirectoryIsContainedToRealPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges")
	}
	allowed := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{allowed}})

	if _, err := runner.safeCWD(link); err == nil {
		t.Fatal("a cwd resolving outside allowed_cwds must be rejected")
	}
	if _, err := runner.safeCWD(allowed); err != nil {
		t.Fatalf("allowed cwd rejected: %v", err)
	}
}
