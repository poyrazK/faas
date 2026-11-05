package databaseswitch_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDatabaseSwitchRefusesTemporaryTargets(t *testing.T) {
	for _, adopted := range []bool{false, true} {
		name := "restore response"
		if adopted {
			name = "previously recorded target"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			if adopted {
				h.tick() // save the still-provisioning target before a restart
			}
			h.databases.target.State = managedpostgres.StateReady
			h.databases.target.RestoreSlotUntil = h.clock.Add(api.ManagedPostgresRestoreSlotWindow)
			result := h.runUntil(state.DatabaseSwitchFailed, 5)
			if result.FailureCode != "database_limit" || h.releases.live["default"] != h.sw.FromDeploymentID || len(h.cutovers.calls) != 0 {
				t.Fatalf("temporary copy reached cutover: switch=%+v cutover calls=%v", result, h.cutovers.calls)
			}
		})
	}
}
