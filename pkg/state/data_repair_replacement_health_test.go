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

func TestDataRepairHealthPendingReplacement(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.RepairTestStore = state.NewMemStore()
			if backend == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			now := time.Now().UTC()
			op := state.SeedRepairHealthOperation(t, store, now, api.DataRepairReplacing, false)
			h, err := store.(state.DataRepairHealthStore).DataRepairHealth(t.Context(), now, "", 10)
			if err != nil || len(h.Items) != 1 || h.Items[0].OperationID != op.ID || h.Items[0].Phase != api.DataRepairReplacing ||
				!h.Items[0].NeedsAttention || h.Items[0].CapacityHeld || !strings.Contains(h.Items[0].RecoveryGuidance, "same UUID") ||
				h.Phases[slices.Index(api.DataRepairHealthPhases(), api.DataRepairReplacing)].Count != 1 || h.Phases[slices.Index(api.DataRepairHealthPhases(), api.DataRepairReplacing)].WarnAfterSeconds != api.DataRepairTimeoutSeconds {
				t.Fatalf("pending replacement health: %+v %v", h, err)
			}
		})
	}
}
