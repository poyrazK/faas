package apphealth

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// A fresh boot that crash-loops while an older replica keeps serving used to
// read "healthy" (prod 2026-11-15, finding 43).
func TestAppHealth_BootFailures(t *testing.T) {
	failed := func(e Evidence, deployment string, age time.Duration) state.Instance {
		return state.Instance{ID: "failed-" + age.String(), DeploymentID: deployment, State: string(state.StateFailed), StartedAt: e.Now.Add(-age)}
	}
	tests := []struct {
		name       string
		change     func(*Evidence)
		status     string
		bootStatus string // "" = no boot_failures check
	}{
		{"recent failure beside a ready replica", func(e *Evidence) {
			e.Recent = []state.Instance{failed(*e, "serving", 2*time.Minute)}
		}, Degraded, Warning},
		{"recent failure with nothing ready", func(e *Evidence) {
			e.Instances = nil
			e.Recent = []state.Instance{failed(*e, "serving", 2*time.Minute)}
		}, Unhealthy, Fail},
		{"recent failure while another replica starts", func(e *Evidence) {
			e.Instances[0].State = string(state.StateColdBooting)
			e.Recent = []state.Instance{failed(*e, "serving", time.Minute)}
		}, Degraded, Warning},
		{"failure older than the window", func(e *Evidence) {
			e.Recent = []state.Instance{failed(*e, "serving", 20*time.Minute)}
		}, Healthy, ""},
		{"failure of a release no longer serving", func(e *Evidence) {
			e.Recent = []state.Instance{failed(*e, "retired", time.Minute)}
		}, Healthy, ""},
		{"parked and stopped history is not a failure", func(e *Evidence) {
			e.Recent = []state.Instance{
				{ID: "p", DeploymentID: "serving", State: string(state.StateParked), StartedAt: e.Now.Add(-time.Minute)},
				{ID: "s", DeploymentID: "serving", State: string(state.StateStopped), StartedAt: e.Now.Add(-time.Minute)},
			}
		}, Healthy, ""},
		{"recent evidence unavailable", func(e *Evidence) {
			e.RecentKnown = false
			e.Recent = []state.Instance{failed(*e, "serving", time.Minute)}
		}, Healthy, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := healthEvidence()
			e.RecentKnown = true
			tt.change(&e)
			got := Evaluate(e)
			if got.Status != tt.status {
				t.Fatalf("status = %s, want %s: %+v", got.Status, tt.status, got.Checks)
			}
			var boot string
			for _, c := range got.Checks {
				if c.Code == "boot_failures" {
					boot = c.Status
					if !strings.Contains(c.Detail, "crash-looped or timed out") || c.DeploymentID != "serving" {
						t.Fatalf("boot_failures check = %+v", c)
					}
				}
			}
			if boot != tt.bootStatus {
				t.Fatalf("boot_failures status = %q, want %q: %+v", boot, tt.bootStatus, got.Checks)
			}
		})
	}
}
