// adr: 947
package sched

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestValidatorArtifactRefusalPreventsDeploymentPrime(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 2)
	vmm := &fakeVMM{}
	notifier := &fakeNotifier{}
	denied := errors.New("validator unavailable")
	e := newEngine(t, store, vmm, notifier, "1.10.0").WithValidatorArtifactCheck(func(_ context.Context, appID, deploymentID string) error {
		if appID != app.ID || deploymentID != dep.ID {
			t.Fatal("wrong artifact identity")
		}
		return denied
	})
	if err := e.Prime(t.Context(), app.ID, dep.ID); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	if vmm.coldBoots != 0 || vmm.snapshots != 0 || notifier.count("snapshot_written") != 0 {
		t.Fatal("prime ran despite unavailable validator")
	}
	rows, err := store.ListInstancesForApp(t.Context(), app.ID)
	if err != nil || len(rows) != 0 {
		t.Fatal("prime allocated instance", rows, err)
	}
}
