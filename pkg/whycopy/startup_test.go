package whycopy

import (
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"testing"
)

func TestStartupGuidanceUsesRecordedPhase(t *testing.T) {
	cases := []struct{ detail, want string }{
		{"guest id not ready: startup_phase=guest_startup; no response", "guest did not answer"},
		{"startup_phase=handler_healthcheck: readiness returned 503", "answered readiness probes"},
		{"startup_phase=image_healthcheck: command failed", "fresh successful result"},
		{"startup_phase=scheduler_timeout: cold boot expired", "configured readiness check"},
		{"startup_phase=unknown", "configured readiness check"},
		{"prefix_startup_phase=handler_healthcheck", "configured readiness check"},
		{"", "configured readiness check"},
	}
	for _, tc := range cases {
		t.Run(tc.detail, func(t *testing.T) {
			p := api.NewProblem(422, api.CodeAppStartupTimeout, "timeout", tc.detail+" private-password")
			Decorate(p, p.Code, nil)
			if !strings.Contains(p.Why, tc.want) {
				t.Fatalf("why=%q, want %q", p.Why, tc.want)
			}
			if p.Fix == "" || strings.Contains(p.Why+p.Fix, "private-password") || strings.Contains(p.Why, "35s") {
				t.Fatalf("unexpected guidance: %+v", p)
			}
		})
	}
	p := api.NewProblem(422, api.CodeAppNotListening, "listener", "startup_phase=handler_healthcheck")
	Decorate(p, p.Code, nil)
	if strings.Contains(p.Why, "answered readiness probes") {
		t.Fatal("phase changed unrelated error guidance")
	}
}
