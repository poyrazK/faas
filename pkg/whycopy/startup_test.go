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
	// A raw TCP server (Redis) built from source: both phases it can end in
	// name the image deploy, which needs no HTTP probe (ADR-20261112223000 §2).
	for _, detail := range []string{
		"startup_phase=guest_startup: guest x never answered readiness probe /healthz before 30s",
		"startup_phase=handler_healthcheck: guest x accepted 3 readiness probes GET /healthz on :6379 but answered without HTTP before 30s",
	} {
		p := api.NewProblem(422, api.CodeAppStartupTimeout, "timeout", detail)
		Decorate(p, p.Code, nil)
		if !strings.Contains(p.Fix, "gregale deploy --image") || !strings.Contains(p.Fix, "--ports <name>=tcp:<port>:internal") {
			t.Fatalf("detail %q: fix=%q, want the image deploy", detail, p.Fix)
		}
	}
	nonHTTP := api.NewProblem(422, api.CodeAppStartupTimeout, "timeout", "startup_phase=handler_healthcheck: answered without HTTP")
	Decorate(nonHTTP, nonHTTP.Code, nil)
	if !strings.Contains(nonHTTP.Why, "raw TCP server") {
		t.Fatalf("why=%q, want the raw TCP explanation", nonHTTP.Why)
	}
	p := api.NewProblem(422, api.CodeAppNotListening, "listener", "startup_phase=handler_healthcheck")
	Decorate(p, p.Code, nil)
	if strings.Contains(p.Why, "answered readiness probes") {
		t.Fatal("phase changed unrelated error guidance")
	}
}
