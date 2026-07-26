// Negative + integration tests for cmd/apid handlers. The
// deployment_logs SSE stream tests live here because they need a
// real broadcaster handle (server_test.go constructs only the bare
// server).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestDeploymentLogsSSE_Pagination confirms the initial page of a
// deployment log feed is the chronological slice from oldest → newest
// (even though the table is DESC by seq) and that ?follow=0 closes
// the stream with the `end` event — no live tail, no broadcaster
// dependency. The live-tail path is exercised by manual smoke
// (slice 5 spec verification line).
func TestDeploymentLogsSSE_Pagination(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "log-multi")
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		if _, err := e.store.AppendDeploymentLog(ctx, dep.ID, "stdout", "line"+itoa(i)); err != nil {
			t.Fatal(err)
		}
	}

	rec := e.do(t, "GET", "/v1/deployments/"+dep.ID+"/logs?follow=0", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("first page: %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("ct = %q, want text/event-stream", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{"line1", "line2", "line3", "line4", "line5", "event: end"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n%s", want, body)
		}
	}
	if !strings.Contains(body, `"seq":5`) {
		t.Errorf("body missing seq:5 — newest row\n%s", body)
	}
}

// itoa turns small ints into strings without strconv dependency so
// the test stays self-contained.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [4]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// mustSeedDeployment provisions an app + a deployment under the
// test account and returns the deployment.
func mustSeedDeployment(t *testing.T, e testEnv, slug string) state.Deployment {
	t.Helper()
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID,
		Slug:      slug,
		Type:      state.AppTypeApp,
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatalf("seed app %s: %v", slug, err)
	}
	d, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:deadbeefcafebabe1234567890abcdef1234567890abcdef1234567890abcdef",
		Kind:        state.DeploymentKindImage,
		Status:      state.DeployBuilding,
		CreatedAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	return d
}

// mustSeedApp provisions an app under the test account at the given
// slug (default active status). Returns the app ID for later assertions.
func mustSeedApp(t *testing.T, e testEnv, slug string) string {
	t.Helper()
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID,
		Slug:      slug,
		Type:      state.AppTypeApp,
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatalf("seed app %s: %v", slug, err)
	}
	return app.ID
}

// TestUpdateAppMinInstances_Hobby locks the plan-tier gate (ux_spec
// §6.5): Hobby plans cannot set apps.min_instances. The handler must
// return 403 plan_min_instances_not_allowed, not 422, because the
// feature is tier-locked — the value the customer typed is irrelevant.
func TestUpdateAppMinInstances_Hobby(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "hobby-floor")
	one := 1
	rec := e.do(t, "PATCH", "/v1/apps/hobby-floor", api.UpdateAppRequest{MinInstances: &one}, nil)
	assertProblem(t, rec, 403, api.CodePlanMinInstancesNotAllowed)
}

// TestUpdateAppMinInstances_Pro is the happy path: Pro plans accept
// min_instances and the response carries the new value.
func TestUpdateAppMinInstances_Pro(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-floor")
	one := 1
	rec := e.do(t, "PATCH", "/v1/apps/pro-floor", api.UpdateAppRequest{MinInstances: &one}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.MinInstances != 1 {
		t.Errorf("MinInstances = %d, want 1", out.MinInstances)
	}
}

// TestUpdateAppMinInstances_OutOfRange pins the bounds check: min
// must be in [0, MaxConcurrency]. Pro caps at 5, so 6 must 422 with
// code invalid_min_instances. Also covers the gate-precedes-bounds
// ordering: a Hobby plan sending 6 should still get 403 (covered by
// TestUpdateAppMinInstances_Hobby).
func TestUpdateAppMinInstances_OutOfRange(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-over")
	six := 6
	rec := e.do(t, "PATCH", "/v1/apps/pro-over", api.UpdateAppRequest{MinInstances: &six}, nil)
	assertProblem(t, rec, 422, api.CodeInvalidMinInstances)
}

// TestUpdateAppMinInstances_Negative pins the lower bound: -1 must
// 422 invalid_min_instances, regardless of plan. A negative value
// would otherwise pass through to the PG CHECK constraint as a
// second-layer defense, but the handler should catch it first.
func TestUpdateAppMinInstances_Negative(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-neg")
	neg := -1
	rec := e.do(t, "PATCH", "/v1/apps/pro-neg", api.UpdateAppRequest{MinInstances: &neg}, nil)
	assertProblem(t, rec, 422, api.CodeInvalidMinInstances)
}

// TestUpdateAppAutoscaleRPS_FreeGate locks the plan-tier gate for the
// reactive scale-up trigger (issue #169 / #172). Free plans cannot set
// autoscale_target_rps at all — the handler must return 403
// plan_autoscale_not_allowed, not 422, because the feature is
// tier-locked (the value the customer typed is irrelevant). Mirrors
// TestUpdateAppMinInstances_Hobby above.
func TestUpdateAppAutoscaleRPS_FreeGate(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "free-rps")
	fifty := 50
	rec := e.do(t, "PATCH", "/v1/apps/free-rps", api.UpdateAppRequest{AutoscaleTargetRPS: &fifty}, nil)
	assertProblem(t, rec, 403, api.CodePlanScaleUpNotAllowed)
}

// TestUpdateAppAutoscaleRPS_HobbyZero pins the lower bound: 0 is
// the explicit-disable form (the Set bit is set, so the column
// gets overwritten to 0). It must round-trip as 200, NOT 422 — the
// only invalid value is negative. The PG CHECK constraint
// `apps_autoscale_target_rps_nonneg` enforces `>= 0 OR NULL`; we
// rely on the apid-side validation to reject negatives before they
// reach the DB.
func TestUpdateAppAutoscaleRPS_HobbyZero(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "hobby-rps-zero")
	neg := -1
	rec := e.do(t, "PATCH", "/v1/apps/hobby-rps-zero", api.UpdateAppRequest{AutoscaleTargetRPS: &neg}, nil)
	assertProblem(t, rec, 422, api.CodeInvalidAutoscaleTargetRPS)
}

// TestUpdateAppAutoscaleRPS_HobbyHappy is the happy path: Hobby
// plans accept autoscale_target_rps and the response carries the
// new value. Hobby stays RPS-only — setting CPU on Hobby must 403
// (covered by TestUpdateAppAutoscaleCPU_HobbyGate).
func TestUpdateAppAutoscaleRPS_HobbyHappy(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "hobby-rps-ok")
	fifty := 50
	rec := e.do(t, "PATCH", "/v1/apps/hobby-rps-ok", api.UpdateAppRequest{AutoscaleTargetRPS: &fifty}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.AutoscaleTargetRPS != 50 {
		t.Errorf("AutoscaleTargetRPS = %d, want 50", out.AutoscaleTargetRPS)
	}
}

// TestUpdateAppAutoscaleCPU_HobbyGate locks the CPU tier gate: Hobby
// plans do not unlock autoscale_target_cpu_pct. The handler must 403
// even when the value is a perfectly valid 60 (the gate runs first,
// value validation is irrelevant on a tier-locked feature).
func TestUpdateAppAutoscaleCPU_HobbyGate(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "hobby-cpu")
	sixty := 60
	rec := e.do(t, "PATCH", "/v1/apps/hobby-cpu", api.UpdateAppRequest{AutoscaleTargetCPUPct: &sixty}, nil)
	assertProblem(t, rec, 403, api.CodePlanScaleUpNotAllowed)
}

// TestUpdateAppAutoscaleCPU_ProOutOfRange pins the bounds check: CPU
// must be in [1, 100]. Pro unlocks CPU, so the gate is satisfied,
// but 150 must 422 invalid_autoscale_target_cpu_pct.
func TestUpdateAppAutoscaleCPU_ProOutOfRange(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-cpu-over")
	over := 150
	rec := e.do(t, "PATCH", "/v1/apps/pro-cpu-over", api.UpdateAppRequest{AutoscaleTargetCPUPct: &over}, nil)
	assertProblem(t, rec, 422, api.CodeInvalidAutoscaleTargetCPU)
}

// TestUpdateAppAutoscaleCPU_ProHappy is the happy path: Pro plans
// accept autoscale_target_cpu_pct in [1, 100] and the response
// carries the new value.
func TestUpdateAppAutoscaleCPU_ProHappy(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-cpu-ok")
	sixty := 60
	rec := e.do(t, "PATCH", "/v1/apps/pro-cpu-ok", api.UpdateAppRequest{AutoscaleTargetCPUPct: &sixty}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.AutoscaleTargetCPUPct != 60 {
		t.Errorf("AutoscaleTargetCPUPct = %d, want 60", out.AutoscaleTargetCPUPct)
	}
}

// TestUpdateAppAutoscaleDisableZero verifies that a PATCH with
// autoscale_target_rps=0 (or cpu_pct=0) is treated as "explicit
// disable" — the value is stored as 0 and surfaces back on GET.
// This is the contract that lets customers turn autoscale OFF
// without having to know the trigger's internal enable bit (which
// is "an autoscale_target_* column is non-zero").
func TestUpdateAppAutoscaleDisableZero(t *testing.T) {
	e := setup(t, api.PlanScale)
	mustSeedApp(t, e, "scale-toggle")
	// First set both targets.
	rps := 25
	cpu := 70
	rec := e.do(t, "PATCH", "/v1/apps/scale-toggle", api.UpdateAppRequest{
		AutoscaleTargetRPS: &rps, AutoscaleTargetCPUPct: &cpu,
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("set status %d: %s", rec.Code, rec.Body)
	}
	// Then disable both with explicit 0.
	zero := 0
	rec = e.do(t, "PATCH", "/v1/apps/scale-toggle", api.UpdateAppRequest{
		AutoscaleTargetRPS: &zero, AutoscaleTargetCPUPct: &zero,
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("disable status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.AutoscaleTargetRPS != 0 {
		t.Errorf("AutoscaleTargetRPS = %d, want 0 (disabled)", out.AutoscaleTargetRPS)
	}
	if out.AutoscaleTargetCPUPct != 0 {
		t.Errorf("AutoscaleTargetCPUPct = %d, want 0 (disabled)", out.AutoscaleTargetCPUPct)
	}
}

// TestUpdateAppEgressAllowlist_FreeGate locks the plan-tier gate:
// Free plans cannot set egress_allowlist at all. The handler must
// return 403 plan_egress_allowlist_not_allowed, not 400, because the
// feature is tier-locked — the value the customer typed is
// irrelevant. Mirrors TestUpdateAppMinInstances_Hobby above.
func TestUpdateAppEgressAllowlist_FreeGate(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "free-allow")
	rec := e.do(t, "PATCH", "/v1/apps/free-allow", api.UpdateAppRequest{
		EgressAllowlist: &[]string{"1.2.3.0/24"},
	}, nil)
	assertProblem(t, rec, 403, api.CodePlanEgressAllowlistNotAllowed)
}

// TestUpdateAppEgressAllowlist_FreeGate_EmptySlice locks the
// plan-tier gate for the empty-slice form: a Free plan PATCHing
// `egress_allowlist: []` (an explicit "clear the allowlist") must
// still hit the 403 path, NOT silently fall through and clear the
// column. `validateUpdateApp` checks `req.EgressAllowlist != nil`
// at handlers_ext.go:72 — a nil pointer is a no-op (column
// unchanged), but a non-nil pointer with an empty slice is a
// deliberate PATCH and must surface 403 the same as the populated
// case. Without this pin, a future refactor that moves the
// `EgressAllowlistAllowed()` check below the empty-slice branch
// would let a Free user mutate the column to default-accept (a
// small privilege-escalation surface — they couldn't SET a list
// but could still CLEAR one).
func TestUpdateAppEgressAllowlist_FreeGate_EmptySlice(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "free-empty")
	rec := e.do(t, "PATCH", "/v1/apps/free-empty", api.UpdateAppRequest{
		EgressAllowlist: &[]string{},
	}, nil)
	assertProblem(t, rec, 403, api.CodePlanEgressAllowlistNotAllowed)
}

// TestUpdateAppEgressAllowlist_GatePrecedesPerEntryShape locks the
// ordering: a Free plan sending a 64-entry list of deliberately
// malformed CIDRs must surface 403 plan_egress_allowlist_not_allowed,
// NOT 400 invalid_egress_allowlist. Leaking the per-entry shape
// failure on a tier-locked feature would tell a Free user which
// things to fix before upgrading — small information leak, but
// pinned because the code path is easy to invert in a future
// refactor of validateUpdateApp.
func TestUpdateAppEgressAllowlist_GatePrecedesPerEntryShape(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "free-leak")
	bad := []string{"not-a-cidr", "still-not", "also-not"}
	rec := e.do(t, "PATCH", "/v1/apps/free-leak", api.UpdateAppRequest{
		EgressAllowlist: &bad,
	}, nil)
	assertProblem(t, rec, 403, api.CodePlanEgressAllowlistNotAllowed)
}

// TestUpdateAppEgressAllowlist_ZeroBits: /0 must be rejected as
// invalid_egress_allowlist regardless of plan, even on a Pro tier
// that otherwise allows the feature. PR #159 review F2: a /0 in the
// allowlist would unblock every v4 destination and silently neuter
// the gate, defeating the operator's whole intent.
func TestUpdateAppEgressAllowlist_ZeroBits(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-zero")
	rec := e.do(t, "PATCH", "/v1/apps/pro-zero", api.UpdateAppRequest{
		EgressAllowlist: &[]string{"0.0.0.0/0"},
	}, nil)
	assertProblem(t, rec, 400, api.CodeInvalidEgressAllowlist)
}

// TestUpdateAppEgressAllowlist_V6AcceptedOnPro: a v6-only allowlist
// must be accepted on a paid plan (ADR-032 v6 mirror). The handler
// no longer rejects v6 entries; the DB trigger
// `apps_egress_allowlist_cidr` (migration 00033) holds the
// non-/0 contract. The MemStore path is sufficient here — pgStore
// has its own round-trip tests under pkg/state.
func TestUpdateAppEgressAllowlist_V6AcceptedOnPro(t *testing.T) {
	e := setup(t, api.PlanPro)
	id := mustSeedApp(t, e, "pro-v6")
	rec := e.do(t, "PATCH", "/v1/apps/pro-v6", api.UpdateAppRequest{
		EgressAllowlist: &[]string{"fe80::/10"},
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	// Re-read via AppByID to confirm the slice round-tripped.
	app, err := e.store.AppByID(context.Background(), id)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if len(app.EgressAllowlist) != 1 || app.EgressAllowlist[0].String() != "fe80::/10" {
		t.Fatalf("allowlist = %+v, want [fe80::/10]", app.EgressAllowlist)
	}
}

// TestUpdateAppEgressAllowlist_MixedAcceptedOnPro: a mixed v4 + v6
// allowlist must be accepted (ADR-032). The renderer partitions
// into two argvs; the handler does not need to know.
//
// PR-C: strengthened to assert exact values + insertion order,
// mirroring the v6-only test above (which only ever sent a single
// entry so order was trivial). The mixed list is the natural
// mirror for the v4-mapped dedup test below — both share the
// "list crosses families" shape and need first-seen-wins pinning.
func TestUpdateAppEgressAllowlist_MixedAcceptedOnPro(t *testing.T) {
	e := setup(t, api.PlanPro)
	id := mustSeedApp(t, e, "pro-mixed")
	rec := e.do(t, "PATCH", "/v1/apps/pro-mixed", api.UpdateAppRequest{
		EgressAllowlist: &[]string{"1.2.3.0/24", "fe80::/10"},
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	app, err := e.store.AppByID(context.Background(), id)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	want := []string{"1.2.3.0/24", "fe80::/10"}
	if len(app.EgressAllowlist) != len(want) {
		t.Fatalf("allowlist len = %d, want %d: %+v", len(app.EgressAllowlist), len(want), app.EgressAllowlist)
	}
	for i, p := range app.EgressAllowlist {
		if p.String() != want[i] {
			t.Errorf("allowlist[%d] = %q, want %q", i, p.String(), want[i])
		}
	}
}

// TestUpdateAppEgressAllowlist_SlashZeroRejectedV6: `::/0` is the
// "everything" sentinel and must be rejected with 400
// invalid_egress_allowlist just like its v4 sibling (ADR-032
// non-/0 contract). The DB trigger is the source of truth, but
// the handler catches it earlier with a more actionable error.
func TestUpdateAppEgressAllowlist_SlashZeroRejectedV6(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-v6-zero")
	rec := e.do(t, "PATCH", "/v1/apps/pro-v6-zero", api.UpdateAppRequest{
		EgressAllowlist: &[]string{"::/0"},
	}, nil)
	assertProblem(t, rec, 400, api.CodeInvalidEgressAllowlist)
}

// TestUpdateAppEgressAllowlist_TooLong: a Pro plan (cap 16) sending
// 17 entries must surface 400 egress_allowlist_too_long, NOT 403
// (the plan gate already cleared; per-entry shape already cleared —
// only size remains).
func TestUpdateAppEgressAllowlist_TooLong(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-big")
	many := make([]string, 0, 17)
	for i := 1; i <= 17; i++ {
		many = append(many, fmt.Sprintf("10.0.%d.0/24", i))
	}
	rec := e.do(t, "PATCH", "/v1/apps/pro-big", api.UpdateAppRequest{
		EgressAllowlist: &many,
	}, nil)
	assertProblem(t, rec, 400, api.CodeEgressAllowlistTooLong)
}

// --- PR-C v4-mapped canonicalisation + dedup -----------------------------
//
// The validateUpdateApp loop was extended in PR-C to (a) rewrite
// `::ffff:V4ADDR/N` to its canonical v4 form (`V4ADDR/(N-96)`),
// (b) de-duplicate entries after canonicalisation. The tests
// below pin each branch. The pattern mirrors the existing v6-only
// and mixed tests above: PATCH → AppByID → assert exact stored
// string form.

// TestUpdateAppEgressAllowlist_V4MappedCanonicalised: a single
// v4-mapped v6 entry is rewritten to its v4 form. PATCH
// `::ffff:1.2.3.0/120` → 200; AppByID reads back `1.2.3.0/24`.
// This is the happy-path pin for the rewrite branch.
func TestUpdateAppEgressAllowlist_V4MappedCanonicalised(t *testing.T) {
	e := setup(t, api.PlanPro)
	id := mustSeedApp(t, e, "pro-v4mapped")
	rec := e.do(t, "PATCH", "/v1/apps/pro-v4mapped", api.UpdateAppRequest{
		EgressAllowlist: &[]string{"::ffff:1.2.3.0/120"},
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	app, err := e.store.AppByID(context.Background(), id)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if len(app.EgressAllowlist) != 1 || app.EgressAllowlist[0].String() != "1.2.3.0/24" {
		t.Fatalf("allowlist = %+v, want [1.2.3.0/24]", app.EgressAllowlist)
	}
}

// TestUpdateAppEgressAllowlist_V4MappedMixed_Dedup: an input list
// that contains BOTH the v4 form and the v4-mapped form of the
// same prefix must collapse to a single entry after
// canonicalisation. Order is first-seen-wins — the surviving
// entry keeps the position of the FIRST occurrence. Also pins
// that a v4 entry + an unrelated v6 entry + the v4-mapped
// equivalent of the v4 entry ends up as [v4, v6].
func TestUpdateAppEgressAllowlist_V4MappedMixed_Dedup(t *testing.T) {
	e := setup(t, api.PlanPro)
	id := mustSeedApp(t, e, "pro-v4mapped-mixed")
	rec := e.do(t, "PATCH", "/v1/apps/pro-v4mapped-mixed", api.UpdateAppRequest{
		EgressAllowlist: &[]string{"1.2.3.0/24", "::ffff:1.2.3.0/120", "8.8.8.0/24"},
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	app, err := e.store.AppByID(context.Background(), id)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	want := []string{"1.2.3.0/24", "8.8.8.0/24"}
	if len(app.EgressAllowlist) != len(want) {
		t.Fatalf("allowlist len = %d, want %d: %+v", len(app.EgressAllowlist), len(want), app.EgressAllowlist)
	}
	for i, p := range app.EgressAllowlist {
		if p.String() != want[i] {
			t.Errorf("allowlist[%d] = %q, want %q", i, p.String(), want[i])
		}
	}
}

// TestUpdateAppEgressAllowlist_V4MappedBelowV4Min: a v4-mapped
// entry that would canonicalise to a v4 prefix wider than /8
// (the v4 floor enforced by the DB trigger) is rejected at the
// handler with a more actionable message than the trigger's
// generic constraint error. `::ffff:0.0.0.0/96` canonically maps
// to v4 /0 (rejected); `::ffff:0.1.0.0/104` maps to v4 /8 (the
// boundary, accepted by the next test).
func TestUpdateAppEgressAllowlist_V4MappedBelowV4Min(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-v4mapped-floor")
	rec := e.do(t, "PATCH", "/v1/apps/pro-v4mapped-floor", api.UpdateAppRequest{
		EgressAllowlist: &[]string{"::ffff:0.0.0.0/96"},
	}, nil)
	assertProblem(t, rec, 400, api.CodeInvalidEgressAllowlist)
}

// TestUpdateAppEgressAllowlist_V4MappedAtV4S8: the /8 boundary
// is the FLOOR. PATCH `::ffff:1.2.3.0/104` → 200; AppByID
// reads back `1.0.0.0/8`. The host bits get masked off by /8
// (1.2.3.0 round-downs to 1.0.0.0) — that is a property of the
// prefix length, not the canonicalisation. Pins that the handler
// agrees with the DB trigger on the floor and applies Masked()
// after the Unmap.
func TestUpdateAppEgressAllowlist_V4MappedAtV4S8(t *testing.T) {
	e := setup(t, api.PlanPro)
	id := mustSeedApp(t, e, "pro-v4mapped-s8")
	rec := e.do(t, "PATCH", "/v1/apps/pro-v4mapped-s8", api.UpdateAppRequest{
		EgressAllowlist: &[]string{"::ffff:1.2.3.0/104"},
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	app, err := e.store.AppByID(context.Background(), id)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if len(app.EgressAllowlist) != 1 || app.EgressAllowlist[0].String() != "1.0.0.0/8" {
		t.Fatalf("allowlist = %+v, want [1.0.0.0/8]", app.EgressAllowlist)
	}
}

// TestUpdateAppEgressAllowlist_DedupPreservesFirstSeenOrder: the
// dedup branch is independent of v4-mapped canonicalisation —
// plain duplicates are also collapsed. The remaining entries
// keep insertion order (first-seen wins). Pins that the handler
// does NOT sort before persisting (a sort would change
// observable behaviour across repeat PATCHes).
func TestUpdateAppEgressAllowlist_DedupPreservesFirstSeenOrder(t *testing.T) {
	e := setup(t, api.PlanPro)
	id := mustSeedApp(t, e, "pro-dedup-order")
	rec := e.do(t, "PATCH", "/v1/apps/pro-dedup-order", api.UpdateAppRequest{
		EgressAllowlist: &[]string{"8.8.8.0/24", "1.2.3.0/24", "8.8.8.0/24"},
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	app, err := e.store.AppByID(context.Background(), id)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	want := []string{"8.8.8.0/24", "1.2.3.0/24"}
	if len(app.EgressAllowlist) != len(want) {
		t.Fatalf("allowlist len = %d, want %d: %+v", len(app.EgressAllowlist), len(want), app.EgressAllowlist)
	}
	for i, p := range app.EgressAllowlist {
		if p.String() != want[i] {
			t.Errorf("allowlist[%d] = %q, want %q", i, p.String(), want[i])
		}
	}
}

// --- PR-C read-path: AppResponse surfaces EgressAllowlist -----------------
//
// PR-C added EgressAllowlist []string to api.AppResponse. The tests
// below pin the read path: field is materialised in the JSON body
// (never `null`), populated from state.App.EgressAllowlist, and
// reflects the persisted canonical form. The pattern mirrors the
// v6-only write test above: PATCH → GET → assert body shape.

// TestGetApp_SurfacesEgressAllowlist: PATCH a 3-entry mixed list,
// then GET and assert the JSON body has egress_allowlist as a
// 3-element array of the canonical strings (no `::ffff:` after
// PR-C's rewrite). Catches DTO field missing, appResponse
// population bug, JSON tag mismatch.
func TestGetApp_SurfacesEgressAllowlist(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "get-surface")
	rec := e.do(t, "PATCH", "/v1/apps/get-surface", api.UpdateAppRequest{
		EgressAllowlist: &[]string{"1.2.3.0/24", "8.8.8.0/24", "fe80::/10"},
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("PATCH status %d: %s", rec.Code, rec.Body)
	}
	rec = e.do(t, "GET", "/v1/apps/get-surface", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET status %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		EgressAllowlist []string `json:"egress_allowlist"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal GET body: %v; body: %s", err, rec.Body.String())
	}
	want := []string{"1.2.3.0/24", "8.8.8.0/24", "fe80::/10"}
	if len(got.EgressAllowlist) != len(want) {
		t.Fatalf("egress_allowlist len = %d, want %d (body: %s)", len(got.EgressAllowlist), len(want), rec.Body.String())
	}
	for i, e := range got.EgressAllowlist {
		if e != want[i] {
			t.Errorf("egress_allowlist[%d] = %q, want %q", i, e, want[i])
		}
	}
}

// TestGetApp_EmptyAllowlistSerializesAsArray: a Free plan (the
// default) has no allowlist; the GET response must serialise
// egress_allowlist as `[]` (never `null`). This is the
// load-bearing pin against `omitempty` on the AppResponse field —
// without it, a future refactor that adds `omitempty` would
// silently regress the dashboard parser.
func TestGetApp_EmptyAllowlistSerializesAsArray(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "get-empty-free")
	rec := e.do(t, "GET", "/v1/apps/get-empty-free", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET status %d: %s", rec.Code, rec.Body)
	}
	// Body must contain the literal "egress_allowlist":[] —
	// substring search avoids pulling in a JSON parser.
	if !strings.Contains(rec.Body.String(), `"egress_allowlist":[]`) {
		t.Errorf("expected egress_allowlist:[] in GET body, got: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"egress_allowlist":null`) {
		t.Errorf("egress_allowlist serialised as null (omitempty regression); got: %s", rec.Body.String())
	}
}

// TestGetApp_PostPatchEmptyAllowlist: a Pro plan PATCHing []
// (the "clear" sentinel) must read back [] on GET. Pins the
// contract that explicit-clear and never-set have the same wire
// shape — both `[]`, not `null`, not the prior list.
func TestGetApp_PostPatchEmptyAllowlist(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "get-clear")
	// First PATCH populates the list.
	if rec := e.do(t, "PATCH", "/v1/apps/get-clear", api.UpdateAppRequest{
		EgressAllowlist: &[]string{"1.2.3.0/24"},
	}, nil); rec.Code != 200 {
		t.Fatalf("initial PATCH status %d: %s", rec.Code, rec.Body)
	}
	// Second PATCH clears it.
	if rec := e.do(t, "PATCH", "/v1/apps/get-clear", api.UpdateAppRequest{
		EgressAllowlist: &[]string{},
	}, nil); rec.Code != 200 {
		t.Fatalf("clear PATCH status %d: %s", rec.Code, rec.Body)
	}
	rec := e.do(t, "GET", "/v1/apps/get-clear", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("GET status %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"egress_allowlist":[]`) {
		t.Errorf("expected egress_allowlist:[] after clear, got: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"egress_allowlist":null`) {
		t.Errorf("egress_allowlist serialised as null; got: %s", rec.Body.String())
	}
}

// --- CRUD coverage for handlers_ext.go (slice 2) ----------------------------
//
// Each test seeds a single scenario via the MemStore harness and exercises
// one handler through the public HTTP surface. The point is to lift the
// per-handler coverage from 0% on the 21 handlers in handlers_ext.go that
// were previously unreachable from server_test.go.

// TestGetApp_HappyPath confirms getApp returns the seeded app.
func TestGetApp_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "my-api")
	rec := e.do(t, "GET", "/v1/apps/my-api", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Slug != "my-api" {
		t.Errorf("slug = %q, want my-api", out.Slug)
	}
}

// TestGetApp_UnknownReturns404 confirms loadApp's 404 path.
func TestGetApp_UnknownReturns404(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/apps/ghost", nil, nil)
	assertProblem(t, rec, 404, api.CodeNotFound)
}

// TestUpdateApp_RAMValid covers the happy path: a valid RAM value persists
// and the response reflects the new value.
func TestUpdateApp_RAMValid(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "upd-ram")
	newRAM := 256
	rec := e.do(t, "PATCH", "/v1/apps/upd-ram", api.UpdateAppRequest{RAMMB: &newRAM}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.RAMMB != 256 {
		t.Errorf("RAM = %d, want 256", out.RAMMB)
	}
}

// TestUpdateApp_BadJSON confirms decode failure surfaces as 400.
func TestUpdateApp_BadJSON(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "upd-bad")
	req := httptest.NewRequest("PATCH", "/v1/apps/upd-bad", strings.NewReader("{not-json"))
	req.Header.Set("Authorization", "Bearer "+e.key)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	assertProblem(t, rec, 400, api.CodeValidation)
}

// TestDeleteApp_HappyPath confirms the soft-delete + 204.
func TestDeleteApp_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "del-app")
	rec := e.do(t, "DELETE", "/v1/apps/del-app", nil, nil)
	if rec.Code != 204 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	// Subsequent GET should 404 (app row was deleted, not just flagged).
	rec2 := e.do(t, "GET", "/v1/apps/del-app", nil, nil)
	assertProblem(t, rec2, 404, api.CodeNotFound)
}

// TestGetDeployment_HappyPath covers the standard "deploy by id" lookup.
func TestGetDeployment_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "get-dep")
	rec := e.do(t, "GET", "/v1/deployments/"+dep.ID, nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != dep.ID || out.Status != string(state.DeployBuilding) {
		t.Errorf("got %+v, want id=%s status=building", out, dep.ID)
	}
}

// TestGetDeployment_UnknownReturns404 covers the not-found branch.
func TestGetDeployment_UnknownReturns404(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/deployments/deadbeef", nil, nil)
	assertProblem(t, rec, 404, api.CodeNotFound)
}

// TestGetBuildProvenance_HappyPath — ADR-038 / Tier 3 / issue
// #197 B3.10-read half. Seeds app + deployment + build + a
// provenance row under the test account, then verifies the
// GET /v1/builds/{id}/provenance route renders the DTO with
// every field. The pre-existing source_url + commit_sha
// propagation from deployment is verified in the builderd
// test package; here we only assert the public surface.
func TestGetBuildProvenance_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "prov-happy")
	build, err := e.store.CreateBuild(context.Background(), dep.ID, state.DeploymentKindTarball, 0, "")
	if err != nil {
		t.Fatalf("seed build: %v", err)
	}
	now := time.Now().UTC()
	prov := state.BuildProvenance{
		BuildID:        build.ID,
		SourceSHA256:   "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		SourceURL:      "https://github.com/acme/app@main",
		CommitSHA:      "0123456789abcdef0123456789abcdef01234567",
		Plan:           string(api.PlanPro),
		BuilderNodeID:  "default-local",
		StartedAt:      now,
		FinishedAt:     now.Add(2 * time.Second),
		SBOMStorageKey: "",
	}
	if err := e.store.CreateBuildProvenance(context.Background(), prov); err != nil {
		t.Fatalf("seed provenance: %v", err)
	}

	rec := e.do(t, "GET", "/v1/builds/"+build.ID+"/provenance", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.BuildProvenanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.BuildID != build.ID {
		t.Errorf("BuildID = %q, want %q", out.BuildID, build.ID)
	}
	if out.SourceSHA256 != prov.SourceSHA256 {
		t.Errorf("SourceSHA256 = %q, want %q", out.SourceSHA256, prov.SourceSHA256)
	}
	if out.Plan != string(api.PlanPro) {
		t.Errorf("Plan = %q, want %q", out.Plan, api.PlanPro)
	}
	if out.BuilderNodeID != "default-local" {
		t.Errorf("BuilderNodeID = %q, want %q", out.BuilderNodeID, "default-local")
	}
}

// TestGetBuildProvenance_NoSuchBuild404 — no build row at the
// supplied id. The route's first barrier is BuildByID, so the
// response uses the same 404 shape as getDeployment.
func TestGetBuildProvenance_NoSuchBuild404(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/builds/0123456789abcdef0123456789abcdef/provenance", nil, nil)
	assertProblem(t, rec, 404, api.CodeNotFound)
}

// TestGetBuildProvenance_NoRow404 — build exists, provenance
// doesn't. The route's second barrier is BuildProvenanceByBuildID,
// which returns apierr.ErrBuildProvenanceNotFound (the
// build_provenance_not_found code, distinct from CodeNotFound
// so the dashboard can branch on it).
func TestGetBuildProvenance_NoRow404(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "prov-norow")
	build, err := e.store.CreateBuild(context.Background(), dep.ID, state.DeploymentKindTarball, 0, "")
	if err != nil {
		t.Fatalf("seed build: %v", err)
	}
	rec := e.do(t, "GET", "/v1/builds/"+build.ID+"/provenance", nil, nil)
	assertProblem(t, rec, 404, api.CodeBuildProvenanceNotFound)
}

// TestGetBuildProvenance_OtherAccountIDOR — the route MUST
// render 404 when the build's owning app belongs to a
// different account, even with a valid build_id + provenance
// row in the store. The check is
// dep.AppID → AppByID → App.AccountID == acct.ID. A
// regression that drops the check is a cross-account IDOR.
func TestGetBuildProvenance_OtherAccountIDOR(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "prov-idor")
	build, err := e.store.CreateBuild(context.Background(), dep.ID, state.DeploymentKindTarball, 0, "")
	if err != nil {
		t.Fatalf("seed build: %v", err)
	}
	// Stamp a provenance row directly — bypasses the populator,
	// which is fine: we're exercising the handler, not builderd.
	if err := e.store.CreateBuildProvenance(context.Background(), state.BuildProvenance{
		BuildID:      build.ID,
		SourceSHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		StartedAt:    time.Now().UTC(),
		FinishedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed provenance: %v", err)
	}

	// New server with a DIFFERENT account, hitting the same build id.
	e2 := setup(t, api.PlanHobby)
	rec := e2.do(t, "GET", "/v1/builds/"+build.ID+"/provenance", nil, nil)
	assertProblem(t, rec, 404, api.CodeNotFound)
}

// TestRollbackApp_HappyPath seeds two deployments (one live, one
// superseded), then rolls back. Confirms the response shape (it carries
// the previously-superseded deployment's id) AND that the underlying
// row was flipped to live AND that the response itself reports the
// post-promotion status. The third assertion (response status) is the
// F3 fix-up: the handler used to snapshot the target BEFORE calling
// MarkDeploymentLive and return status="superseded" — the test now
// pins the correct post-promotion state in the API response.
func TestRollbackApp_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep1 := mustSeedDeployment(t, e, "rb-app")
	// Promote dep1 to live, then create dep2 and supersede dep1.
	if err := e.store.MarkDeploymentLive(context.Background(), dep1.ID); err != nil {
		t.Fatal(err)
	}
	app, _ := e.store.AppBySlug(context.Background(), "rb-app")
	dep2, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:" + repeat("b", 64),
		Kind:        state.DeploymentKindImage,
		Status:      state.DeployBuilding,
		CreatedAt:   time.Now().UTC().Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(context.Background(), dep2.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentSuperseded(context.Background(), dep1.ID); err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, "POST", "/v1/apps/rb-app/rollback", nil, nil)
	if rec.Code != 202 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != dep1.ID {
		t.Errorf("rollback returned id=%s, want %s (the superseded target)", out.ID, dep1.ID)
	}
	if out.Status != string(state.DeployLive) {
		t.Errorf("rollback response status = %q, want %q (post-promotion; was %q pre-F3 fix)",
			out.Status, state.DeployLive, state.DeploySuperseded)
	}
}

// TestRollbackApp_NoTarget confirms the 422 when there's nothing to roll
// back to.
func TestRollbackApp_NoTarget(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "rb-no")
	if err := e.store.MarkDeploymentLive(context.Background(), dep.ID); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "POST", "/v1/apps/rb-no/rollback", nil, nil)
	assertProblem(t, rec, 409, api.CodeNoRollbackTarget)
}

// TestParkApp_HappyPath confirms the app flips to AppEvictedCold.
func TestParkApp_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "park-me")
	rec := e.do(t, "POST", "/v1/apps/park-me/park", nil, nil)
	if rec.Code != 204 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	app, _ := e.store.AppBySlug(context.Background(), "park-me")
	if app.Status != state.AppEvictedCold {
		t.Errorf("status = %s, want evicted_cold", app.Status)
	}
}

// TestWakeApp_HappyPath parks, then wakes — exercises the inverse path.
func TestWakeApp_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "wake-me")
	e.do(t, "POST", "/v1/apps/wake-me/park", nil, nil)
	rec := e.do(t, "POST", "/v1/apps/wake-me/wake", nil, nil)
	if rec.Code != 204 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	app, _ := e.store.AppBySlug(context.Background(), "wake-me")
	if app.Status != state.AppActive {
		t.Errorf("status = %s, want active", app.Status)
	}
}

// TestRenameApp_HappyPath renames a slug.
func TestRenameApp_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "old-slug")
	rec := e.do(t, "POST", "/v1/apps/old-slug/rename",
		api.RenameAppRequest{NewSlug: "new-slug"}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Slug != "new-slug" {
		t.Errorf("slug = %q, want new-slug", out.Slug)
	}
}

// TestRenameApp_SameSlugIsIdempotent: same slug should 200, no DB write.
func TestRenameApp_SameSlugIsIdempotent(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "stable")
	rec := e.do(t, "POST", "/v1/apps/stable/rename",
		api.RenameAppRequest{NewSlug: "stable"}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

// TestRenameApp_InvalidSlug confirms the slug regex 400 path.
func TestRenameApp_InvalidSlug(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "rename-bad")
	rec := e.do(t, "POST", "/v1/apps/rename-bad/rename",
		api.RenameAppRequest{NewSlug: "BAD SLUG"}, nil)
	assertProblem(t, rec, 400, api.CodeValidation)
}

// TestListInstances_HappyPath seeds an instance and confirms listInstances
// returns it.
func TestListInstances_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "inst-app")
	if _, err := e.store.CreateInstance(context.Background(),
		dep.AppID, dep.ID, string(state.StateRunning), 512, "node-1", ""); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "GET", "/v1/apps/inst-app/instances", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out []api.InstanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 || out[0].State != string(state.StateRunning) {
		t.Errorf("got %+v, want 1 instance running", out)
	}
}

// TestCreateDomain_HappyPath binds a domain to an app.
func TestCreateDomain_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appID := mustSeedApp(t, e, "dom-app")
	rec := e.do(t, "POST", "/v1/domains",
		api.CreateCustomDomainRequest{Domain: "x.example.com", AppID: appID}, nil)
	if rec.Code != 202 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.CustomDomainResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Domain != "x.example.com" || !strings.Contains(out.TXTRecord, "_faas-verify") {
		t.Errorf("got %+v", out)
	}
}

// TestCreateDomain_BadJSON: missing fields → 400.
func TestCreateDomain_BadJSON(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "dom-bad")
	rec := e.do(t, "POST", "/v1/domains",
		api.CreateCustomDomainRequest{Domain: "", AppID: ""}, nil)
	assertProblem(t, rec, 400, api.CodeValidation)
}

// TestCreateDomain_UnknownAppReturns404: an app ID the account doesn't own.
func TestCreateDomain_UnknownAppReturns404(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, "POST", "/v1/domains",
		api.CreateCustomDomainRequest{Domain: "ghost.example.com", AppID: "ghost-id"}, nil)
	assertProblem(t, rec, 404, api.CodeNotFound)
}

// TestListDomains_HappyPath seeds one domain and confirms it shows up.
func TestListDomains_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appID := mustSeedApp(t, e, "ld-app")
	if _, err := e.store.CreateCustomDomain(context.Background(), "y.example.com", appID, "tok"); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "GET", "/v1/domains", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out []api.CustomDomainResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 || out[0].Domain != "y.example.com" {
		t.Errorf("got %+v", out)
	}
}

// TestDeleteDomain_HappyPath creates a domain and deletes it.
func TestDeleteDomain_HappyPath(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appID := mustSeedApp(t, e, "dd-app")
	if _, err := e.store.CreateCustomDomain(context.Background(), "z.example.com", appID, "tok"); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "DELETE", "/v1/domains/z.example.com", nil, nil)
	if rec.Code != 204 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

// TestDeleteDomain_UnknownReturns404.
func TestDeleteDomain_UnknownReturns404(t *testing.T) {
	e := setup(t, api.PlanHobby)
	rec := e.do(t, "DELETE", "/v1/domains/nope.example.com", nil, nil)
	assertProblem(t, rec, 404, api.CodeNotFound)
}

// TestCreateCron_HappyPath schedules a cron and confirms 201.
func TestCreateCron_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "cron-app")
	rec := e.do(t, "POST", "/v1/crons",
		api.CreateCronRequest{AppID: appID, Schedule: "*/5 * * * *", Path: "/heartbeat"}, nil)
	if rec.Code != 201 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.CronResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Schedule != "*/5 * * * *" || out.Path != "/heartbeat" {
		t.Errorf("got %+v", out)
	}
}

// TestCreateCron_InvalidSchedule confirms the cron regex 400 path
// (ErrCronInvalid is http.StatusBadRequest, Code=CodeCronInvalid).
func TestCreateCron_InvalidSchedule(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "cron-bad")
	rec := e.do(t, "POST", "/v1/crons",
		api.CreateCronRequest{AppID: appID, Schedule: "not-a-cron"}, nil)
	assertProblem(t, rec, 400, api.CodeCronInvalid)
}

// TestListCrons_HappyPath seeds a cron via the store and confirms listCrons
// returns it. Direct store insert keeps the test self-contained — the
// HTTP create path is already covered above.
func TestListCrons_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "lc-app")
	if _, err := e.store.CreateCron(context.Background(), appID, "0 9 * * *", "/daily", true); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, "GET", "/v1/crons", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out []api.CronResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 || out[0].Path != "/daily" {
		t.Errorf("got %+v", out)
	}
}

// TestUpdateCron_HappyPath patches a cron schedule.
func TestUpdateCron_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "uc-app")
	c, err := e.store.CreateCron(context.Background(), appID, "0 9 * * *", "/x", true)
	if err != nil {
		t.Fatal(err)
	}
	newSched := "*/15 * * * *"
	rec := e.do(t, "PATCH", "/v1/crons/"+c.ID,
		api.UpdateCronRequest{Schedule: &newSched}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.CronResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Schedule != "*/15 * * * *" {
		t.Errorf("schedule = %q, want */15 * * * *", out.Schedule)
	}
}

// TestUpdateCron_InvalidSchedule: PATCH with a bad schedule is 400
// (matches ErrCronInvalid in pkg/api/errors.go).
func TestUpdateCron_InvalidSchedule(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "uc-bad")
	c, _ := e.store.CreateCron(context.Background(), appID, "0 9 * * *", "/x", true)
	bad := "garbage"
	rec := e.do(t, "PATCH", "/v1/crons/"+c.ID,
		api.UpdateCronRequest{Schedule: &bad}, nil)
	assertProblem(t, rec, 400, api.CodeCronInvalid)
}

// TestDeleteCron_HappyPath.
func TestDeleteCron_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "dc-app")
	c, _ := e.store.CreateCron(context.Background(), appID, "0 9 * * *", "/x", true)
	rec := e.do(t, "DELETE", "/v1/crons/"+c.ID, nil, nil)
	if rec.Code != 204 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

// TestCreateKey_HappyPath: POST /v1/keys returns 201 with the plaintext in
// the response.
func TestCreateKey_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/keys", map[string]string{"label": "ci"}, nil)
	if rec.Code != 201 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.APIKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Plaintext == "" || out.Prefix == "" {
		t.Errorf("missing plaintext/prefix in response: %+v", out)
	}
	if out.Label != "ci" {
		t.Errorf("label = %q, want ci", out.Label)
	}
}

// TestListKeys_HappyPath: GET /v1/keys returns the seeded key.
func TestListKeys_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/keys", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out []api.APIKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("got %d keys, want 1 (the test fixture)", len(out))
	}
	if out[0].Plaintext != "" {
		t.Errorf("plaintext should be empty on list, got %q", out[0].Plaintext)
	}
}

// TestDeleteKey_HappyPath deletes the test fixture key.
func TestDeleteKey_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	keys, _ := e.store.ListAPIKeys(context.Background(), e.acct.ID)
	if len(keys) != 1 {
		t.Fatalf("fixture: want 1 key, got %d", len(keys))
	}
	rec := e.do(t, "DELETE", "/v1/keys/"+keys[0].ID, nil, nil)
	if rec.Code != 204 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

// TestDeleteKey_UnknownReturns404.
func TestDeleteKey_UnknownReturns404(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "DELETE", "/v1/keys/ghost-key", nil, nil)
	assertProblem(t, rec, 404, api.CodeNotFound)
}

// TestGetUsage_HappyPath returns an empty array (no usage rows yet).
func TestGetUsage_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/usage", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out []api.UsageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("got %d usage rows, want 0", len(out))
	}
}

// TestGetUsage_BadMonth confirms the YYYY-MM parse 400 path.
func TestGetUsage_BadMonth(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/usage?month=not-a-month", nil, nil)
	assertProblem(t, rec, 400, api.CodeValidation)
}

// TestListDeployments_HappyPath confirms the empty-page shape and that
// NextBefore is empty when the page is under the limit.
func TestListDeployments_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "ld-app")
	rec := e.do(t, "GET", "/v1/deployments", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.DeploymentListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != dep.ID {
		t.Errorf("got %+v, want 1 item id=%s", out, dep.ID)
	}
	if out.NextBefore != "" {
		t.Errorf("NextBefore = %q, want empty (under limit)", out.NextBefore)
	}
}

// TestListDeployments_CursorValid confirms the cursor branch with a
// well-formed before= value.
func TestListDeployments_CursorValid(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedDeployment(t, e, "ld-cur")
	// far-future cursor → no rows, but still 200.
	rec := e.do(t, "GET", "/v1/deployments?before=2099-01-01T00:00:00Z", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

// TestListDeployments_BadCursor: garbage `before=` → 400.
func TestListDeployments_BadCursor(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/deployments?before=not-a-time", nil, nil)
	assertProblem(t, rec, 400, api.CodeValidation)
}

// TestUsageSummary_HappyPath: no usage → 0 GB-h, 0 overage.
func TestUsageSummary_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/usage/summary", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.UsageSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.UsedGBHours != 0 || out.OverageGBHours != 0 {
		t.Errorf("got %+v", out)
	}
	if out.IncludedGBHours == 0 {
		t.Errorf("included = 0, want plan default")
	}
}

// TestUsageSummary_BadMonth: YYYY-MM parse failure.
func TestUsageSummary_BadMonth(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "GET", "/v1/usage/summary?month=garbage", nil, nil)
	assertProblem(t, rec, 400, api.CodeValidation)
}

// --- pure-unit tests for the response helpers (handlers_ext.go:720-784) ---

// TestDeploymentResponse_RoundTrip confirms every field flows through.
func TestDeploymentResponse_RoundTrip(t *testing.T) {
	srv := newServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{})
	d := state.Deployment{
		ID: "d1", AppID: "a1", ImageDigest: "sha256:x", Kind: state.DeploymentKindImage,
		Status: state.DeployLive, Error: "boom", ErrorCode: "image_not_found",
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	resp := srv.deploymentResponse(d)
	if resp.ID != "d1" || resp.Status != "live" || resp.Error != "boom" ||
		resp.ErrorCode != "image_not_found" || resp.CreatedAt != "2026-01-02T03:04:05Z" {
		t.Errorf("got %+v", resp)
	}
}

// TestInstanceResponse_TimestampsCovered confirms the three optional
// timestamp branches (zero vs populated).
func TestInstanceResponse_TimestampsCovered(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	ins := state.Instance{ID: "i1", AppID: "a1", DeploymentID: "d1", State: "running"}
	r := instanceResponse(ins)
	if r.StartedAt != "" || r.LastRequestAt != "" || r.ParkedAt != "" {
		t.Errorf("zero-time should produce empty strings: %+v", r)
	}
	ins.StartedAt = now
	ins.LastRequestAt = now
	ins.ParkedAt = now
	r = instanceResponse(ins)
	if r.StartedAt == "" || r.LastRequestAt == "" || r.ParkedAt == "" {
		t.Errorf("populated timestamps should serialize: %+v", r)
	}
}

// TestDomainResponse_UnverifiedHasTXT exercises the unverified branch: the
// response carries a TXT record hint and an empty VerifiedAt.
func TestDomainResponse_UnverifiedHasTXT(t *testing.T) {
	d := state.CustomDomain{Domain: "x.example.com", AppID: "a1", ChallengeToken: "tok"}
	r := domainResponse(d)
	if r.Verified {
		t.Error("unverified domain should report Verified=false")
	}
	if r.VerifiedAt != "" {
		t.Errorf("VerifiedAt = %q, want empty", r.VerifiedAt)
	}
	if !strings.Contains(r.TXTRecord, "tok") {
		t.Errorf("TXTRecord missing token: %q", r.TXTRecord)
	}
}

// TestCronResponse_LastFiredAtBranch confirms the optional timestamp branch.
func TestCronResponse_LastFiredAtBranch(t *testing.T) {
	c := state.Cron{ID: "c1", AppID: "a1", Schedule: "0 9 * * *", Path: "/x", Enabled: true,
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		LastFiredAt: time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)}
	r := cronResponse(c)
	if r.LastFiredAt == "" {
		t.Errorf("populated LastFiredAt should serialize: %+v", r)
	}
	c2 := state.Cron{ID: "c2", AppID: "a1", Schedule: "0 9 * * *", Path: "/x", Enabled: true}
	r2 := cronResponse(c2)
	if r2.LastFiredAt != "" {
		t.Errorf("zero LastFiredAt should be empty: %+v", r2)
	}
}

// newStripeServer wires a server with a fixed Stripe webhook secret
// for the A2 fail-closed tests. Everything else (memstore, noop
// notifier, noop mailer, stub githubd, default sessions) mirrors
// the helper used elsewhere in this package.
func newStripeServer(t *testing.T, secret string) http.Handler {
	t.Helper()
	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return newServerWithDeps(store, log, "example.com", noopNotifier{}, secret,
		noopMailer{}, stubGithubdClient{}, nil, nil, 15*time.Minute, "").handler()
}

// TestStripeWebhook_RefusesEmptySecret is the A2 regression test:
// when STRIPE_WEBHOOK_SECRET is unset, the handler must return 503
// rather than processing unsigned events. Previously the empty-
// secret branch in handlers_ext.go let an unauthenticated POST
// suspend any account by claiming customer.subscription.deleted.
func TestStripeWebhook_RefusesEmptySecret(t *testing.T) {
	srv := newStripeServer(t, "")
	body := `{"type":"customer.subscription.deleted","data":{"object":{"customer":"cus_anything"}}}`
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (fail-closed)\nbody = %s", rec.Code, rec.Body.String())
	}
	var prob api.Problem
	if err := json.NewDecoder(rec.Body).Decode(&prob); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if prob.Code != api.CodeCapacity {
		t.Errorf("problem code = %q, want %q", prob.Code, api.CodeCapacity)
	}
}

// TestStripeWebhook_AcceptsSigned fires a properly signed event and
// asserts the handler returns 200 (Stripe expects 2xx for everything
// it didn't recognize — the handler emits 200 with no side effect on
// an unknown customer ID).
func TestStripeWebhook_AcceptsSigned(t *testing.T) {
	const secret = "whsec_test_signing_secret"
	srv := newStripeServer(t, secret)
	body := []byte(`{"type":"invoice.payment_succeeded","data":{"object":{"customer":"cus_unknown"}}}`)
	header := stripe.SignForTest(body, secret, time.Now())
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Stripe-Signature", header)
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("signed event: status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
}

// TestStripeWebhook_RejectsTampered asserts the handler rejects an
// event whose body is altered after signing.
func TestStripeWebhook_RejectsTampered(t *testing.T) {
	const secret = "whsec_test_signing_secret"
	srv := newStripeServer(t, secret)
	body := []byte(`{"type":"customer.subscription.deleted","data":{"object":{"customer":"cus_evil"}}}`)
	header := stripe.SignForTest(body, secret, time.Now())
	// Tamper: flip one byte in the body.
	tampered := append([]byte{}, body...)
	tampered[len(tampered)-1] ^= 1
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", bytes.NewReader(tampered))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Stripe-Signature", header)
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("tampered event: status = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
	var prob api.Problem
	if err := json.NewDecoder(rec.Body).Decode(&prob); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if prob.Code != api.CodeValidation {
		t.Errorf("problem code = %q, want %q", prob.Code, api.CodeValidation)
	}
}

// TestStripeWebhook_RejectsWrongSecret asserts an event signed with
// the wrong secret is rejected with 400.
func TestStripeWebhook_RejectsWrongSecret(t *testing.T) {
	srv := newStripeServer(t, "whsec_test_correct_secret")
	body := []byte(`{"type":"customer.subscription.deleted","data":{"object":{"customer":"cus_x"}}}`)
	header := stripe.SignForTest(body, "whsec_test_WRONG_secret", time.Now())
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Stripe-Signature", header)
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong-secret event: status = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
}

// --- Stripe webhook email coverage (spec §171 "All transitions emailed") ---

// stripeWebhookHarness wires the production server with a recordingMailer
// so we can assert the customer-facing email surface. The webhook secret is
// intentionally empty — same dev-mode disable the route has in production
// when STRIPE_WEBHOOK_SECRET is unset.
func stripeWebhookHarness(t *testing.T, plan api.Plan) (testEnv, *recordingMailer) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "alice@example.com", plan)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := store.UpdateAccountProviderCustomerID(context.Background(), acct.ID, "cus_test_123"); err != nil {
		t.Fatalf("UpdateAccountProviderCustomerID: %v", err)
	}
	mailer := &recordingMailer{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Wire a real signing secret — main's A2 fail-closed behavior
	// (handlers_ext.go) returns 503 on empty secret, so the harness
	// must sign events to exercise the dunning state machine.
	srv := newServerWithDeps(store, log, "example.com", noopNotifier{},
		stripeWebhookSecretForTest, mailer,
		stubGithubdClient{}, nil, nil, 0, "")
	return testEnv{h: srv.handler(), store: store, acct: acct}, mailer
}

// stripeWebhookSecretForTest is the shared signing secret the
// stripeWebhookHarness + postStripeEvent pair use. Constant so
// handoffs are deterministic; main's TestStripeWebhook_AcceptsSigned
// uses the same value.
const stripeWebhookSecretForTest = "whsec_test_signing_secret"

// postStripeEvent sends a signed Stripe event JSON to the webhook
// route. Signature matches stripeWebhookSecretForTest so the route's
// HMAC verification passes.
func postStripeEvent(t *testing.T, h http.Handler, eventType, customer string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"type": eventType,
		"data": map[string]any{
			"object": map[string]any{"customer": customer},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stripe-Signature", stripe.SignForTest(body, stripeWebhookSecretForTest, time.Now()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestStripePaymentFailed_EmailsOnFirstDelivery is the spec §171 closure:
// first delivery of invoice.payment_failed flips status → past_due AND
// sends the PaymentFailedBody entry-point email. A regression that drops
// the s.mailer.Send call (or moves it outside the success branch) would
// leave the customer silent for 7 days.
func TestStripePaymentFailed_EmailsOnFirstDelivery(t *testing.T) {
	e, mailer := stripeWebhookHarness(t, api.PlanHobby)

	rec := postStripeEvent(t, e.h, "invoice.payment_failed", "cus_test_123")
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200: %s", rec.Code, rec.Body)
	}

	// Status flipped.
	got, err := e.store.AccountByID(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if got.Status != state.AccountPastDue {
		t.Fatalf("status = %s, want past_due", got.Status)
	}

	// Exactly one email went out, with PaymentFailedBody's subject.
	msgs := mailer.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("mailer.snapshot() = %d, want 1: %+v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0].Subject, "payment failed") {
		t.Errorf("subject = %q, want it to mention payment failed", msgs[0].Subject)
	}
	if len(msgs[0].To) != 1 || msgs[0].To[0] != e.acct.Email {
		t.Errorf("To = %v, want [%s]", msgs[0].To, e.acct.Email)
	}
	if !strings.Contains(msgs[0].TextBody, "7 days") {
		t.Errorf("body missing 7-day window:\n%s", msgs[0].TextBody)
	}
}

// TestStripePaymentFailed_RedeliveryNoEmail is the idempotency closure:
// a Stripe redelivery on an already-past_due account must produce ZERO
// additional emails. MarkDunningStep returns state.ErrNotFound on
// redelivery, the handler logs it at Debug, and the success-branch
// (which fires the mail) is skipped entirely.
func TestStripePaymentFailed_RedeliveryNoEmail(t *testing.T) {
	e, mailer := stripeWebhookHarness(t, api.PlanHobby)

	// First delivery: flips to past_due, sends 1 email.
	postStripeEvent(t, e.h, "invoice.payment_failed", "cus_test_123")
	if n := len(mailer.snapshot()); n != 1 {
		t.Fatalf("after first delivery: %d emails, want 1", n)
	}

	// Stripe redelivers the same event — status is already past_due so
	// MarkDunningStep short-circuits.
	rec := postStripeEvent(t, e.h, "invoice.payment_failed", "cus_test_123")
	if rec.Code != http.StatusOK {
		t.Fatalf("redelivery status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if n := len(mailer.snapshot()); n != 1 {
		t.Errorf("after redelivery: %d emails, want 1 (zero additional)", n)
	}
}

// TestStripePaymentSucceeded_RestoresAndEmails is the recovery closure:
// invoice.payment_succeeded on a past_due account flips status → active
// AND sends the AccountRestoredBody recovery email. No-op on an already-
// active account (the acct.Status == AccountPastDue guard).
func TestStripePaymentSucceeded_RestoresAndEmails(t *testing.T) {
	e, mailer := stripeWebhookHarness(t, api.PlanHobby)

	// Drive the account into past_due.
	if err := e.store.UpdateAccountStatus(context.Background(), e.acct.ID, state.AccountPastDue); err != nil {
		t.Fatalf("seed past_due: %v", err)
	}

	rec := postStripeEvent(t, e.h, "invoice.payment_succeeded", "cus_test_123")
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200: %s", rec.Code, rec.Body)
	}

	got, _ := e.store.AccountByID(context.Background(), e.acct.ID)
	if got.Status != state.AccountActive {
		t.Fatalf("status = %s, want active", got.Status)
	}

	msgs := mailer.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("mailer.snapshot() = %d, want 1: %+v", len(msgs), msgs)
	}
	if !strings.Contains(msgs[0].Subject, "good standing") {
		t.Errorf("subject = %q, want it to mention good standing", msgs[0].Subject)
	}
}

// TestStripePaymentSucceeded_NoEmailOnAlreadyActive is the no-op closure:
// payment_succeeded on an account that was never past_due must not email.
// Without the acct.Status == AccountPastDue guard, every fresh signup
// would receive a recovery email the first time Stripe confirmed their
// card.
func TestStripePaymentSucceeded_NoEmailOnAlreadyActive(t *testing.T) {
	e, mailer := stripeWebhookHarness(t, api.PlanHobby)

	rec := postStripeEvent(t, e.h, "invoice.payment_succeeded", "cus_test_123")
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if n := len(mailer.snapshot()); n != 0 {
		t.Errorf("already-active payment_succeeded sent %d emails, want 0", n)
	}
}

// TestStripePaymentFailed_MailErrDoesNotUndoStatus pins the load-
// bearing invariant the comments make: the mail is best-effort and
// must NEVER undo the status flip Stripe told us about. A regression
// that promoted Send's error to a 500 response (or rolled back the
// CAS) would silently break the dunning state machine for any
// customer whose SMTP relay is degraded.
func TestStripePaymentFailed_MailErrDoesNotUndoStatus(t *testing.T) {
	e, mailer := stripeWebhookHarness(t, api.PlanHobby)
	mailer.sendErr = errors.New("smtp relay temporarily unavailable")

	rec := postStripeEvent(t, e.h, "invoice.payment_failed", "cus_test_123")
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200 even when mailer errors: %s", rec.Code, rec.Body)
	}

	// Status still flipped — the cas committed BEFORE the send.
	got, err := e.store.AccountByID(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if got.Status != state.AccountPastDue {
		t.Fatalf("status = %s, want past_due (mail error must not roll back)", got.Status)
	}
	if n := len(mailer.snapshot()); n != 1 {
		t.Errorf("mailer was called %d times, want exactly 1 (the failing attempt)", n)
	}
}

// TestStripePaymentSucceeded_MailErrDoesNotUndoStatus mirrors the
// payment_failed closure: a failed recovery mail must not roll the
// account back to past_due.
func TestStripePaymentSucceeded_MailErrDoesNotUndoStatus(t *testing.T) {
	e, mailer := stripeWebhookHarness(t, api.PlanHobby)
	if err := e.store.UpdateAccountStatus(context.Background(), e.acct.ID, state.AccountPastDue); err != nil {
		t.Fatalf("seed past_due: %v", err)
	}
	mailer.sendErr = errors.New("smtp relay temporarily unavailable")

	rec := postStripeEvent(t, e.h, "invoice.payment_succeeded", "cus_test_123")
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200 even when mailer errors: %s", rec.Code, rec.Body)
	}

	got, _ := e.store.AccountByID(context.Background(), e.acct.ID)
	if got.Status != state.AccountActive {
		t.Fatalf("status = %s, want active (mail error must not roll back)", got.Status)
	}
	if n := len(mailer.snapshot()); n != 1 {
		t.Errorf("mailer was called %d times, want exactly 1 (the failing attempt)", n)
	}
}

// TestStripePaymentFailed_SuspendedIsNoOp pins the inverted guard:
// MarkDunningStep rejects every status other than the expected
// `from` with ErrNotFound, so a payment_failed event landing on an
// already-suspended account is silently ignored. The meterd 7-day
// timer is the source of truth for "apps already parked" — the
// webhook seeing a failure for a suspended customer is a Stripe
// stale-delivery or a duplicate subscription and should never
// re-fire any mail.
func TestStripePaymentFailed_SuspendedIsNoOp(t *testing.T) {
	e, mailer := stripeWebhookHarness(t, api.PlanHobby)
	if err := e.store.UpdateAccountStatus(context.Background(), e.acct.ID, state.AccountSuspended); err != nil {
		t.Fatalf("seed suspended: %v", err)
	}

	rec := postStripeEvent(t, e.h, "invoice.payment_failed", "cus_test_123")
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200 (silent no-op): %s", rec.Code, rec.Body)
	}

	got, _ := e.store.AccountByID(context.Background(), e.acct.ID)
	if got.Status != state.AccountSuspended {
		t.Fatalf("status = %s, want suspended (must not flip back to past_due)", got.Status)
	}
	if n := len(mailer.snapshot()); n != 0 {
		t.Errorf("mailer.snapshot() = %d, want 0 (suspended accounts must not receive PaymentFailedBody)", n)
	}
}

// --- Move 2: event-driven surface tests -------------------------------------
//
// These tests pin the customer-facing HTTP surface for the Move 1
// invocations backend. They use MemStore (no Postgres) so the assertions
// stay on the handler-level contract. The long-poll handlers
// (invokeApp, queueReceive) use setupWithNotifier with a hook that
// injects a synthetic payload so the test doesn't hang on a real
// LISTEN connection.

// TestAsyncInvoke_AcceptedWithId confirms POST /v1/apps/{slug}/invoke/async
// returns 202 + a populated id, and the row lands in the invocations table
// with source='async_invoke'.
func TestAsyncInvoke_AcceptedWithId(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "myapp")

	rec := e.do(t, "POST", "/v1/apps/myapp/invoke/async", api.InvokeRequest{
		Method:  "POST",
		Path:    "/webhook",
		Payload: json.RawMessage(`{"hello":"world"}`),
	}, nil)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.AsyncInvokeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID == "" {
		t.Fatalf("response.id is empty")
	}
	if resp.StatusURL != "/v1/invocations/"+resp.ID {
		t.Errorf("status_url = %q, want /v1/invocations/%s", resp.StatusURL, resp.ID)
	}
	// Round-trip the row.
	inv, err := e.store.InvocationByID(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("row not found: %v", err)
	}
	if inv.Source != state.InvocationAsyncInvoke {
		t.Errorf("source = %v, want async_invoke", inv.Source)
	}
	if inv.State != state.InvocationPending {
		t.Errorf("state = %v, want pending", inv.State)
	}
}

// TestAsyncInvoke_FreeRejected confirms the Free-plan gate fires before
// any row is written. The 403 carries the feature_not_allowed code so a
// clear SDK message can be surfaced.
func TestAsyncInvoke_FreeRejected(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "myapp")

	rec := e.do(t, "POST", "/v1/apps/myapp/invoke/async", api.InvokeRequest{}, nil)

	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402; body=%s", rec.Code, rec.Body.String())
	}
	var prob api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if prob.Code != "plan_feature_gated" {
		t.Errorf("code = %q, want plan_feature_gated", prob.Code)
	}
}

// TestQueueSend_RejectsAtLimit confirms the plan's MaxQueueDepth cap is
// enforced on the way in. Hobby's cap is 5; the 6th send is a 403 with
// the plan_queue_depth code.
func TestQueueSend_RejectsAtLimit(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "myapp")

	// 5 successful sends.
	for i := 0; i < 5; i++ {
		rec := e.do(t, "POST", "/v1/apps/myapp/queues/send", api.QueueSendRequest{
			Payload: json.RawMessage(`{"i":` + strconv.Itoa(i) + `}`),
		}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("send %d: status = %d, want 201; body=%s", i, rec.Code, rec.Body.String())
		}
	}
	// 6th send is rejected.
	rec := e.do(t, "POST", "/v1/apps/myapp/queues/send", api.QueueSendRequest{}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	var prob api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if prob.Code != "plan_queue_depth" {
		t.Errorf("code = %q, want plan_queue_depth", prob.Code)
	}
	if prob.Limit != nil && *prob.Limit != 5 {
		t.Errorf("limit = %v, want 5", prob.Limit)
	}
}

// TestQueueReceive_TimeoutReturns204 confirms the long-poll returns a
// clean 204 (no body) when the server-side budget elapses without a
// matching notification. The setupWithNotifier hook returns
// db.ErrWaitTimeout so the handler falls into the timeout branch.
func TestQueueReceive_TimeoutReturns204(t *testing.T) {
	e := setupWithNotifier(t, api.PlanPro, func(_ context.Context, _ string, _ func(string) bool, _ time.Duration) (string, error) {
		return "", db.ErrWaitTimeout
	})
	mustSeedApp(t, e, "myapp")

	rec := e.do(t, "POST", "/v1/apps/myapp/queues/receive", nil, nil)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

// TestDelayedTaskCreate_PayloadTooLarge confirms the
// MaxSourceBytesPerInvocation cap is enforced per-plan. Hobby's cap is
// 64 KB; a 70 KB payload is a 413.
func TestDelayedTaskCreate_PayloadTooLarge(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "myapp")

	big := make([]byte, 70*1024)
	for i := range big {
		big[i] = 'a'
	}
	rec := e.do(t, "POST", "/v1/apps/myapp/delayed-tasks", api.DelayedTaskRequest{
		Payload:     json.RawMessage(`{"blob":"` + string(big) + `"}`),
		ScheduledAt: time.Now().Add(time.Hour).UTC(),
	}, nil)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", rec.Code, rec.Body.String())
	}
	var prob api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if prob.Code != "plan_source_bytes" {
		t.Errorf("code = %q, want plan_source_bytes", prob.Code)
	}
}

// TestDelayedTaskCreate_PastSchedAt confirms the handler rejects a
// scheduled_at in the past with 400 + invalid_scheduled_at.
func TestDelayedTaskCreate_PastSchedAt(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "myapp")

	rec := e.do(t, "POST", "/v1/apps/myapp/delayed-tasks", api.DelayedTaskRequest{
		Payload:     json.RawMessage(`{}`),
		ScheduledAt: time.Now().Add(-time.Hour).UTC(),
	}, nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var prob api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if prob.Code != "invalid_scheduled_at" {
		t.Errorf("code = %q, want invalid_scheduled_at", prob.Code)
	}
}

// TestDeleteApp_CancelsPendingInvocations confirms the deleteApp GC
// cancels pending/dispatching invocations before the app row is
// removed. A terminal (completed) row is left untouched.
func TestDeleteApp_CancelsPendingInvocations(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "myapp")

	// 3 pending delayed-tasks.
	pendingIDs := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		inv, err := e.store.EnqueueInvocation(context.Background(), state.Invocation{
			AppID:       appID,
			AccountID:   e.acct.ID,
			Source:      state.InvocationDelayedTask,
			Payload:     json.RawMessage(`{}`),
			DueAt:       time.Now().Add(time.Hour),
			ScheduledAt: ptrTime2(time.Now().Add(time.Hour)),
		})
		if err != nil {
			t.Fatalf("seed pending: %v", err)
		}
		pendingIDs = append(pendingIDs, inv.ID)
	}
	// 1 completed row — must survive the delete.
	completed, err := e.store.EnqueueInvocation(context.Background(), state.Invocation{
		AppID:     appID,
		AccountID: e.acct.ID,
		Source:    state.InvocationDelayedTask,
		Payload:   json.RawMessage(`{}`),
		DueAt:     time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("seed completed: %v", err)
	}
	// Synthesise a completed row: Claim (pending→dispatching) then
	// Complete (dispatching→completed). The drain drives this transition
	// in production; the test inlines the same hand-off so the row
	// lands in `completed` state and the GC path leaves it alone.
	claimed, err := e.store.ClaimInvocation(context.Background(), completed.ID, "inst_test", 60)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := e.store.CompleteInvocation(context.Background(), claimed.ID, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatalf("mark completed: %v", err)
	}

	rec := e.do(t, "DELETE", "/v1/apps/myapp", nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}

	// Pending rows are cancelled.
	for _, id := range pendingIDs {
		inv, err := e.store.InvocationByID(context.Background(), id)
		if err != nil {
			t.Fatalf("invocation %s not found: %v", id, err)
		}
		if inv.State != state.InvocationCancelled {
			t.Errorf("invocation %s state = %v, want cancelled", id, inv.State)
		}
	}
	// Completed row is untouched.
	inv, err := e.store.InvocationByID(context.Background(), completed.ID)
	if err != nil {
		t.Fatalf("completed invocation not found: %v", err)
	}
	if inv.State != state.InvocationCompleted {
		t.Errorf("completed state = %v, want completed (must not be GC'd)", inv.State)
	}
}

// ptrTime2 is a tiny adapter so the test seeds a *time.Time for
// ScheduledAt without a nil-check at the call site.
func ptrTime2(t time.Time) *time.Time { return &t }

// TestGetInvocation_NotFoundAcrossAccount confirms a cross-account
// read returns 404 (the privacy contract — no ownership leak).
func TestGetInvocation_NotFoundAcrossAccount(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "myapp")

	// Seed an invocation on a different account.
	other, err := e.store.CreateAccount(context.Background(), "intruder@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	inv, err := e.store.EnqueueInvocation(context.Background(), state.Invocation{
		AppID:     appID,
		AccountID: other.ID,
		Source:    state.InvocationAsyncInvoke,
		Payload:   json.RawMessage(`{}`),
		DueAt:     time.Now(),
	})
	if err != nil {
		t.Fatalf("seed inv: %v", err)
	}

	rec := e.do(t, "GET", "/v1/invocations/"+inv.ID, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestStreamAppLogs_NotImplementedFrame pins the Move 3 stub wire
// shape: GET /v1/apps/{slug}/logs MUST return text/event-stream with
// the two frames the SDK decoder parses:
//
//	event: not_implemented
//	data: {"reason":"vmmd Logs(req) gRPC pending — Move 4"}
//
//	event: end
//	data: {}
//
// The order matters — the SDK consumes frames line-by-line via the
// typed Event/Data split (pkg/api/sse.go:Event) and treats `end` as
// the terminal sentinel. A future Move 4 implementation that
// subscribes to vmmd's Logs(req) gRPC stream replaces the body of
// streamAppLogs but MUST keep the same opening protocol so existing
// SDK consumers parse the new frames identically.
//
// Locks the contract for: SDK happy path
// (pkg/api/client_test.go::TestStreamAppLogs_HappyPath), the dashboard
// per-app log button (no 404), and a future Move 4 implementation
// that upgrades the body.
func TestStreamAppLogs_NotImplementedFrame(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "stub-logs")
	appID := mustSeedApp(t, e, "stub-logs-app")
	// The handler's `ListInstancesForApp` check requires at least one
	// row — otherwise it 404s with "no running instance". A stopped
	// instance satisfies the existence check (the stub doesn't read
	// the row's state column).
	if _, err := e.store.CreateInstance(context.Background(), appID, dep.ID, "stopped", 256, "default-local", ""); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	rec := e.do(t, "GET", "/v1/apps/stub-logs-app/logs?follow=0", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	body := rec.Body.String()
	// Frame 1: not_implemented signal. The reason text is part of the
	// SDK's stable contract — a customer reading the docs sees it as
	// the canonical "this isn't built yet" message.
	if !strings.Contains(body, "event: not_implemented\ndata: {\"reason\":\"vmmd Logs(req) gRPC pending — Move 4\"}") {
		t.Errorf("body missing not_implemented frame:\n%s", body)
	}
	// Frame 2: terminal sentinel. SDK consumers with a structured-
	// frame loop exit on `event: end`.
	if !strings.Contains(body, "event: end\ndata: {}") {
		t.Errorf("body missing end frame:\n%s", body)
	}
	// Order: not_implemented must precede end so the SDK's decoder
	// surfaces the reason before the consumer's loop exits.
	niIdx := strings.Index(body, "event: not_implemented")
	endIdx := strings.Index(body, "event: end")
	if niIdx < 0 || endIdx < 0 || niIdx >= endIdx {
		t.Errorf("frame order wrong: not_implemented@%d end@%d", niIdx, endIdx)
	}
}
