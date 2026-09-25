package sched

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppForDeploymentUsesImmutableComputeShape(t *testing.T) {
	app := state.App{RAMMB: 256, CPUMillicores: 500, MaxConcurrency: 2}
	dep := state.Deployment{RAMMB: 512, CPUMillicores: 1000}

	got := appForDeployment(app, dep)
	if got.RAMMB != 512 || got.CPUMillicores != 1000 || got.MaxConcurrency != app.MaxConcurrency {
		t.Fatalf("effective app = %d MiB/%d mCPU/concurrency %d; want revision compute with app concurrency %d", got.RAMMB, got.CPUMillicores, got.MaxConcurrency, app.MaxConcurrency)
	}

	legacy := appForDeployment(app, state.Deployment{})
	if legacy.RAMMB != app.RAMMB || legacy.CPUMillicores != app.CPUMillicores {
		t.Fatalf("legacy deployment changed app defaults: %d MiB/%d mCPU", legacy.RAMMB, legacy.CPUMillicores)
	}
}
