package api_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppLogDrainHealthSurfaceSanitizesStatusAndError(t *testing.T) {
	if got := api.NormalizeAppLogDrainHealthStatus("future-status"); got != api.AppLogDrainHealthStatusUnknown {
		t.Fatalf("unknown health status = %q, want %q", got, api.AppLogDrainHealthStatusUnknown)
	}
	if got := api.NormalizeAppLogDrainHealthStatus(api.AppLogDrainHealthStatusHealthy); got != api.AppLogDrainHealthStatusHealthy {
		t.Fatalf("known health status = %q, want %q", got, api.AppLogDrainHealthStatusHealthy)
	}
	if got := api.SanitizeAppLogDrainHealthError("dial tcp 10.0.0.1:443: secret=leaked"); got != "delivery failed" {
		t.Fatalf("raw health error = %q, want generic summary", got)
	}
	if got := api.SanitizeAppLogDrainHealthError("source log gap observed"); got != "source log gap observed" {
		t.Fatalf("known health error = %q, want preserved summary", got)
	}
}
