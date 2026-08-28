package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alvnukov/cozy-tools/config"
	"github.com/alvnukov/cozy-tools/security"
)

// RunArgv is the injection-safe execution path: values travel as argv
// elements or environment entries and are never parsed by a shell. Every
// hostile shape below must arrive as data, byte for byte.
func TestRunArgvKeepsHostileValuesAsData(t *testing.T) {
	dir := t.TempDir()
	pwned := filepath.Join(dir, "pwned")
	hostile := "a'; touch " + pwned + "; echo '"
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}})

	result, err := runner.RunArgv(t.Context(), []string{"printf", "%s", hostile}, dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(result.StdoutTail, "\n"); !strings.Contains(joined, hostile) {
		t.Fatalf("hostile value must arrive verbatim as data, got %q", joined)
	}
	if _, err := os.Stat(pwned); err == nil {
		t.Fatal("hostile value escaped its argv slot and executed")
	}
}

func TestRunArgvOptionLikeAndSubstitutionValuesStayData(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}})
	for _, hostile := range []string{
		"--rm-rf",
		"$(touch pwned)",
		"`touch pwned`",
		"a; touch pwned",
		"line1\nline2; rm -rf /",
		"$(echo hi)",
	} {
		result, err := runner.RunArgv(t.Context(), []string{"printf", "%s", hostile}, dir, 10)
		if err != nil {
			t.Fatalf("value %q: %v", hostile, err)
		}
		if joined := strings.Join(result.StdoutTail, "\n"); !strings.Contains(joined, hostile) {
			t.Fatalf("value %q must stay data, got %q", hostile, joined)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Fatal("a substitution-shaped value executed")
	}
}

// Environment entries reach the process verbatim; a trusted script may read
// them as variables, which is the migration path off raw interpolation.
func TestRunArgvEnvChannelCarriesValues(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}})
	ctx := ContextWithExec(t.Context(), Exec{Env: map[string]string{"CT002_TOKEN": "sec;ret`x`$(y)"}})

	result, err := runner.RunArgv(ctx, []string{"sh", "-c", `printf %s "$CT002_TOKEN"`}, dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(result.StdoutTail, "\n"); joined != "sec;ret`x`$(y)" {
		t.Fatalf("env value must arrive verbatim, got %q", joined)
	}
	if strings.Contains(result.Command, "sec;ret") {
		t.Fatalf("env values must not appear in the recorded command: %q", result.Command)
	}
}

// Stdin is the third safe channel and works on the argv path too.
func TestRunArgvStdinChannel(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}})
	ctx := ContextWithExec(t.Context(), Exec{Stdin: "line1'; rm -rf /\nline2`x`\n"})

	result, err := runner.RunArgv(ctx, []string{"cat"}, dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(result.StdoutTail, "\n")
	if !strings.Contains(joined, "line1'; rm -rf /") || !strings.Contains(joined, "line2`x`") {
		t.Fatalf("stdin must arrive verbatim as data, got %q", joined)
	}
}

// Mixing the argv channel with template variables would silently drop the
// variables; the runner refuses instead.
func TestRunArgvRejectsTemplateVars(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{dir}})
	ctx := ContextWithExec(t.Context(), Exec{Vars: map[string]string{"V": "x"}})

	_, err := runner.RunArgv(ctx, []string{"true"}, dir, 10)
	if err == nil || !strings.Contains(err.Error(), "argv") {
		t.Fatalf("err = %v, want argv/vars conflict error", err)
	}
}

func TestRunArgvRequiresArgv(t *testing.T) {
	runner := NewRunner(config.CommandPolicy{AllowedCWDs: []string{t.TempDir()}})
	if _, err := runner.RunArgv(t.Context(), nil, t.TempDir(), 1); err == nil {
		t.Fatal("empty argv must be rejected")
	}
}

// Secrets in argv values are masked everywhere the raw shell path masks
// them: command display, output, and persisted history.
func TestRunArgvMasksSecretArgvValues(t *testing.T) {
	dir := t.TempDir()
	secret := "hunter2-exact-value"
	mask := security.NewMask()
	mask.Add(secret)
	runner := NewRunnerWithMask(config.CommandPolicy{AllowedCWDs: []string{dir}}, mask)

	result, err := runner.RunArgv(t.Context(), []string{"printf", "%s", secret}, dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(result.StdoutTail, "\n")
	if strings.Contains(joined, secret) || strings.Contains(result.Command, secret) {
		t.Fatalf("secret leaked into result: out=%q cmd=%q", joined, result.Command)
	}
}
