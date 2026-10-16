package state

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOperatorAlertCollectionHistoryMemoryBoundAndLegacy(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	store := NewMemStore()
	store.operatorAlertCollection = OperatorAlertCollection{LastAttemptAt: now, LastSuccessAt: now}
	f := OperatorAlertCollectionHistoryFilter{Since: now.Add(-time.Hour), Now: now}
	legacy, err := store.OperatorAlertCollectionHistory(t.Context(), f)
	if err != nil || len(legacy.Outcomes) != 0 || !legacy.Collection.LastSuccessAt.Equal(now) {
		t.Fatalf("legacy evidence invented: %+v %v", legacy, err)
	}
	for i := api.ObsAlertCollectionHistoryOutcomesMax; i >= 0; i-- {
		store.operatorAlertCollectionOutcomes = append(store.operatorAlertCollectionOutcomes, OperatorAlertCollectionOutcome{AttemptedAt: now.Add(-time.Duration(i) * time.Microsecond), Outcome: "success"})
	}
	_, err = store.OperatorAlertCollectionHistory(t.Context(), f)
	var bound *OperatorAlertCollectionHistoryLimitError
	if !errors.As(err, &bound) {
		t.Fatalf("partial memory read: %v", err)
	}
	// Restrict by as_of to include exactly the cap, with the carry-in range.
	f.Since, f.Now = now.Add(-time.Hour), now.Add(-time.Microsecond)
	exact, err := store.OperatorAlertCollectionHistory(t.Context(), f)
	if err != nil || len(exact.Outcomes) != api.ObsAlertCollectionHistoryOutcomesMax {
		t.Fatalf("exact read: %d %v", len(exact.Outcomes), err)
	}
	if _, err := store.PruneOperatorAlertHistory(t.Context(), now.Add(time.Second), 1); err != nil {
		t.Fatal(err)
	}
	if len(store.operatorAlertCollectionOutcomes) != api.ObsAlertCollectionHistoryOutcomesMax+1-api.ObsAlertCollectionHistoryPruneBatch {
		t.Fatal("prune batch not bounded")
	}
}
