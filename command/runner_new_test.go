package command

import (
	"runtime"
	"testing"
)

func TestShellBinUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows-specific test")
	}
	if got := shellBin(); got != "/bin/sh" {
		t.Fatalf("shellBin() = %q, want /bin/sh", got)
	}
}

func TestShellBinWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("unix-specific test")
	}
	if got := shellBin(); got != "cmd" {
		t.Fatalf("shellBin() = %q, want cmd", got)
	}
}

func TestShellArgsUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows-specific test")
	}
	args := shellArgs("echo hello")
	if len(args) != 2 || args[0] != "-lc" || args[1] != "echo hello" {
		t.Fatalf("shellArgs = %#v, want [-lc \"echo hello\"]", args)
	}
}

func TestShellArgsWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("unix-specific test")
	}
	args := shellArgs("dir")
	if len(args) != 2 || args[0] != "/c" || args[1] != "dir" {
		t.Fatalf("shellArgs = %#v, want [/c dir]", args)
	}
}

func TestRedactExpandedPatterns(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"x-api-key header", "x-api-key: sk-abc123", "x-api-key: [REDACTED]"},
		{"X-Api-Key case insensitive", "X-API-KEY: secret123", "X-API-KEY: [REDACTED]"},
		{"private-token header", "private-token: glpat-xyz", "private-token: [REDACTED]"},
		{"PRIVATE-TOKEN case", "PRIVATE-TOKEN: tok", "PRIVATE-TOKEN: [REDACTED]"},
		{"api_key assignment", "api_key=abc", "api_key=[REDACTED]"},
		{"api_key colon", `api_key: "xyz"`, `api_key: "[REDACTED]"`},
		{"token colon", `token: "ghp_xxx"`, `token: "[REDACTED]"`},
		{"bearer auth", "Authorization: bearer tok123", "Authorization: bearer [REDACTED]"},
		{"secret field", `secret: "mysecret"`, `secret: "[REDACTED]"`},
		{"password field", `password: "pass123"`, `password: "[REDACTED]"`},
		{"AWS AKIA key", "AKIAIOSFODNN7EXAMPLE", "[REDACTED]"},
		{"AWS ASIA key", "ASIATESTKEY1234567", "[REDACTED]"},
		{"GitHub pat", "ghp_abcdefghijklmnopqrstuvwxyz1234567890", "[REDACTED]"},
		{"GitHub oauth", "gho_XYZ1234567890ABCDEFGHIJKLMNOPQRSTUVWXYZ", "[REDACTED]"},
		{"JWT token", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8", "[REDACTED]"},
		{"multiple secrets", "AKIAIOSFODNN7EXAMPLE and password: secret123", "[REDACTED] and password: [REDACTED]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redact(tt.input)
			if got != tt.want {
				t.Fatalf("redact(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRedactPreservesNonSensitiveText(t *testing.T) {
	input := "Build succeeded\nexit_code: 0\nok  github.com/foo/bar"
	got := redact(input)
	if got != input {
		t.Fatalf("redact altered benign text: %q", got)
	}
}
