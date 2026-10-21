package state_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperatorAlertResponseTimesMemoryGapsAndReceiptBound(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	rule, series := strings.Repeat("a", 64), strings.Repeat("b", 64)
	e := state.OperatorAlertEpisode{RuleKey: rule, SeriesID: series, Name: "FaasApiDown", Severity: "critical", StartedAt: now.Add(-time.Minute)}
	e.ID = state.OperatorAlertEpisodeID(rule, series, e.StartedAt)
	if err := store.RecordOperatorAlertSnapshot(ctx, state.OperatorAlertSnapshot{ObservedAt: now, Rules: []state.OperatorAlertRuleObservation{{Key: rule, EvaluatedAt: now, EpisodeIDs: []string{e.ID}}}, Episodes: []state.OperatorAlertEpisode{e}}); err != nil {
		t.Fatal(err)
	}
	for i, action := range []string{"claim", "acknowledge"} {
		if _, err := store.MutateOperatorAlertTriage(ctx, state.OperatorAlertTriageMutation{EpisodeID: e.ID, ActorID: "11111111-1111-4111-8111-111111111111", Action: action, ExpectedRevision: int64(i), Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	(*store.Mu()).Lock()
	(*store.AuditLog()) = (*store.AuditLog())[1:]
	(*store.Mu()).Unlock()
	f := state.OperatorAlertResponseTimesFilter{Since: now.Add(-time.Hour), Now: now, CollectionEnabled: true}
	gap, err := store.OperatorAlertResponseTimes(ctx, f)
	if err != nil || gap.Episodes[0].ReceiptCoverage != "gaps" || gap.Episodes[0].FirstAcknowledgedAt != nil {
		t.Fatalf("gap: %+v %v", gap, err)
	}
	(*store.Mu()).Lock()
	receipt := (*store.AuditLog())[0]
	for len((*store.AuditLog())) <= api.ObsAlertResponseTimesReceiptsMax {
		receipt.ID = uuid.New()
		store.AppendAuditLogLocked(receipt)
	}
	(*store.Mu()).Unlock()
	if _, err := store.OperatorAlertResponseTimes(ctx, f); err == nil {
		t.Fatal("receipt limit silently sampled")
	}
}
