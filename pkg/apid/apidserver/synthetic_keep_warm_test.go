package apidserver

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// prod 2026-11-15: a 5-minute check kept an app with the default idle
// timeout warm around the clock. Creation now says so.
func TestSyntheticCheckKeepWarmWarning(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanScale)
	idle := limits.EffectiveIdleTimeoutS(string(state.AppTypeApp), 0)
	app := state.App{Type: state.AppTypeApp}
	if got := syntheticCheckKeepWarmWarning(api.PlanScale, app, idle); !strings.Contains(got, "never scales to zero") {
		t.Fatalf("interval == idle timeout: warning = %q", got)
	}
	if got := syntheticCheckKeepWarmWarning(api.PlanScale, app, idle+1); got != "" {
		t.Fatalf("interval past the idle timeout: warning = %q, want none", got)
	}
	warm := state.App{Type: state.AppTypeApp, MinInstances: 1}
	if got := syntheticCheckKeepWarmWarning(api.PlanScale, warm, 300); got != "" {
		t.Fatalf("app with a warm floor: warning = %q, want none", got)
	}
	if got := joinSyntheticWarnings("a", "", "b"); got != "a b" {
		t.Fatalf("join = %q", got)
	}
}
