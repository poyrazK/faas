package main

import (
	"bytes"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"testing"
	"time"
)

func TestRenderRouteRemovalServerCheck(t *testing.T) {
	deadline := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	renderRouteRemovalServerCheck(&out, api.RouteRemovalCheck{Status: "blocked", Policy: api.RouteRemovalPolicy{Mode: "enforce", Revision: 2}, EarliestApprovalAt: &deadline, ApprovalValidUntil: &deadline, Blockers: []string{"old_route_observed"}, NextActions: []string{"Migrate remaining clients."}})
	for _, want := range []string{"policy enforce, revision 2", "Earliest approval: 2026-10-08T12:00:00Z", "Approval expires:", "Blocker: old_route_observed", "Next: Migrate remaining clients."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %s", want, out.String())
		}
	}
}
