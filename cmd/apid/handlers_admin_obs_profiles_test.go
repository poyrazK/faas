package main

// adr: 157 — operator capacity and usage views report the resolved app shape.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestBuildObsCapacityProfiles(t *testing.T) {
	apps := []state.App{
		{ID: "small", RAMMB: 256, CPUMillicores: 500, Status: state.AppActive},
		{ID: "custom", RAMMB: 384, CPUMillicores: 750, Status: state.AppActive},
		{ID: "deleted", RAMMB: 128, CPUMillicores: 250, Status: state.AppDeleted},
	}
	instances := []state.Instance{
		{AppID: "small", State: string(state.StateRunning)},
		{AppID: "small", State: string(state.StateWaking)},
		{AppID: "custom", State: string(state.StateParked)},
	}

	got := buildObsCapacityProfiles(apps, instances)
	if len(got) != 2 {
		t.Fatalf("profile rows = %d, want 2: %+v", len(got), got)
	}
	if got[0].ResourceProfile != "small" || got[0].Apps != 1 || got[0].LiveInstances != 2 {
		t.Fatalf("small profile = %+v", got[0])
	}
	if got[1].ResourceProfile != "custom" || got[1].Apps != 1 || got[1].LiveInstances != 0 {
		t.Fatalf("custom profile = %+v", got[1])
	}
}

func TestBuildObsTenantUsageProfiles(t *testing.T) {
	apps := map[string]state.App{
		"small":  {ID: "small", RAMMB: 256, CPUMillicores: 500},
		"custom": {ID: "custom", RAMMB: 384, CPUMillicores: 750},
	}
	rows := []state.Usage{
		{AppID: "small", MBSeconds: 1024 * 3600, CPUUsec: 3_600_000_000, Requests: 3},
		{AppID: "custom", MBSeconds: 512 * 3600, Requests: 2, ColdBootCount: 1},
	}

	got := buildObsTenantUsageProfiles(rows, apps)
	if len(got) != 2 {
		t.Fatalf("profile rows = %d, want 2: %+v", len(got), got)
	}
	if got[0].ResourceProfile != "small" || got[0].Apps != 1 || got[0].Requests != 3 || got[0].UsedCPUHours != 1 {
		t.Fatalf("small usage profile = %+v", got[0])
	}
	if got[1].ResourceProfile != "custom" || got[1].Apps != 1 || got[1].ColdBoots != 1 {
		t.Fatalf("custom usage profile = %+v", got[1])
	}
}
