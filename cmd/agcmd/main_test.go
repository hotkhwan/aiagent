package main

import (
	"sync"
	"testing"
)

// Two missions created at the same moment — the PM dispatching to two
// workspaces, or a phone and a desktop — must get different run ids. The id
// names the run's record and log file; a shared id lets one run overwrite the
// other's record.
func TestConcurrentRunsGetDistinctIDs(t *testing.T) {
	t.Setenv("AGCMD_RUNS", t.TempDir())

	const n = 20
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := nextRunID()
			if err != nil {
				t.Errorf("nextRunID: %v", err)
			}
			ids[i] = id
		}()
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("run id %s was handed out twice: %v", id, ids)
		}
		seen[id] = true
	}
}
