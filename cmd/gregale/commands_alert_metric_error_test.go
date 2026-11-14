package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestAlertMetricErrorListsTheMetrics pins Hunt 32 H32-18: an unknown
// --metric said only "not in the closed set".
func TestAlertMetricErrorListsTheMetrics(t *testing.T) {
	got := main.InvalidAlertMetricError("nope").Error()
	for _, metric := range api.AllowedAlertRuleMetrics {
		if !strings.Contains(got, metric) {
			t.Fatalf("error does not list %q: %s", metric, got)
		}
	}
}
