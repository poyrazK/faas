package apidserver_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// The idle timeout and pause behaviour in effective_limits are what the
// reaper and schedd apply, so `gregale deploy` can print them.
func TestAppEffectiveLimitsIdleAndPause(t *testing.T) {
	pro := api.MustLimitsFor(api.PlanPro)
	tests := []struct {
		name      string
		app       state.App
		wantIdle  int
		wantPause string
	}{
		{"function defaults", state.App{Type: state.AppTypeFunction}, api.FunctionIdleTimeoutDefaultSeconds, api.PauseBetweenRequests},
		{"app defaults", state.App{Type: state.AppTypeApp}, pro.IdleTimeoutS, api.PauseWhenIdle},
		{"configured", state.App{Type: state.AppTypeFunction, IdleTimeoutS: 120, Manifest: state.AppManifest{Pause: api.PauseNever}}, 120, api.PauseNever},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := apidserver.AppEffectiveLimits(tt.app, api.PlanPro)
			if got.IdleTimeoutS != tt.wantIdle || got.Pause != tt.wantPause {
				t.Fatalf("idle/pause = %d/%q, want %d/%q", got.IdleTimeoutS, got.Pause, tt.wantIdle, tt.wantPause)
			}
		})
	}
}
