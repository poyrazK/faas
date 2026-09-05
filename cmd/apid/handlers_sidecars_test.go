package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// goodSidecarImage is a placeholder valid image
// (sha256-pinned digest form, matches Sidecar.Validate's
// regex) so the test rows don't trip the per-element image
// gate before reaching the cap check.
const goodSidecarImage = "ghcr.io/me/x@sha256:" + "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"

func TestBuildDeploymentForInsert_ServiceDefaultsToReadinessRollout(t *testing.T) {
	app := state.App{
		ID:       "app-service",
		Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService},
	}
	dep, problem := buildDeploymentForInsert(app, &api.CreateDeploymentRequest{Image: "sha256:test"}, nil, testSidecarLimits())
	if problem != nil {
		t.Fatalf("buildDeploymentForInsert: %v", problem)
	}
	if !state.IsServiceRollout(dep) {
		t.Fatalf("deployment marker = state:%q canary_steps:%d; want readiness-gated service rollout", dep.RolloutState, dep.CanaryTotalSteps)
	}
	if dep.TrafficPercent != 0 || dep.RolloutStartedAt == nil {
		t.Fatalf("service rollout = traffic:%d started_at:%v; want traffic 0 and a start timestamp", dep.TrafficPercent, dep.RolloutStartedAt)
	}
}

// testSidecarLimits returns the per-plan Limits table
// the apid gate reads (cmd/apid/main.go populates this at
// boot from pkg/api::planLimits via the exported LimitsFor
// accessor). Reused so the cap test exercises the same
// limits the handler sees in production.
func testSidecarLimits() api.Limits {
	l, _ := api.LimitsFor(api.PlanHobby)
	return l
}

// TestValidateAndPlanSidecars_ThreeSidecarsRejected pins
// AC #3 of issue #463 / ADR-069 / PR-B at the apid
// handler level: a CreateDeploymentRequest carrying a
// 3-element sidecars array MUST surface the literal
// api.CodeSidecarCapExceeded via the handler's
// validateAndPlanSidecars gate. The earlier pkg/api DTO
// test pins the same wire code; this test confirms the
// handler chains it through to the *api.Problem that
// api.WriteProblem emits to the HTTP response.
//
// A regression that swaps the gate (e.g. removes the
// cap check, or changes the wire code to a near-synonym)
// fails this test in the same commit.
//
// Hobby is used because Hobby inherits the global 2-cap
// (PR-A's accessor returns true for every plan; the
// load-bearing gate is the GLOBAL SidecarCapMax constant,
// not a per-plan matrix). The per-sidecar RamMB is set to
// 32 MB — well above the 16 MB floor — so the cap check
// fires first, not the ram_mb gate.
func TestValidateAndPlanSidecars_ThreeSidecarsRejected(t *testing.T) {
	acct := state.Account{Plan: api.PlanHobby}
	limits := testSidecarLimits()
	req := &api.CreateDeploymentRequest{
		Sidecars: api.Sidecars{
			{Name: "a", Image: goodSidecarImage, Type: api.SidecarTypeInit, RamMB: 32},
			{Name: "b", Image: goodSidecarImage, Type: api.SidecarTypeSidecar, RamMB: 32},
			{Name: "c", Image: goodSidecarImage, Type: api.SidecarTypeInit, RamMB: 32},
		},
	}
	p := validateAndPlanSidecars(req, acct, limits)
	if p == nil {
		t.Fatal("validateAndPlanSidecars: expected Problem on 3-sidecar request, got nil")
	}
	if p.Code != api.CodeSidecarCapExceeded {
		t.Errorf("problem.Code = %q, want %q (RFC 7807 stable code, closed enum)",
			p.Code, api.CodeSidecarCapExceeded)
	}
	if p.Status != 400 {
		t.Errorf("problem.Status = %d, want 400", p.Status)
	}
	// Title must mention "sidecar" so the dashboard's
	// human-readable rendering stays useful (the wire
	// code is the load-bearing field; the title is the
	// UX hint).
	if !strings.Contains(strings.ToLower(p.Title), "sidecar") {
		t.Errorf("problem.Title = %q; want a substring mentioning sidecar", p.Title)
	}
}

// TestValidateAndPlanSidecars_TwoSidecarsAccepted pins the
// happy-path inverse: a 2-sidecar array (the cap) MUST NOT
// trip the gate. A regression that flips the comparison
// (< vs <=) would reject legitimate 2-sidecar deploys and
// fail this test.
func TestValidateAndPlanSidecars_TwoSidecarsAccepted(t *testing.T) {
	acct := state.Account{Plan: api.PlanHobby}
	limits := testSidecarLimits()
	req := &api.CreateDeploymentRequest{
		Sidecars: api.Sidecars{
			{Name: "a", Image: goodSidecarImage, Type: api.SidecarTypeInit, RamMB: 32},
			{Name: "b", Image: goodSidecarImage, Type: api.SidecarTypeSidecar, RamMB: 32},
		},
	}
	if p := validateAndPlanSidecars(req, acct, limits); p != nil {
		t.Errorf("validateAndPlanSidecars: expected nil on 2-sidecar request, got %+v", p)
	}
}

// TestValidateAndPlanSidecars_EmptySidecarsNoop pins the
// legacy tolerance: a request without sidecars MUST NOT
// trip the gate. A regression that always returns a
// Problem would block every pre-PR-B deploy.
func TestValidateAndPlanSidecars_EmptySidecarsNoop(t *testing.T) {
	acct := state.Account{Plan: api.PlanHobby}
	limits := testSidecarLimits()
	req := &api.CreateDeploymentRequest{}
	if p := validateAndPlanSidecars(req, acct, limits); p != nil {
		t.Errorf("validateAndPlanSidecars: expected nil on empty sidecars, got %+v", p)
	}
}
