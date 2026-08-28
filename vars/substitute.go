// Package vars substitutes {{name}} references with literal values before a
// command string reaches the shell.
//
// The point of the substitution is to remove nested quoting: a value travels
// as a JSON field, not as quoted text inside a command string, and lands in
// the command after JSON decoding but before the shell parses anything.
// Substitution is literal — a value is never re-evaluated, and braces inside
// a value are not substituted again.
//
// # Raw shell channel
//
// Because substitution happens before the shell parses the command, a value
// is inserted as shell source, not as data: quotes, semicolons, $( ) and
// backticks in a value change the command that runs. "Never re-evaluated"
// means the value is not substituted again; it does not mean the shell
// cannot interpret it.
//
// This is the legacy raw channel. Hosts that accept values from outside
// their trust boundary must not pass those values through Substitute. The
// migration contract: an injection-safe channel, where the value travels as
// an environment entry or an argv element and never becomes shell source, is
// the supported path; raw substitution stays available while it is being
// adopted, is documented as unsafe for untrusted values, and is removed or
// gated before the module's first stable version.
package vars

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// namePattern matches one substitutable name: a letter or underscore, then
// letters, digits, underscores or dashes, at most 64 characters.
var namePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)

// ValidateName reports whether name may be used as a variable name.
func ValidateName(name string) bool {
	return namePattern.MatchString(name)
}

// Substitute replaces every {{name}} reference in s with values[name].
//
// The contract is fail-closed: a reference whose name has no value is an
// error, because silently passing the braces through to the shell is how a
// missing value turns into a quoting accident. {{{{ is the escape for a
// literal {{; }} outside a reference is literal already.
func Substitute(s string, values map[string]string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		rest := s[i:]
		if strings.HasPrefix(rest, "{{{{") {
			b.WriteString("{{")
			i += 4
			continue
		}
		if !strings.HasPrefix(rest, "{{") {
			b.WriteByte(s[i])
			i++
			continue
		}
		end := strings.Index(rest[2:], "}}")
		if end < 0 {
			return "", fmt.Errorf("unterminated {{ in template at offset %d: %q; close the reference with }} or escape a literal {{ as {{{{", i, rest)
		}
		name := rest[2 : 2+end]
		if !ValidateName(name) {
			return "", referenceError(s, name, values, "is not a valid variable name (want ^[A-Za-z_][A-Za-z0-9_-]{0,63}$)")
		}
		value, ok := values[name]
		if !ok {
			return "", referenceError(s, name, values, "has no value")
		}
		b.WriteString(value)
		i += 2 + end + 2
	}
	return b.String(), nil
}

// referenceError teaches the way out instead of reporting a bare name.
func referenceError(template string, name string, values map[string]string, problem string) error {
	known := make([]string, 0, len(values))
	for k := range values {
		known = append(known, k)
	}
	sort.Strings(known)
	knownText := "(none)"
	if len(known) > 0 {
		knownText = strings.Join(known, ", ")
	}
	return fmt.Errorf("template reference {{%s}} %s. Known variables: %s. Pass the value in vars (or reference a secret_handle by its name); write {{{{ for a literal {{. Template: %q", name, problem, knownText, template)
}
