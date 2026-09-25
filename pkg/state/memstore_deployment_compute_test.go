package state

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreCreateDeploymentSnapshotsComputeShape(t *testing.T) {
	store := NewMemStore()
	app := seedMemAffinityApp(t, store)
	wantCPU := app.CPUMillicores
	if wantCPU <= 0 {
		wantCPU = api.DefaultAppCPUMillicores
	}

	defaulted, err := store.CreateDeployment(context.Background(), Deployment{AppID: app.ID})
	if err != nil {
		t.Fatalf("CreateDeployment(default): %v", err)
	}
	if defaulted.RAMMB != app.RAMMB || defaulted.CPUMillicores != wantCPU {
		t.Fatalf("default deployment shape = %d MiB/%d mCPU, want %d MiB/%d mCPU", defaulted.RAMMB, defaulted.CPUMillicores, app.RAMMB, wantCPU)
	}

	overridden, err := store.CreateDeployment(context.Background(), Deployment{
		AppID: app.ID, RAMMB: 128, CPUMillicores: 250,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(override): %v", err)
	}
	if overridden.RAMMB != 128 || overridden.CPUMillicores != 250 {
		t.Fatalf("overridden deployment shape = %d MiB/%d mCPU, want 128 MiB/250 mCPU", overridden.RAMMB, overridden.CPUMillicores)
	}
}
