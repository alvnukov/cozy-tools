package command

import (
	"strings"
	"testing"
)

func TestFailureMarkersRecognizeToolchainFailures(t *testing.T) {
	cases := map[string]string{
		"go test case":     "--- FAIL: TestThing (0.00s)",
		"go test package":  "FAIL\tgithub.com/alvnukov/mcp-ai-helper/internal/mcp\t301.878s",
		"go compile":       "internal/pipeline/pipeline_test.go:358:40: string literal not terminated",
		"golangci finding": "internal/command/runner.go:747:2: singleCaseSwitch: should rewrite switch (gocritic)",
		"lean":             "Proofs/QMClosure/Probe.lean:12:0: error: unknown identifier",
		"cargo":            "error[E0308]: mismatched types",
		"rustc plain":      "error: could not compile `happ`",
		"pytest":           "FAILED tests/test_thing.py::test_case - AssertionError",
	}
	for name, line := range cases {
		if got := failureMarkers([]string{line}); len(got) != 1 {
			t.Errorf("%s: %q was not recognized as a failure", name, line)
		}
	}
}

func TestFailureMarkersIgnoreOrdinaryOutput(t *testing.T) {
	quiet := []string{
		"ok  \tgithub.com/alvnukov/mcp-ai-helper/internal/command\t1.126s",
		"PASS",
		"=== RUN   TestThing",
		"no test files",
		"Compiling happ v1.2.1",
		"error handling is covered by the table above",
		"2 errors were fixed in this release",
		"internal/command/runner.go is formatted",
		"warning: unused import",
	}
	if got := failureMarkers(quiet); len(got) != 0 {
		t.Fatalf("ordinary output reported as failure: %#v", got)
	}
}

func TestFailureMarkersAreBounded(t *testing.T) {
	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, "--- FAIL: TestThing (0.00s)")
	}
	if got := failureMarkers(lines); len(got) != maxFailureMarkers {
		t.Fatalf("markers = %d, want %d", len(got), maxFailureMarkers)
	}
}

func TestMaskedFailureMarkersOnlyContradictASuccessfulExit(t *testing.T) {
	lines := []string{"--- FAIL: TestThing (0.00s)"}
	if got := maskedFailureMarkers(1, lines); got != nil {
		t.Fatalf("a non-zero exit already says it failed, got %#v", got)
	}
	if got := maskedFailureMarkers(0, lines); len(got) != 1 {
		t.Fatalf("markers under exit 0 = %#v", got)
	}
}

// A pipeline exits with the status of its last stage, so this is how a real
// failure reaches the caller as success.
func TestRunReportsAFailureAPipelineSwallowed(t *testing.T) {
	repoPath := t.TempDir()
	runner := waitTestRunner(t, repoPath)

	result, err := runner.RunInRepo(t.Context(),
		"printf -- '--- FAIL: TestThing (0.00s)\\nFAIL\\tpkg/thing\\t0.1s\\n' | tail -5", repoPath, "", 30)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.Status != "ok" {
		t.Fatalf("pipeline did not mask the failure: status %q exit %d", result.Status, result.ExitCode)
	}
	if len(result.FailureMarkers) == 0 {
		t.Fatal("a masked test failure was not reported")
	}
	if !strings.Contains(result.FailureMarkers[0], "--- FAIL") {
		t.Fatalf("failure markers = %#v", result.FailureMarkers)
	}

	fetched, err := runner.FilterHistory(result.CommandID, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(fetched.FailureMarkers) == 0 {
		t.Fatal("get did not report the masked failure the run reported")
	}
}

func TestRunLeavesFailureMarkersEmptyForACleanCommand(t *testing.T) {
	repoPath := t.TempDir()
	runner := waitTestRunner(t, repoPath)

	result, err := runner.RunInRepo(t.Context(), "printf 'ok  \\tpkg/thing\\t0.1s\\n'", repoPath, "", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.FailureMarkers) != 0 {
		t.Fatalf("clean output reported failure markers: %#v", result.FailureMarkers)
	}
}
