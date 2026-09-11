// adr: 137

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

func TestExecutionModeForApp(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want string
	}{
		{name: "empty defaults request", want: api.ExecutionModeRequest},
		{name: "request", mode: api.ExecutionModeRequest, want: api.ExecutionModeRequest},
		{name: "service", mode: api.ExecutionModeService, want: api.ExecutionModeService},
		{name: "worker", mode: api.ExecutionModeWorker, want: api.ExecutionModeWorker},
		{name: "job", mode: api.ExecutionModeJob, want: api.ExecutionModeJob},
		{name: "unknown fails safe", mode: "magic", want: api.ExecutionModeRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := state.App{Manifest: state.AppManifest{ExecutionMode: tt.mode}}
			if got := executionModeForApp(app); got != tt.want {
				t.Fatalf("execution mode = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAppSpecToProtoCarriesExecutionMode(t *testing.T) {
	got := (AppSpec{ExecutionMode: api.ExecutionModeWorker}).toProto().GetExecutionMode()
	if got != api.ExecutionModeWorker {
		t.Fatalf("execution mode = %q, want %q", got, api.ExecutionModeWorker)
	}
}
