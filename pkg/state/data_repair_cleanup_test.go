package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type repairCleanupHost struct {
	operation.Host
	store state.Store
}

func (h repairCleanupHost) Store() state.Store { return h.store }

func TestDataRepairDraftExpiryWithoutArtifactStorage(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.RepairTestStore = state.NewMemStore()
			if backend == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			now := time.Now().UTC()
			op := state.SeedRepairHealthOperation(t, store, now.Add(-25*time.Hour), api.DataRepairPrepared, false)
			svc := operation.Service{Host: repairCleanupHost{store: store}}
			if err := svc.CleanupOperationArtifacts(t.Context(), now); err != nil {
				t.Fatal(err)
			}
			got, err := store.OperationByID(t.Context(), op.AccountID, "", op.ID)
			if err != nil || got.State != api.OperationFailed || got.DataRepair.Phase != api.DataRepairFailed {
				t.Fatal("apid maintenance left an expired draft active without artifact storage", err)
			}
		})
	}
}
