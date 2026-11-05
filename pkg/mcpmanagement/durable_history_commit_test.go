// adr: 20261022161757
package mcpmanagement

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStateStoreHistoryBatchPreservesCommitOrdering(t *testing.T) {
	p := &countingPersistence{}
	s := &StateStore{persist: p, records: map[string]json.RawMessage{}}
	batch := map[string]any{"plans/grant": map[string]int{"attempted": 1}, "actions/grant": map[string]int{"unknown": 1}}
	if err := s.updateMany(batch); err != nil || p.commits != 1 {
		t.Fatalf("related records were not committed once: %v %d", err, p.commits)
	}
	if len(s.records) != 2 {
		t.Fatal("batch lost related history")
	}
	if err := s.updateMany(batch); err != nil || p.commits != 1 {
		t.Fatalf("unchanged batch rewrote state: %v %d", err, p.commits)
	}
	before := string(s.records["plans/grant"])
	if err := s.updateMany(map[string]any{"plans/grant": 2, "bad": make(chan int)}); err == nil || string(s.records["plans/grant"]) != before || p.commits != 1 {
		t.Fatal("marshal failure partially applied the batch")
	}
	active := oauthSnapshot{Grants: map[string]storedGrant{"grant": {}}}
	if err := s.updateSnapshot("oauth", active, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.updateSnapshot("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}, 1); err != nil {
		t.Fatal(err)
	}
	if len(s.records["actions/grant"]) == 0 || len(s.records["plans/grant"]) == 0 {
		t.Fatal("stale OAuth snapshot purged live history")
	}
	if err := s.updateSnapshot("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}, 3); err != nil {
		t.Fatal(err)
	}
	if len(s.records["actions/grant"]) != 0 || len(s.records["plans/grant"]) != 0 {
		t.Fatal("current revocation retained grant history")
	}
}

func TestStateStoreAgentHistorySurvivesReplayExpiry(t *testing.T) {
	p := &countingPersistence{}
	s := &StateStore{persist: p, records: map[string]json.RawMessage{}}
	grant := "agent-key/headless"
	id := strings.Repeat("a", 64)
	until := time.Now().Add(time.Hour)
	history := actionHistorySnapshot{Version: 1, Items: map[string]actionRecord{id: {RetainedUntil: until}}}
	if err := s.updateMany(map[string]any{workloadActionsNamespace + grant: history, namedServiceAbortOutcomeNamespace + grant: map[string]int{"reference": 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}); err != nil {
		t.Fatal(err)
	}
	if len(s.records[workloadActionsNamespace+grant]) == 0 || len(s.records[namedServiceAbortOutcomeNamespace+grant]) == 0 {
		t.Fatal("OAuth maintenance removed retained headless recovery")
	}
	history.Items[id] = actionRecord{RetainedUntil: time.Now().Add(-time.Minute)}
	if err := s.update(workloadActionsNamespace+grant, history); err != nil {
		t.Fatal(err)
	}
	if err := s.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}); err != nil {
		t.Fatal(err)
	}
	if len(s.records[workloadActionsNamespace+grant]) != 0 || len(s.records[namedServiceAbortOutcomeNamespace+grant]) != 0 {
		t.Fatal("expired headless recovery was not pruned")
	}
}
