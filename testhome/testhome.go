// Package testhome gives a test binary a home directory of its own.
//
// Several parts of the helper fall back to $HOME when no path is configured:
// command history lands in ~/.mcp-ai-helper, and so do the project store and the
// web cache. A test that builds a Runner without naming a log directory
// therefore reads — and, through History's retention pass, deletes from — the
// history of whoever is running the tests.
//
// That is wrong twice over. It mutates real state that no test asked for, and it
// makes the suite's runtime a function of how much the developer has been using
// the tool: loading an index of a few thousand records costs a second per Runner
// built, and the mcp package builds one in almost every test.
//
// Call Use from TestMain and the whole binary gets an empty directory instead.
package testhome

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Use points $HOME at a fresh directory for the duration of the test binary and
// returns m.Run's exit code, so TestMain can be a single line:
//
//	func TestMain(m *testing.M) { os.Exit(testhome.Use(m)) }
func Use(m *testing.M) (exitCode int) {
	home, err := os.MkdirTemp("", "mcp-ai-helper-testhome-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "testhome: %v\n", err)
		return 1
	}
	defer func() {
		if err := os.RemoveAll(home); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "testhome: remove %s: %v\n", home, err)
			if exitCode == 0 {
				exitCode = 1
			}
		}
	}()

	// elan keeps its Lean toolchains under $HOME/.elan unless ELAN_HOME says
	// otherwise, so moving $HOME hides an already-installed toolchain and the
	// Lean-backed tests sit there re-downloading it. Pin ELAN_HOME to the real
	// one first. The helper's own state still goes to the temp home; only the
	// toolchain store, which no test writes to, stays shared.
	if _, pinned := os.LookupEnv("ELAN_HOME"); !pinned {
		if realHome, err := os.UserHomeDir(); err == nil {
			if err := os.Setenv("ELAN_HOME", filepath.Join(realHome, ".elan")); err != nil {
				fmt.Fprintf(os.Stderr, "testhome: set ELAN_HOME: %v\n", err)
				return 1
			}
			defer func() { _ = os.Unsetenv("ELAN_HOME") }()
		}
	}

	// os.UserHomeDir reads $HOME on unix and USERPROFILE on Windows; setting
	// both keeps the redirect working wherever the suite runs.
	for _, name := range []string{"HOME", "USERPROFILE"} {
		previous, had := os.LookupEnv(name)
		if err := os.Setenv(name, home); err != nil {
			fmt.Fprintf(os.Stderr, "testhome: set %s: %v\n", name, err)
			return 1
		}
		defer func() {
			if had {
				_ = os.Setenv(name, previous)
				return
			}
			_ = os.Unsetenv(name)
		}()
	}

	exitCode = m.Run()
	return exitCode
}
