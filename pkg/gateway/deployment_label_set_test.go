// adr: 127
// deployment_label_set_test.go — table-driven tests for the
// per-app bounded admission set backing the deployment_id
// label on gateway_request_duration_by_deployment_seconds (ADR-127 §Decision
// 4 / Debugger UX v1).
//
// The set is per-app (cap varies by plan) rather than global,
// so the test pins the contract at three layers:
//
//   - per-app cap, not global: a Hobby customer's 10-dep cap
//     should not consume budget from a Scale customer's 200-dep
//     cap;
//   - reserved sentinels (empty, "__other__") don't consume
//     capacity;
//   - Free plan collapses every real deployment id to
//     "__other__" (DebugTelemetryDeploymentsPerApp = 0).
package gateway

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDeploymentLabelSet_FirstAdmitReturnsLabel(t *testing.T) {
	// Case (a): first admit of a real deployment id returns the
	// id verbatim. Idempotent — second admit of the same id is a
	// no-op hit.
	s := newDeploymentLabelSet()
	got := s.admit("app-1", api.PlanHobby, "deploy-A")
	if got != "deploy-A" {
		t.Errorf("first admit = %q, want deploy-A", got)
	}
	got2 := s.admit("app-1", api.PlanHobby, "deploy-A")
	if got2 != "deploy-A" {
		t.Errorf("second admit = %q, want deploy-A (idempotent)", got2)
	}
}

func TestDeploymentLabelSet_CapExhaustedCollapsesToOther(t *testing.T) {
	// Case (b): Hobby cap = 10 (DebugTelemetryDeploymentsPerApp).
	// After admitting 10 distinct real deployment ids, the 11th
	// collapses to "__other__".
	s := newDeploymentLabelSet()
	for i := 1; i <= 10; i++ {
		id := deploymentIDForIndex(i)
		if got := s.admit("app-hobby", api.PlanHobby, id); got != id {
			t.Fatalf("admit #%d (%q) = %q, want %q", i, id, got, id)
		}
	}
	got := s.admit("app-hobby", api.PlanHobby, "deploy-overflow")
	if got != otherDeploymentLabel {
		t.Errorf("overflow admit = %q, want %q", got, otherDeploymentLabel)
	}
}

func TestDeploymentLabelSet_EmptyDoesNotConsumeCapacity(t *testing.T) {
	// Case (c): empty deployment id (legacy pre-PR-B
	// single-targetSet) routes to the "" reserved label and does
	// NOT consume capacity. The Hobby cap stays at 10 real
	// deployments even after millions of empty admissions.
	s := newDeploymentLabelSet()
	for i := 0; i < 100; i++ {
		if got := s.admit("app-hobby", api.PlanHobby, ""); got != emptyDeploymentLabel {
			t.Fatalf("empty admit #%d = %q, want %q", i, got, emptyDeploymentLabel)
		}
	}
	// All 10 real deployments should still admit cleanly.
	for i := 1; i <= 10; i++ {
		id := deploymentIDForIndex(i)
		if got := s.admit("app-hobby", api.PlanHobby, id); got != id {
			t.Fatalf("post-empty admit #%d (%q) = %q, want %q", i, id, got, id)
		}
	}
	// 11th real collapses to __other__ — confirms capacity was
	// NOT stolen by the empty admissions.
	got := s.admit("app-hobby", api.PlanHobby, "deploy-overflow")
	if got != otherDeploymentLabel {
		t.Errorf("overflow after empty admits = %q, want %q", got, otherDeploymentLabel)
	}
}

func TestDeploymentLabelSet_OtherDoesNotConsumeCapacity(t *testing.T) {
	// Case (d): "__other__" is a reserved pass-through — admits
	// don't consume capacity. Mirrors the empty-id case.
	s := newDeploymentLabelSet()
	for i := 0; i < 100; i++ {
		if got := s.admit("app-hobby", api.PlanHobby, otherDeploymentLabel); got != otherDeploymentLabel {
			t.Fatalf("other admit #%d = %q, want %q", i, got, otherDeploymentLabel)
		}
	}
}

func TestDeploymentLabelSet_PerAppIsolation(t *testing.T) {
	// Case (e): per-app cap, not global. Hobby's 10-dep cap on
	// app-hobby must not consume budget from Scale's 200-dep
	// cap on app-scale.
	s := newDeploymentLabelSet()
	// Exhaust Hobby's app-hobby budget.
	for i := 1; i <= 10; i++ {
		s.admit("app-hobby", api.PlanHobby, deploymentIDForIndex(i))
	}
	if got := s.admit("app-hobby", api.PlanHobby, "deploy-hobby-overflow"); got != otherDeploymentLabel {
		t.Errorf("Hobby overflow = %q, want %q", got, otherDeploymentLabel)
	}
	// Scale app-scale must still admit freely — the Hobby cap
	// didn't consume any budget from app-scale.
	for i := 1; i <= 50; i++ {
		id := deploymentIDForIndex(i)
		if got := s.admit("app-scale", api.PlanScale, id); got != id {
			t.Fatalf("Scale admit #%d (%q) = %q, want %q (cross-app budget bleed)",
				i, id, got, id)
		}
	}
}

func TestDeploymentLabelSet_FreePlanCollapsesEverything(t *testing.T) {
	// Case (f): Free plan's DebugTelemetryDeploymentsPerApp = 0
	// (limits.go:1659). Any real deployment id collapses to
	// "__other__" on first sight — no admission, no capacity.
	s := newDeploymentLabelSet()
	got := s.admit("app-free", api.PlanFree, "deploy-real")
	if got != otherDeploymentLabel {
		t.Errorf("Free plan first admit = %q, want %q", got, otherDeploymentLabel)
	}
	// Reserved pass-throughs still work.
	if got := s.admit("app-free", api.PlanFree, ""); got != emptyDeploymentLabel {
		t.Errorf("Free plan empty admit = %q, want %q", got, emptyDeploymentLabel)
	}
	if got := s.admit("app-free", api.PlanFree, otherDeploymentLabel); got != otherDeploymentLabel {
		t.Errorf("Free plan other admit = %q, want %q", got, otherDeploymentLabel)
	}
}

func TestDeploymentLabelSet_PlanCapLookupCached(t *testing.T) {
	// Indirectly verified by TestDeploymentLabelSet_PerAppIsolation
	// (Scale admits 50 without overflowing — the cap lookup
	// produced 200, not 10). This test pins the explicit
	// cap-by-plan contract: Hobby=10, Pro=50, Scale=200, Free=0.
	cases := []struct {
		plan api.Plan
		cap  int
	}{
		{api.PlanFree, 0},
		{api.PlanHobby, 10},
		{api.PlanPro, 50},
		{api.PlanScale, 200},
	}
	for _, tc := range cases {
		s := newDeploymentLabelSet()
		// Admit `cap` distinct deployment ids — all should pass.
		for i := 1; i <= tc.cap; i++ {
			id := deploymentIDForIndex(i)
			if got := s.admit("app", tc.plan, id); got != id {
				t.Errorf("plan=%v admit #%d = %q, want %q", tc.plan, i, got, id)
			}
		}
		// The (cap+1)-th collapses.
		got := s.admit("app", tc.plan, "deploy-overflow")
		if got != otherDeploymentLabel {
			t.Errorf("plan=%v overflow admit = %q, want %q", tc.plan, got, otherDeploymentLabel)
		}
	}
}

func deploymentIDForIndex(i int) string {
	// Deterministic deployment-id generator. Real deployment
	// ids are UUIDs; the test only needs distinct values.
	const hex = "0123456789abcdef"
	if i < 0 || i >= 16*16 {
		panic("deploymentIDForIndex: out of range")
	}
	hi := hex[i/16]
	lo := hex[i%16]
	return "deploy-00000000-0000-0000-0000-00000000000" + string(hi) + string(lo)
}

// Prod hunt r58 follow-up: a route update admits a new release of an app this
// gateway already serves, so the release's series exist before its first
// request; an app it has not served is left to its first request.
func TestDeploymentLabelSetAdmitSeen(t *testing.T) {
	s := newDeploymentLabelSet()
	var admitted []string
	s.onAdmit = func(appID, deploymentID string) { admitted = append(admitted, appID+"/"+deploymentID) }
	s.admitSeen("unseen", "d1")
	if len(admitted) != 0 {
		t.Fatalf("unseen app admitted: %v", admitted)
	}
	s.admit("app", api.PlanPro, "d1")
	s.admitSeen("app", "d2")
	s.admitSeen("app", "d2")
	if len(admitted) != 2 || admitted[1] != "app/d2" {
		t.Fatalf("admitted = %v, want app/d1 then app/d2 once", admitted)
	}
}

// A provisional id (admitted from a recorded target, never requested) gives
// its slot to a requested id when the app's cap is full; a requested id is
// never evicted, and a provisional id never evicts anything.
func TestDeploymentLabelSetRequestsEvictProvisionalIDs(t *testing.T) {
	s := newDeploymentLabelSet()
	var evicted []string
	s.onEvict = func(_, deploymentID string) { evicted = append(evicted, deploymentID) }
	limit := api.MustLimitsFor(api.PlanHobby).DebugTelemetryDeploymentsPerApp
	s.admit("app", api.PlanHobby, "live-0")
	for i := 1; i < limit; i++ {
		s.admitSeen("app", fmt.Sprintf("staged-%d", i))
	}
	s.admitSeen("app", "staged-extra")
	if len(evicted) != 0 {
		t.Fatalf("provisional admit evicted %v", evicted)
	}
	if got := s.admit("app", api.PlanHobby, "live-1"); got != "live-1" {
		t.Fatalf("request at full cap = %q, want its own label", got)
	}
	if len(evicted) != 1 || !strings.HasPrefix(evicted[0], "staged-") {
		t.Fatalf("evicted = %v, want one staged id", evicted)
	}

	requested := newDeploymentLabelSet()
	requested.onEvict = func(_, deploymentID string) { t.Fatalf("evicted requested id %s", deploymentID) }
	requested.admit("app", api.PlanHobby, "d0")
	for i := 1; i < limit; i++ {
		id := fmt.Sprintf("d%d", i)
		requested.admitSeen("app", id)
		requested.admit("app", api.PlanHobby, id)
	}
	if got := requested.admit("app", api.PlanHobby, "late"); got != otherDeploymentLabel {
		t.Fatalf("full set of requested ids admitted %q, want __other__", got)
	}
}
