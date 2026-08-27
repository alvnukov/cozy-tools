package vars

import (
	"strings"
	"testing"
)

func TestSubstituteReplacesKnownNames(t *testing.T) {
	got, err := Substitute("printf '%s' {{MSG}} {{other-name}}", map[string]string{"MSG": "hi'there", "other-name": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "printf '%s' hi'there b" {
		t.Fatalf("got %q", got)
	}
}

func TestSubstituteIsLiteralAndNotRecursive(t *testing.T) {
	got, err := Substitute("{{A}}", map[string]string{"A": "{{B}}", "B": "no"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "{{B}}" {
		t.Fatalf("a value must not be re-substituted, got %q", got)
	}
}

func TestSubstituteEscapesLiteralBraces(t *testing.T) {
	got, err := Substitute("a {{{{ b", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "a {{ b" {
		t.Fatalf("got %q", got)
	}
}

func TestSubstituteKeepsClosingBracesLiteral(t *testing.T) {
	got, err := Substitute("}} without opening {{X}}", map[string]string{"X": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "}} without opening x" {
		t.Fatalf("got %q", got)
	}
}

func TestSubstituteUnknownNameFailsClosedAndTeaches(t *testing.T) {
	_, err := Substitute("echo {{MISSING}}", map[string]string{"KNOWN": "1"})
	if err == nil {
		t.Fatal("want fail-closed error")
	}
	for _, want := range []string{"{{MISSING}}", "has no value", "Known variables: KNOWN", "vars"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestSubstituteInvalidNameFailsClosed(t *testing.T) {
	_, err := Substitute("echo {{1bad}}", nil)
	if err == nil || !strings.Contains(err.Error(), "not a valid variable name") {
		t.Fatalf("err = %v, want invalid-name error", err)
	}
}

func TestSubstituteUnterminatedFailsClosed(t *testing.T) {
	_, err := Substitute("echo {{open", nil)
	if err == nil || !strings.Contains(err.Error(), "unterminated") {
		t.Fatalf("err = %v, want unterminated error", err)
	}
}

func TestSubstitutePassesPlainTextThrough(t *testing.T) {
	got, err := Substitute("plain 'quote' \"dquote\" $dollar", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "plain 'quote' \"dquote\" $dollar" {
		t.Fatalf("got %q", got)
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"a", "A_b-c1", "_x"} {
		if !ValidateName(ok) {
			t.Fatalf("ValidateName(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", "1a", "a b", "a.b"} {
		if ValidateName(bad) {
			t.Fatalf("ValidateName(%q) = true", bad)
		}
	}
}
