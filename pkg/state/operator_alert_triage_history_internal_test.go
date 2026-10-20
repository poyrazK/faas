package state_test

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

func TestOperatorAlertTriageHistoryMemoryRetentionAndMalformedReceipt(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	rule, series := strings.Repeat("a", 64), strings.Repeat("b", 64)
	episode := state.OperatorAlertEpisode{RuleKey: rule, SeriesID: series, Name: "FaasApiDown", Severity: "critical", StartedAt: now.Add(-time.Minute)}
	episode.ID = state.OperatorAlertEpisodeID(rule, series, episode.StartedAt)
	if err := store.RecordOperatorAlertSnapshot(ctx, state.OperatorAlertSnapshot{ObservedAt: now, Rules: []state.OperatorAlertRuleObservation{{Key: rule, EvaluatedAt: now, EpisodeIDs: []string{episode.ID}}}, Episodes: []state.OperatorAlertEpisode{episode}}); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if _, err := store.MutateOperatorAlertTriage(ctx, state.OperatorAlertTriageMutation{EpisodeID: episode.ID, ActorID: "11111111-1111-4111-8111-111111111111", Action: "acknowledge", ExpectedRevision: int64(i), Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	(*store.Mu()).Lock()
	(*store.AuditLog()) = append((*store.AuditLog())[:1], (*store.AuditLog())[2:]...)
	(*store.Mu()).Unlock()
	page, err := store.OperatorAlertTriageHistory(ctx, state.OperatorAlertTriageHistoryFilter{ID: episode.ID, Limit: 3})
	if err != nil || page.Coverage.State != "gaps" || page.Coverage.MissingEntries != 1 || len(page.Items) != 2 {
		t.Fatalf("retention gap: %+v %v", page, err)
	}
	(*store.Mu()).Lock()
	original := append([]byte(nil), (*store.AuditLog())[0].Data...)
	(*store.Mu()).Unlock()
	var payload map[string]any
	if err := json.Unmarshal(original, &payload); err != nil {
		t.Fatal(err)
	}
	payload["triage"].(map[string]any)["acknowledged"] = nil
	invalid, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	(*store.Mu()).Lock()
	(*store.AuditLog())[0].Data = invalid
	(*store.Mu()).Unlock()
	if _, err := store.OperatorAlertTriageHistory(ctx, state.OperatorAlertTriageHistoryFilter{ID: episode.ID, Limit: 3}); err == nil {
		t.Fatal("malformed receipt was exported")
	}
}
