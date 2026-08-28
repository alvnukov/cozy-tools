package fileops

import (
	"sync"
	"testing"
)

// CreateIfAbsent used to be Stat-then-Write, so two concurrent callers could
// both observe "absent" and both write — the loser's file silently replaced.
// The create has to be exclusive at the syscall level: exactly one caller
// may ever see ok.
func TestCreateIfAbsentIsExclusive(t *testing.T) {
	dir := t.TempDir()
	const goroutines = 32
	start := make(chan struct{})
	results := make(chan ReplaceResult, goroutines)

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := CreateIfAbsent(CreateIfAbsentRequest{
				RepoPath: dir,
				Path:     "claim.txt",
				Content:  "first writer wins",
			})
			if err != nil {
				t.Error(err)
				return
			}
			results <- result
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	created := 0
	for result := range results {
		if result.Status == "ok" {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created %d times, want exactly one exclusive create", created)
	}
}
