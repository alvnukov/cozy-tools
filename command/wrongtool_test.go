package command

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestShellSourceWritesAreRefused(t *testing.T) {
	repo := "/Users/zol/src/mcp-ai-helper"
	refused := map[string]string{
		"apply_patch heredoc":  "apply_patch <<'PATCH'\n*** Begin Patch\n*** Add File: internal/x.go\nPATCH",
		"redirect into source": "printf 'package x\\n' > internal/thing.go",
		"append to source":     "echo '- note' >> README.md",
		"tee into source":      "printf 'x' | tee internal/config/schema.go",
		"sed in place":         "sed -i '' 's/old/new/' internal/command/runner.go",
		"absolute repo path":   "printf 'x' > " + filepath.Join(repo, "internal", "thing.go"),
		"chained write":        "go build ./... && printf 'x' > cmd/main.go",
	}
	for name, cmd := range refused {
		err := rejectShellSourceWrite(cmd, repo)
		if err == nil {
			t.Errorf("%s: command was allowed to write source through the shell", name)
			continue
		}
		if !strings.Contains(err.Error(), "edit action=replace") {
			t.Errorf("%s: denial does not name the tool to use instead: %v", name, err)
		}
	}
}

func TestOrdinaryCommandsAreNotRefused(t *testing.T) {
	repo := "/Users/zol/src/mcp-ai-helper"
	allowed := map[string]string{
		"stderr redirect":     "go test ./... 2>&1 | tail -40",
		"report to tmp":       "gh run view 123 --json jobs > /tmp/run.json",
		"formatter":           "gofmt -w internal/command/runner.go",
		"read":                "sed -n '1,40p' internal/command/runner.go",
		"search":              "grep -rn 'apply_patch_like' internal",
		"absolute outside":    "printf 'x' > /var/tmp/scratch.go",
		"variable target":     "printf 'x' > \"$OUT.go\"",
		"non-source redirect": "go test ./... > results.txt",
		"tee to tmp":          "make quality | tee /tmp/quality.log",
		"sed without -i":      "sed 's/a/b/' internal/command/runner.go",
	}
	for name, cmd := range allowed {
		if err := rejectShellSourceWrite(cmd, repo); err != nil {
			t.Errorf("%s: ordinary command refused: %v", name, err)
		}
	}
}

func TestSourceWriteDenialReachesTheRunner(t *testing.T) {
	repoPath := t.TempDir()
	runner := waitTestRunner(t, repoPath)

	_, err := runner.RunInRepo(t.Context(), "printf 'package broken\\n' > pkg.go", repoPath, "", 5)
	if err == nil {
		t.Fatal("runner executed a shell write into repository source")
	}
	if !strings.Contains(err.Error(), "policy_denied") {
		t.Fatalf("denial = %v", err)
	}
	if _, statErr := filepath.Abs(repoPath); statErr != nil {
		t.Fatal(statErr)
	}
}
