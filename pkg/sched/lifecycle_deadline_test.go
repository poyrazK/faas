package sched

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStartupDeadlineForApp(t *testing.T) {
	tests := []struct {
		name     string
		manifest state.AppManifest
		plan     api.Plan
		want     int32
	}{
		{name: "plan default", plan: api.PlanPro, want: 60},
		{name: "manifest override", manifest: state.AppManifest{StartupDeadlineS: 17}, plan: api.PlanPro, want: 17},
		{name: "unknown plan preserves vmmd default", plan: api.Plan("unknown"), want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := state.App{Manifest: tt.manifest}
			if got := startupDeadlineForApp(app, tt.plan); got != tt.want {
				t.Fatalf("startup deadline = %d, want %d", got, tt.want)
			}
		})
	}
}
