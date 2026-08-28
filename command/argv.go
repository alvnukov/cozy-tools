package command

import (
	"context"
	"errors"
	"sort"
	"strings"
)

// RunArgv executes argv directly, without a shell: every element is passed to
// the program as data and is never parsed as shell source. It is the
// injection-safe value channel promised by the package trust model: caller
// values travel as argv elements, environment entries (Exec.Env), or stdin
// (Exec.Stdin), and quotes, separators, substitutions and option-like
// strings arrive byte for byte.
//
// The quoted argv is recorded as the command display for history, masked by
// the runner's secret masks like any other command text. Per-execution
// environment entries already present in the context's Exec are preserved,
// so hosts can combine RunArgv with Exec.Env; template variables are not --
// mixing the argv channel with Exec.Vars is rejected, because silently
// dropping the variables would hide a migration mistake.
func (r *Runner) RunArgv(ctx context.Context, argv []string, cwd string, timeoutSeconds int) (Result, error) {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return Result{}, errors.New("argv must name a program to run")
	}
	e := execFromContext(ctx)
	for key := range e.Env {
		if key == "" || strings.ContainsAny(key, "=\x00\n") {
			return Result{}, errors.New("execution env keys must be non-empty and contain no '=', NUL, or newline")
		}
	}
	e.Argv = argv
	return r.RunFiltered(ContextWithExec(ctx, e), quoteArgv(argv), cwd, timeoutSeconds, Filter{})
}

// quoteArgv renders argv as a single-line, shell-style display string for
// history and results. It is display only: nothing quoted here is ever
// parsed by a shell.
func quoteArgv(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		switch {
		case arg == "":
			quoted[i] = "''"
		case isPlainArg(arg):
			quoted[i] = arg
		default:
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
		}
	}
	return strings.Join(quoted, " ")
}

// isPlainArg reports whether arg needs no quoting for display.
func isPlainArg(arg string) bool {
	for _, r := range arg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("._-/:=,", r):
		default:
			return false
		}
	}
	return true
}

// envSlice renders per-execution environment entries deterministically.
func envSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(env))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}
