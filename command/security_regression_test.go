package command

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alvnukov/cozy-tools/config"
	"github.com/alvnukov/cozy-tools/security"
)

func TestReadEntriesRejectsRecordPathOutsideRecordsDirectory(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.jsonl")
	entry := indexEntry{CommandID: "hostile", File: "../../victim.json"}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := readEntriesFile(indexPath); err == nil {
		t.Fatal("expected an out-of-tree record path to be rejected")
	}
}

func TestReadJSONRecordRejectsOversizedDecompressedData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bomb.json.gz")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := gzip.NewWriter(file)
	record := Record{Combined: []string{strings.Repeat("x", 17<<20)}}
	if err := json.NewEncoder(writer).Encode(record); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := readJSONRecord(path); err == nil {
		t.Fatal("expected oversized decompressed record to be rejected")
	}
}

func TestRunnerMasksSecretInSubstitutionError(t *testing.T) {
	dir := t.TempDir()
	mask := security.NewMask()
	mask.Add("super-secret-value")
	runner := NewRunnerWithMask(config.CommandPolicy{
		AllowedCWDs:           []string{dir},
		DefaultTimeoutSeconds: 1,
		MaxOutputBytes:        1000,
		MaxLines:              20,
	}, mask)
	ctx := ContextWithExec(t.Context(), Exec{Vars: map[string]string{"KNOWN": "value"}})

	_, err := runner.Run(ctx, "printf super-secret-value {{UNKNOWN}}", dir, 1)
	if err == nil {
		t.Fatal("expected substitution error")
	}
	if strings.Contains(err.Error(), "super-secret-value") {
		t.Fatalf("substitution error leaked secret: %v", err)
	}
}

func TestRewriteIndexDoesNotFollowPredictableTempSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated privileges")
	}
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "index.jsonl")
	victimPath := filepath.Join(dir, "victim.txt")
	const victim = "must remain intact"
	if err := os.WriteFile(victimPath, []byte(victim), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victimPath, indexPath+".tmp"); err != nil {
		t.Fatal(err)
	}

	if err := rewriteIndex(indexPath, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(victimPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != victim {
		t.Fatalf("predictable temp symlink modified victim: %q", got)
	}
}

func TestAcquireIndexLockRejectsSymlinkOutsideRoot(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" || runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("platform does not provide the supported flock implementation")
	}
	root := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim.lock")
	if err := os.WriteFile(victim, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(root, indexLockName)); err != nil {
		t.Fatal(err)
	}

	lock, err := acquireIndexLock(root)
	if err == nil {
		lock.release()
		t.Fatal("index lock followed a symlink outside the command log root")
	}
}
