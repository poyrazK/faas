package state_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDataRepairHealthPendingResolution(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s state.RepairTestStore = state.NewMemStore()
			if backend == "postgres" {
				s = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			now := time.Now().UTC()
			op := state.SeedRepairHealthOperation(t, s, now.Add(-365*24*time.Hour), api.DataRepairApplying, false)
			op, err := s.AdvanceDataRepairOperation(t.Context(), op.AccountID, op.ID, state.DataRepairAdvance{ExpectedSequence: op.LatestSequence, Phase: api.DataRepairResolving})
			if err != nil {
				t.Fatal(err)
			}
			h, err := s.(state.DataRepairHealthStore).DataRepairHealth(t.Context(), now.Add(3*time.Minute), "", 10)
			i := slices.Index(api.DataRepairHealthPhases(), api.DataRepairResolving)
			if err != nil || len(h.Items) != 1 || h.Items[0].OperationID != op.ID || h.Items[0].Phase != api.DataRepairResolving ||
				!h.Items[0].NeedsAttention || h.Items[0].CapacityHeld || !strings.Contains(h.Items[0].RecoveryGuidance, "durable source closure") ||
				h.Phases[i].Count != 1 || h.Phases[i].WarnAfterSeconds != api.DataRepairTimeoutSeconds {
				t.Fatalf("pending resolution health: %+v %v", h, err)
			}
			if _, err := s.(state.OperationRetentionStore).PruneOperationState(t.Context(), now.Add(365*24*time.Hour), api.OperationRetentionPageMax); err != nil {
				t.Fatal(err)
			}
			if kept, err := s.OperationByID(t.Context(), op.AccountID, "", op.ID); err != nil || kept.DataRepair.Phase != api.DataRepairResolving {
				t.Fatal("pending resolution was pruned", err)
			}
			at := time.Now().UTC()
			settled, err := s.AdvanceDataRepairOperation(t.Context(), op.AccountID, op.ID, state.DataRepairAdvance{ExpectedSequence: op.LatestSequence, Phase: api.DataRepairClosed, ApplyClosedAt: &at})
			if err != nil || !settled.ExpiresAt.After(at) || !settled.EventExpiresAt.After(at) || !settled.DataRepair.PlanExpiresAt.Equal(op.DataRepair.PlanExpiresAt) {
				t.Fatal("late closure expired its outcome or refreshed source authority", err)
			}
			if _, err := s.OperationByID(t.Context(), op.AccountID, "", op.ID); err != nil {
				t.Fatal("late closure was unavailable for replacement", err)
			}
			timeline, err := s.OperationTimeline(t.Context(), op.AccountID, op.ID, state.OperationTimelineCursor{}, 10)
			if err != nil || len(timeline.Response.Entries) != 2 {
				t.Fatal("pending resolution lost its history", err)
			}
		})
	}
}
