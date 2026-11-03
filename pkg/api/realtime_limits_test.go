package api

import "testing"

// adr: 1323 — realtimed maps an endpoint's account connection allowance back
// to its plan, so allowances must stay distinct, and a plan's resume share
// fits inside the per-account half of a node.
func TestRealtimeLimits(t *testing.T) {
	nodeShare := RealtimeResumeSubscriptionsPerNode * RealtimeAccountNodeSharePercent / 100
	seen := map[int]Plan{}
	for _, plan := range Plans {
		l, ok := RealtimeLimitsFor(plan)
		if !ok || l.ConnectionsPerAccount == 0 {
			continue
		}
		if other, dup := seen[l.ConnectionsPerAccount]; dup {
			t.Fatalf("%s and %s share ConnectionsPerAccount %d", plan, other, l.ConnectionsPerAccount)
		}
		seen[l.ConnectionsPerAccount] = plan
		if l.ResumeSubscriptionsPerNode <= 0 || l.ResumeSubscriptionsPerNode > nodeShare {
			t.Errorf("%s ResumeSubscriptionsPerNode = %d, want 1..%d", plan, l.ResumeSubscriptionsPerNode, nodeShare)
		}
		if got := RealtimeResumeSubscriptionsPerNodeFor(l.ConnectionsPerAccount); got != l.ResumeSubscriptionsPerNode {
			t.Errorf("%s: allowance for %d connections = %d, want %d", plan, l.ConnectionsPerAccount, got, l.ResumeSubscriptionsPerNode)
		}
	}
	for _, n := range []int{0, -1, 7} {
		if got := RealtimeResumeSubscriptionsPerNodeFor(n); got != 0 {
			t.Errorf("allowance for %d connections = %d, want 0", n, got)
		}
	}
}

// A plan has realtime exactly when it has endpoints and a connection cap:
// every realtime limit of a plan without it is zero, which reads as no cap.
func TestRealtimeAllowedFor(t *testing.T) {
	allowed := map[string]bool{}
	for _, p := range RealtimePlans() {
		allowed[p] = true
	}
	for _, plan := range Plans {
		l, _ := RealtimeLimitsFor(plan)
		want := l.EndpointsPerApp > 0 && l.ConnectionsPerAccount > 0
		if got := RealtimeAllowedFor(plan); got != want || allowed[string(plan)] != want {
			t.Errorf("%s: RealtimeAllowedFor = %v, in RealtimePlans = %v, want %v", plan, got, allowed[string(plan)], want)
		}
	}
	if RealtimeAllowedFor(PlanFree) || RealtimeAllowedFor(Plan("unknown")) {
		t.Error("Free or an unknown plan includes realtime")
	}
}
