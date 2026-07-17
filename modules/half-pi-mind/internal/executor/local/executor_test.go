package local

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestLocalExecutorsIsolateRemoteBridges(t *testing.T) {
	first := New(&RemoteBridge{ActiveHand: func() string { return "hand-a" }})
	second := New(&RemoteBridge{ActiveHand: func() string { return "hand-b" }})
	args := json.RawMessage(`{}`)
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for _, exec := range []*LocalExecutor{first, second} {
		wg.Add(1)
		go func(exec *LocalExecutor) {
			defer wg.Done()
			result := exec.ExecuteTool(context.Background(), "select_hand", args)
			results <- result.Output
		}(exec)
	}
	wg.Wait()
	close(results)
	seen := map[string]bool{}
	for result := range results {
		if strings.Contains(result, "hand-a") {
			seen["hand-a"] = true
		}
		if strings.Contains(result, "hand-b") {
			seen["hand-b"] = true
		}
	}
	if !seen["hand-a"] || !seen["hand-b"] {
		t.Fatalf("bridges were not isolated: %+v", seen)
	}
}
