package state

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestOperatorAlertResponseTimesMemoryGapsAndReceiptBound(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	rule, series := strings.Repeat("a", 64), strings.Repeat("b", 64)
	e := OperatorAlertEpisode{RuleKey: rule, SeriesID: series, Name: "FaasApiDown", Severity: "critical", StartedAt: now.Add(-time.Minute)}
	e.ID = OperatorAlertEpisodeID(rule, series, e.StartedAt)
	if err := store.RecordOperatorAlertSnapshot(ctx, OperatorAlertSnapshot{ObservedAt: now, Rules: []OperatorAlertRuleObservation{{Key: rule, EvaluatedAt: now, EpisodeIDs: []string{e.ID}}}, Episodes: []OperatorAlertEpisode{e}}); err != nil {
		t.Fatal(err)
	}
	for i, action := range []string{"claim", "acknowledge"} {
		if _, err := store.MutateOperatorAlertTriage(ctx, OperatorAlertTriageMutation{EpisodeID: e.ID, ActorID: "11111111-1111-4111-8111-111111111111", Action: action, ExpectedRevision: int64(i), Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	store.mu.Lock()
	store.auditLog = store.auditLog[1:]
	store.mu.Unlock()
	f := OperatorAlertResponseTimesFilter{Since: now.Add(-time.Hour), Now: now, CollectionEnabled: true}
	gap, err := store.OperatorAlertResponseTimes(ctx, f)
	if err != nil || gap.Episodes[0].ReceiptCoverage != "gaps" || gap.Episodes[0].FirstAcknowledgedAt != nil {
		t.Fatalf("gap: %+v %v", gap, err)
	}
	store.mu.Lock()
	receipt := store.auditLog[0]
	for len(store.auditLog) <= api.ObsAlertResponseTimesReceiptsMax {
		receipt.ID = uuid.New()
		store.appendAuditLogLocked(receipt)
	}
	store.mu.Unlock()
	if _, err := store.OperatorAlertResponseTimes(ctx, f); err == nil {
		t.Fatal("receipt limit silently sampled")
	}
}
