package main_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// H10-31: an app that starts listening after its startup deadline was told
// to fix its bind address. The explanation now names the deadline.
func TestMapFailureProblemNamesStartupDeadline(t *testing.T) {
	slow := &api.Problem{Code: api.CodeAppNotListening, Detail: "snapshot prime failed: sched: prime: cold boot: app_not_listening: startup_phase=handler_boot: readiness probe GET /healthz on :8080 got ECONNREFUSED (refused_count=1717, deadline=20s, instance=f8c2f1f0); the app never listened or exited during startup"}
	got := main.MapFailureProblem(slow)
	for _, want := range []string{"within the 20s startup deadline", "--startup-deadline-s", "bind to 0.0.0.0"} {
		if !strings.Contains(got, want) {
			t.Errorf("slow-start explanation lacks %q:\n%s", want, got)
		}
	}

	wrongPort := &api.Problem{Code: api.CodeAppNotListening, Detail: "readiness probe found no listener"}
	if got := main.MapFailureProblem(wrongPort); strings.Contains(got, "startup deadline") || !strings.Contains(got, "isn't accepting traffic") {
		t.Errorf("explanation without a deadline detail changed:\n%s", got)
	}
}
