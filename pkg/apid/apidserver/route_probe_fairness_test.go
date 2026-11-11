package apidserver_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/state"
)

// One goroutine probed every canary in a fixed order, so one that never
// answered held every other app's probes for the round timeout, always the
// same apps first (bug hunt H79). Targets are grouped per account, each
// account's order kept, and the first account rotates by minute.
func TestRouteProbeQueuesAreFairAcrossAccounts(t *testing.T) {
	target := func(account, app string) release.ProbeTarget {
		return release.ProbeTarget{RouteProbeTarget: state.RouteProbeTarget{AccountID: account, AppID: app}}
	}
	targets := []release.ProbeTarget{target("a", "a1"), target("b", "b1"), target("a", "a2"), target("c", "c1")}
	minute := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	queues := apidserver.RouteProbeAccountQueues(targets, minute)
	if len(queues) != 3 {
		t.Fatalf("queues = %d, want one per account", len(queues))
	}
	firsts := map[string]bool{}
	for offset := range 3 {
		q := apidserver.RouteProbeAccountQueues(targets, minute.Add(time.Duration(offset)*time.Minute))
		firsts[q[0][0].AccountID] = true
		for _, queue := range q {
			if queue[0].AccountID == "a" && (len(queue) != 2 || queue[0].AppID != "a1" || queue[1].AppID != "a2") {
				t.Fatalf("account a's targets = %+v, want a1 then a2", queue)
			}
		}
	}
	if len(firsts) != 3 {
		t.Fatalf("accounts probed first over three minutes = %v, want each once", firsts)
	}
}
