package state

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestOperatorAlertTriageHistoryMemoryRetentionAndMalformedReceipt(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	rule, series := strings.Repeat("a", 64), strings.Repeat("b", 64)
	episode := OperatorAlertEpisode{RuleKey: rule, SeriesID: series, Name: "FaasApiDown", Severity: "critical", StartedAt: now.Add(-time.Minute)}
	episode.ID = OperatorAlertEpisodeID(rule, series, episode.StartedAt)
	if err := store.RecordOperatorAlertSnapshot(ctx, OperatorAlertSnapshot{ObservedAt: now, Rules: []OperatorAlertRuleObservation{{Key: rule, EvaluatedAt: now, EpisodeIDs: []string{episode.ID}}}, Episodes: []OperatorAlertEpisode{episode}}); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if _, err := store.MutateOperatorAlertTriage(ctx, OperatorAlertTriageMutation{EpisodeID: episode.ID, ActorID: "11111111-1111-4111-8111-111111111111", Action: "acknowledge", ExpectedRevision: int64(i), Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	store.mu.Lock()
	store.auditLog = append(store.auditLog[:1], store.auditLog[2:]...)
	store.mu.Unlock()
	page, err := store.OperatorAlertTriageHistory(ctx, OperatorAlertTriageHistoryFilter{ID: episode.ID, Limit: 3})
	if err != nil || page.Coverage.State != "gaps" || page.Coverage.MissingEntries != 1 || len(page.Items) != 2 {
		t.Fatalf("retention gap: %+v %v", page, err)
	}
	store.mu.Lock()
	original := append([]byte(nil), store.auditLog[0].Data...)
	store.mu.Unlock()
	var payload map[string]any
	if err := json.Unmarshal(original, &payload); err != nil {
		t.Fatal(err)
	}
	payload["triage"].(map[string]any)["acknowledged"] = nil
	invalid, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.auditLog[0].Data = invalid
	store.mu.Unlock()
	if _, err := store.OperatorAlertTriageHistory(ctx, OperatorAlertTriageHistoryFilter{ID: episode.ID, Limit: 3}); err == nil {
		t.Fatal("malformed receipt was exported")
	}
}
