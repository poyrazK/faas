package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDataRepairUnresolvedOutcomeSurvivesRetention(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			for _, outcome := range []string{api.DataRepairSucceeded, api.DataRepairClosed} {
				t.Run(outcome, func(t *testing.T) {
					var store state.RepairTestStore = state.NewMemStore()
					if backend == "postgres" {
						store = state.NewPgStore(pgtest.OpenMigrated(t))
					}
					now := time.Now().UTC()
					op := state.SeedRepairHealthOperation(t, store, now.Add(-365*24*time.Hour), api.DataRepairApplying, false)
					planExpiry := op.DataRepair.PlanExpiresAt
					var err error
					op, err = store.AdvanceDataRepairOperation(t.Context(), op.AccountID, op.ID, state.DataRepairAdvance{
						ExpectedSequence: op.LatestSequence, Phase: api.DataRepairUnknown,
					})
					if err != nil {
						t.Fatal(err)
					}
					retention := store.(state.OperationRetentionStore)
					if _, err := retention.PruneOperationState(t.Context(), now.Add(365*24*time.Hour), api.OperationRetentionPageMax); err != nil {
						t.Fatal(err)
					}
					kept, err := store.OperationByID(t.Context(), op.AccountID, "", op.ID)
					if err != nil || kept.DataRepair.Phase != api.DataRepairUnknown || !kept.ExpiresAt.Before(now) {
						t.Fatalf("unresolved identity lost or expiry refreshed: %+v %v", kept.DataRepair, err)
					}
					events, err := store.OperationEvents(t.Context(), op.AccountID, "", op.ID, 0, 10)
					if err != nil || events.ResyncRequired || len(events.Events) != 1 {
						t.Fatalf("unresolved events lost: %+v %v", events, err)
					}
					if outcome == api.DataRepairClosed {
						op, err = store.AdvanceDataRepairOperation(t.Context(), op.AccountID, op.ID, state.DataRepairAdvance{
							ExpectedSequence: op.LatestSequence, Phase: api.DataRepairResolving,
						})
						if err != nil {
							t.Fatal(err)
						}
					}
					in := state.DataRepairAdvance{ExpectedSequence: op.LatestSequence, Phase: outcome}
					if outcome == api.DataRepairSucceeded {
						in.AppliedAt = &now
					} else {
						in.ApplyClosedAt = &now
					}
					settled, err := store.AdvanceDataRepairOperation(t.Context(), op.AccountID, op.ID, in)
					if err != nil || !settled.ExpiresAt.After(now) || !settled.EventExpiresAt.After(now) || !settled.DataRepair.PlanExpiresAt.Equal(planExpiry) {
						t.Fatalf("settlement lost retention or refreshed source authority: %+v %v", settled.DataRepair, err)
					}
					if _, err := retention.PruneOperationState(t.Context(), now.Add(time.Second), api.OperationRetentionPageMax); err != nil {
						t.Fatal(err)
					}
					if _, err := store.OperationByID(t.Context(), op.AccountID, "", op.ID); err != nil {
						t.Fatal("newly settled outcome unavailable", err)
					}
					if _, err := retention.PruneOperationState(t.Context(), settled.ExpiresAt.Add(time.Second), api.OperationRetentionPageMax); err != nil {
						t.Fatal(err)
					}
					if _, err := store.OperationByID(t.Context(), op.AccountID, "", op.ID); !errors.Is(err, state.ErrNotFound) {
						t.Fatal("settled repair was retained indefinitely", err)
					}
				})
			}
		})
	}
}
