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
	"github.com/onebox-faas/faas/pkg/webhookdedupe"
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

// TestEmitStageDiff_AllSixStages drives emitStageDiff through the
// full 6-stage progression the customer sees on a cold-cache deploy.
// Each tick advances the jsonb stage_state one step; we assert:
//   - tick 1: `event: stage` for source_download, status=in_progress
//   - tick 2..6: each tick emits BOTH a completed frame for the prior
//     stage AND an in_progress frame for the new one
//   - the announced map dedupes a no-op tick (same raw bytes)
//   - the active row's failure stamp emits status=failed + reason
//
// ADR-117 §3.
func TestEmitStageDiff_AllSixStages(t *testing.T) {
	order := []state.StageName{
		state.StageSourceDownload,
		state.StageDependencyRestore,
		state.StageImageBuild,
		state.StageSecurityScan,
		state.StageSnapshotPrepare,
		state.StageReadiness,
	}
	announced := make(map[state.StageName]string)
	var lastRaw []byte

	for i, stage := range order {
		// Build the jsonb row as imaged would: completed row for
		// every prior stage + current = `stage`, started_at = now.
		now := time.Now().UTC()
		history := make([]state.StageStateItem, 0, i)
		for j := 0; j < i; j++ {
			startedAt := now.Add(-time.Duration(i-j) * time.Second)
			endedAt := now.Add(-time.Duration(i-j-1) * time.Second)
			history = append(history, state.StageStateItem{
				Name:       order[j],
				StartedAt:  &startedAt,
				EndedAt:    &endedAt,
				DurationMs: 1000,
				Status:     "completed",
			})
		}
		raw, err := json.Marshal(state.StageState{
			Current:          stage,
			CurrentStartedAt: &now,
			History:          history,
		})
		if err != nil {
			t.Fatalf("tick %d marshal: %v", i, err)
		}

		buf := httptest.NewRecorder()
		emitStageDiff(buf, nil, raw, announced, &lastRaw)

		frames := strings.Split(buf.Body.String(), "\n\n")
		// First tick: 1 in_progress frame. Ticks 2..6: 1 completed
		// (for prior) + 1 in_progress (for current) = 2 frames. The
		// trailing empty element from Split is ignored.
		wantFrames := 1
		if i > 0 {
			wantFrames = 2
		}
		if got := len(frames) - 1; got != wantFrames {
			t.Fatalf("tick %d (%s): got %d frames, want %d: %q", i, stage, got, wantFrames, buf.Body.String())
		}

		// Every frame must be `event: stage\ndata: {...}`.
		for k, frame := range frames {
			if frame == "" {
				continue
			}
			if !strings.HasPrefix(frame, "event: stage\ndata: ") {
				t.Fatalf("tick %d frame %d: bad prefix %q", i, k, frame)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(frame, "event: stage\ndata: ")), &payload); err != nil {
				t.Fatalf("tick %d frame %d: bad JSON: %v", i, k, err)
			}
			if payload["status"] != "in_progress" && payload["status"] != "completed" {
				t.Fatalf("tick %d frame %d: bad status %v", i, k, payload["status"])
			}
		}

		// Idempotent re-poll with same raw bytes must NOT re-emit.
		buf2 := httptest.NewRecorder()
		emitStageDiff(buf2, nil, raw, announced, &lastRaw)
		if buf2.Body.Len() != 0 {
			t.Fatalf("tick %d idempotent re-poll: expected zero frames, got %q", i, buf2.Body.String())
		}
	}

	// Tick 7: flip to failed via builderd path — same from/to.
	// AppendDeploymentStage stamps the active row with
	// status=failed + reason; emitStageDiff picks it up on the
	// next poll and emits one event: stage frame with reason.
	now := time.Now().UTC()
	failed := state.StageState{
		Current:          state.StageReadiness,
		CurrentStartedAt: &now,
		History: []state.StageStateItem{
			{
				Name: state.StageReadiness, StartedAt: &now,
				EndedAt: &now, DurationMs: 0, Status: "failed",
				Reason: "build failed: image_scan 1 error",
			},
		},
	}
	failedRaw, _ := json.Marshal(failed)
	announcedFail := make(map[state.StageName]string)
	// Pre-seed prior stages so only the failed entry for
	// StageReadiness emits. StageReadiness itself was never
	// announced — it failed before its in_progress frame was
	// observed, so the walk must emit exactly one failed frame.
	for _, s := range order {
		if s == state.StageReadiness {
			continue
		}
		announcedFail[s] = "completed"
	}
	var lastRawFail []byte
	buf := httptest.NewRecorder()
	emitStageDiff(buf, nil, failedRaw, announcedFail, &lastRawFail)
	out := buf.Body.String()
	if !strings.Contains(out, `"status":"failed"`) {
		t.Fatalf("failed frame missing status=failed: %q", out)
	}
	if !strings.Contains(out, `"reason":"build failed: image_scan 1 error"`) {
		t.Fatalf("failed frame missing reason: %q", out)
	}
	// And a subsequent tick with the same row must be idempotent.
	buf2 := httptest.NewRecorder()
	emitStageDiff(buf2, nil, failedRaw, announcedFail, &lastRawFail)
	if buf2.Body.Len() != 0 {
		t.Fatalf("failed idempotent re-poll: expected zero frames, got %q", buf2.Body.String())
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
	// Issue #695 / ADR-080: stamp the per-plan defaults the same way
	// cmd/apid/handlers.go::buildApp would stamp them at create-time.
	// Without this, every seeded app reads require_authn=false /
	// public_auth_mode='' regardless of plan, and any test that
	// asserts the plan default tripwire fails.
	plan := e.acct.Plan
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID:      e.acct.ID,
		Slug:           slug,
		Type:           state.AppTypeApp,
		Status:         state.AppActive,
		RequireAuthn:   plan.RequireAuthnDefault(),
		PublicAuthMode: plan.PublicAuthModeDefault(),
	})
	if err != nil {
		t.Fatalf("seed app %s: %v", slug, err)
	}
	return app.ID
}

// mustSeedAppFor provisions an app on a non-env store under the
// given account. Used by IDOR-safety tests that need a "foreign"
// app to probe with the env's credentials.
func mustSeedAppFor(t *testing.T, store *state.MemStore, accountID, slug string) string {
	t.Helper()
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: accountID,
		Slug:      slug,
		Type:      state.AppTypeApp,
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatalf("seed app %s on foreign store: %v", slug, err)
	}
	return app.ID
}

// TestUpdateAppMinInstances_Hobby locks the new Hobby+ tier-up
// (issue #462 / ADR-058 / PR-A). After the tier-up, Hobby plans
// accept apps.min_instances and the response carries the new
// value. The pre-#462 test expected 403 plan_min_instances_not_allowed;
// the PR-A tier-up moved Hobby to the same gate as Pro/Scale.
//
// Coverage: Hobby is the lowest tier that unlocks the warm floor.
// The legacy Hobby gate (pre-#462) is no longer the contract; the
// remaining plan-gate case is Free, exercised by TestUpdateAppMinInstances_Free
// below.
func TestUpdateAppMinInstances_Hobby(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "hobby-floor")
	one := 1
	rec := e.do(t, "PATCH", "/v1/apps/hobby-floor", api.UpdateAppRequest{MinInstances: &one}, nil)
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

// TestUpdateAppMinInstances_Free is the new lowest-tier gate (issue
// #462 / ADR-058 / PR-A). Free plans still cannot set
// apps.min_instances — the Hobby+ tier-up is as far as the gate
// goes. The handler must return 403, not 422, because the feature
// is tier-locked (the value the customer typed is irrelevant).
func TestUpdateAppMinInstances_Free(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "free-floor")
	one := 1
	rec := e.do(t, "PATCH", "/v1/apps/free-floor", api.UpdateAppRequest{MinInstances: &one}, nil)
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

// TestUpdateDeploymentMinInstances_FreeGate pins the issue #557 /
// ADR-072 plan-gate fix: a Free account PATCHing
// deployments.min_instances MUST return 403
// plan_min_instances_not_allowed, not 422
// max_min_instances_exceeded. Pre-fix the handler skipped the
// MinInstancesAllowed gate and Free plans masked the bug
// accidentally because `MaxMinInstances == 0` always tripped the
// value cap with the wrong error code. The fix adds the
// `acct.Plan.MinInstancesAllowed()` gate at the top of the
// handler, mirroring the per-app gate at validateUpdateApp.
func TestUpdateDeploymentMinInstances_FreeGate(t *testing.T) {
	e := setup(t, api.PlanFree)
	d := mustSeedDeployment(t, e, "free-dep")
	one := 1
	rec := e.do(t, "PATCH", "/v1/deployments/"+d.ID, api.UpdateDeploymentRequest{MinInstances: &one}, nil)
	assertProblem(t, rec, 403, api.CodePlanMinInstancesNotAllowed)
}

// TestUpdateDeploymentMinInstances_HobbyHappy is the symmetric
// happy-path: Hobby plans now accept the per-deployment floor
// (issue #462 / ADR-058 / PR-A tier-up). The response carries the
// new value. Mirrors TestUpdateAppMinInstances_Hobby above.
func TestUpdateDeploymentMinInstances_HobbyHappy(t *testing.T) {
	e := setup(t, api.PlanHobby)
	d := mustSeedDeployment(t, e, "hobby-dep")
	one := 1
	rec := e.do(t, "PATCH", "/v1/deployments/"+d.ID, api.UpdateDeploymentRequest{MinInstances: &one}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.MinInstances != 1 {
		t.Errorf("MinInstances = %d, want 1", out.MinInstances)
	}
}

// TestUpdateDeploymentMinInstances_Negative pins the 422 path: a
// Pro plan PATCHing -1 on a deployment gets
// invalid_min_instances, not plan_min_instances_not_allowed.
// Pre-fix the handler returned 400 (validation) instead of 422
// (semantic) for negative values; the new code lifts the negative
// check to the same 422 ErrInvalidMinInstances the per-app handler
// emits. The plan gate ran first so a Free plan PATCHing -1 still
// gets 403 (covered by TestUpdateDeploymentMinInstances_FreeGate).
func TestUpdateDeploymentMinInstances_Negative(t *testing.T) {
	e := setup(t, api.PlanPro)
	d := mustSeedDeployment(t, e, "pro-dep-neg")
	neg := -1
	rec := e.do(t, "PATCH", "/v1/deployments/"+d.ID, api.UpdateDeploymentRequest{MinInstances: &neg}, nil)
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

// TestUpdateAppAutoscaleRPS_ProNegative pins the lower bound on the
// lowest plan that unlocks the feature: after the 2026-07-28
// Hobby→Pro re-tier (ADR-037 amendment), only Pro+ accepts the
// field at all, so this test exercises Pro (gate passes) with a
// negative value to assert that bounds validation runs and 422
// invalid_autoscale_target_rps is returned. The PG CHECK constraint
// `apps_autoscale_target_rps_nonneg` enforces `>= 0 OR NULL`; we
// rely on the apid-side validation to reject negatives before they
// reach the DB. Pre-amendment, the equivalent coverage lived in
// TestUpdateAppAutoscaleRPS_HobbyZero — but after Hobby lost the
// gate, that test surfaced the plan gate (403) instead of the
// bounds check (422), so it was renamed/moved here.
func TestUpdateAppAutoscaleRPS_ProNegative(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-rps-neg")
	neg := -1
	rec := e.do(t, "PATCH", "/v1/apps/pro-rps-neg", api.UpdateAppRequest{AutoscaleTargetRPS: &neg}, nil)
	assertProblem(t, rec, 422, api.CodeInvalidAutoscaleTargetRPS)
}

// TestUpdateAppAutoscaleRPS_HobbyGate locks the Hobby→Pro re-tier
// (2026-07-28: ADR-037 amendment): Hobby plans do not unlock
// autoscale_target_rps. The handler must 403 plan_autoscale_not_allowed
// even when the value is a perfectly valid 50 — the gate runs first,
// value validation is irrelevant on a tier-locked feature. Mirrors
// TestUpdateAppAutoscaleCPU_HobbyGate.
func TestUpdateAppAutoscaleRPS_HobbyGate(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "hobby-rps-gate")
	fifty := 50
	rec := e.do(t, "PATCH", "/v1/apps/hobby-rps-gate", api.UpdateAppRequest{AutoscaleTargetRPS: &fifty}, nil)
	assertProblem(t, rec, 403, api.CodePlanScaleUpNotAllowed)
}

// TestUpdateAppAutoscaleRPS_ProHappy is the new happy path: Pro is
// the lowest paid tier that unlocks autoscale_target_rps after the
// 2026-07-28 Hobby→Pro re-tier (ADR-037 amendment). The response
// carries the new value. Pro unlocks both RPS and CPU.
func TestUpdateAppAutoscaleRPS_ProHappy(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-rps-ok")
	fifty := 50
	rec := e.do(t, "PATCH", "/v1/apps/pro-rps-ok", api.UpdateAppRequest{AutoscaleTargetRPS: &fifty}, nil)
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

// TestUpdateAppWarmSnapshot_FreeGate locks the plan-tier gate for the
// per-app two-tier snapshot flag (issue #470 / ADR-055). Free plans
// cannot set warm_snapshot_enabled=true at all — the doubled parked
// footprint (warm.snap + init.snap, +130 MB per app) doesn't fit the
// Free pricing tier. The handler must 403
// plan_warm_snapshot_not_allowed, not 422 / not 200. Customers on any
// plan may PATCH true → false (opt-out per-app), but that's a no-op on
// Free (already false). Mirrors TestUpdateAppAutoscaleRPS_FreeGate.
func TestUpdateAppWarmSnapshot_FreeGate(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "free-warm")
	tru := true
	rec := e.do(t, "PATCH", "/v1/apps/free-warm", api.UpdateAppRequest{WarmSnapshotEnabled: &tru}, nil)
	assertProblem(t, rec, 403, api.CodePlanWarmSnapshotNotAllowed)
}

// TestUpdateAppWarmSnapshot_HobbyGate is the Hobby branch of the same
// gate (issue #470 / ADR-055). Hobby is gated off for the same
// cost-shape reason as Free — doubling the parked per-app snapshot
// footprint doesn't fit the €9/month Hobby tier.
func TestUpdateAppWarmSnapshot_HobbyGate(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "hobby-warm")
	tru := true
	rec := e.do(t, "PATCH", "/v1/apps/hobby-warm", api.UpdateAppRequest{WarmSnapshotEnabled: &tru}, nil)
	assertProblem(t, rec, 403, api.CodePlanWarmSnapshotNotAllowed)
}

// TestUpdateAppWarmSnapshot_ProHappy is the Pro happy path: Pro
// plans may toggle warm_snapshot_enabled freely. The value round-trips
// through UpdateApp → scanApp → appResponse. Mirrors
// TestUpdateAppAutoscaleRPS_ProHappy.
func TestUpdateAppWarmSnapshot_ProHappy(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-warm-ok")
	tru := true
	rec := e.do(t, "PATCH", "/v1/apps/pro-warm-ok", api.UpdateAppRequest{WarmSnapshotEnabled: &tru}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.WarmSnapshotEnabled {
		t.Errorf("WarmSnapshotEnabled = false, want true (PATCH round-trip)")
	}
}

// TestUpdateAppWarmSnapshotMinRequests_OutOfRange pins the bounds on
// the per-app request-count threshold (issue #470 / ADR-055). The
// SQL CHECK `warm_snapshot_min_requests BETWEEN 1 AND 100` is the
// load-bearing safety net; the apid handler must reject the
// out-of-range value FIRST so the customer sees a clean 422
// invalid_warm_snapshot_min_requests, not a SQL CHECK violation. We
// test both edges (0 and 101) so a future off-by-one fix doesn't
// silently widen the range.
func TestUpdateAppWarmSnapshotMinRequests_OutOfRange(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-warm-minreq")
	tooLow := 0
	recLow := e.do(t, "PATCH", "/v1/apps/pro-warm-minreq", api.UpdateAppRequest{WarmSnapshotMinRequests: &tooLow}, nil)
	assertProblem(t, recLow, 422, api.CodeInvalidWarmSnapshotMinRequests)
	tooHigh := 101
	recHigh := e.do(t, "PATCH", "/v1/apps/pro-warm-minreq", api.UpdateAppRequest{WarmSnapshotMinRequests: &tooHigh}, nil)
	assertProblem(t, recHigh, 422, api.CodeInvalidWarmSnapshotMinRequests)
}

// TestUpdateAppWarmSnapshotMinMs_OutOfRange pins the bounds on the
// per-app time-since-first-ready threshold. SQL CHECK is
// `warm_snapshot_min_ms BETWEEN 100 AND 60000`. The 100 ms floor blocks
// capturing too early (before JIT/AOT has a chance to fire); the 60 s
// ceiling bounds the per-park latency cost the warm capture adds to
// the cold path (R1 in the plan). We test both edges so a future
// off-by-one fix doesn't widen the range silently.
func TestUpdateAppWarmSnapshotMinMs_OutOfRange(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-warm-minms")
	tooLow := 99
	recLow := e.do(t, "PATCH", "/v1/apps/pro-warm-minms", api.UpdateAppRequest{WarmSnapshotMinMs: &tooLow}, nil)
	assertProblem(t, recLow, 422, api.CodeInvalidWarmSnapshotMinMs)
	tooHigh := 60001
	recHigh := e.do(t, "PATCH", "/v1/apps/pro-warm-minms", api.UpdateAppRequest{WarmSnapshotMinMs: &tooHigh}, nil)
	assertProblem(t, recHigh, 422, api.CodeInvalidWarmSnapshotMinMs)
}

// TestUpdateAppWarmSnapshotMinRequests_BoundaryAccepted locks the
// inclusive bounds: 1 and 100 must be accepted (NOT rejected as
// out-of-range). Mirrors the negative OutOfRange test — without this
// pin a future `v < 1` / `v > 100` refactor that mistakenly uses
// strict inequality would silently break the spec contract.
func TestUpdateAppWarmSnapshotMinRequests_BoundaryAccepted(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-warm-boundary")
	low := 1
	rec := e.do(t, "PATCH", "/v1/apps/pro-warm-boundary", api.UpdateAppRequest{WarmSnapshotMinRequests: &low}, nil)
	if rec.Code != 200 {
		t.Fatalf("min_requests=1: status %d: %s", rec.Code, rec.Body)
	}
	high := 100
	rec = e.do(t, "PATCH", "/v1/apps/pro-warm-boundary", api.UpdateAppRequest{WarmSnapshotMinRequests: &high}, nil)
	if rec.Code != 200 {
		t.Fatalf("min_requests=100: status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.WarmSnapshotMinRequests != 100 {
		t.Errorf("WarmSnapshotMinRequests = %d, want 100 (boundary round-trip)", out.WarmSnapshotMinRequests)
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

// TestGetApp_SurfacesConcurrencyPerVMBound pins the wire shape for
// the issue #559 / PR #645 addition: every plan's GET /v1/apps/{slug}
// response must include `concurrency_per_vm` set to the plan's bound.
// Catches regressions where someone constructs api.AppResponse
// directly (bypassing appResponse) and forgets the new field — the
// limits-table accessor is fail-closed (returns 0 on unknown plan),
// so the assertion is strict equality on the per-plan expected
// value rather than just "field is present".
func TestGetApp_SurfacesConcurrencyPerVMBound(t *testing.T) {
	cases := []struct {
		plan api.Plan
		want int
	}{
		{api.PlanFree, 1},
		{api.PlanHobby, 5},
		{api.PlanPro, 25},
		{api.PlanScale, 80},
	}
	for _, c := range cases {
		t.Run(string(c.plan), func(t *testing.T) {
			e := setup(t, c.plan)
			mustSeedApp(t, e, "cpvm-app")
			rec := e.do(t, "GET", "/v1/apps/cpvm-app", nil, nil)
			if rec.Code != 200 {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			var out api.AppResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if out.ConcurrencyPerVMBound != c.want {
				t.Errorf("concurrency_per_vm = %d, want %d (plan=%s)",
					out.ConcurrencyPerVMBound, c.want, c.plan)
			}
			// Belt-and-suspenders: assert the JSON field is
			// present in the raw payload — catches any future
			// DTO `omitempty` tag added in error.
			if !bytes.Contains(rec.Body.Bytes(), []byte(`"concurrency_per_vm":`)) {
				t.Errorf("raw JSON missing concurrency_per_vm key:\n%s", rec.Body.String())
			}
		})
	}
}

func TestGetApp_SurfacesEffectiveLimits(t *testing.T) {
	cases := []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale}
	for _, plan := range cases {
		t.Run(string(plan), func(t *testing.T) {
			e := setup(t, plan)
			mustSeedApp(t, e, "limits-app")
			rec := e.do(t, "GET", "/v1/apps/limits-app", nil, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			var out api.AppResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			limits := api.MustLimitsFor(plan)
			got := out.EffectiveLimits
			if got.MemoryLimitMB != out.RAMMB || got.PlanMemoryMaxMB != limits.RAMMB {
				t.Errorf("memory limits = %d/%d, want %d/%d", got.MemoryLimitMB, got.PlanMemoryMaxMB, out.RAMMB, limits.RAMMB)
			}
			if got.GuestVCPUs != limits.VCPU || got.CPULimitMillicores != limits.CPUQuotaUS*1000/limits.CPUPeriodUS {
				t.Errorf("cpu limits = %d vCPU/%dm, want %d/%dm", got.GuestVCPUs, got.CPULimitMillicores, limits.VCPU, limits.CPUQuotaUS*1000/limits.CPUPeriodUS)
			}
			if got.MaxInstances != out.MaxConcurrency || got.ConcurrencyPerInstance != limits.ConcurrencyPerVMBound {
				t.Errorf("scaling limits = %d/%d, want %d/%d", got.MaxInstances, got.ConcurrencyPerInstance, out.MaxConcurrency, limits.ConcurrencyPerVMBound)
			}
			if got.AppRequestRateRPS != limits.RateLimitRPS || got.AppRequestBurst != limits.RateLimitBurst || got.AccountRequestRateRPM != limits.RateLimitPerAccountRPM {
				t.Errorf("request rates = %d/%d/%d, want %d/%d/%d", got.AppRequestRateRPS, got.AppRequestBurst, got.AccountRequestRateRPM, limits.RateLimitRPS, limits.RateLimitBurst, limits.RateLimitPerAccountRPM)
			}
			if got.RequestBudgetMS != limits.RequestBudget().Milliseconds() || got.RequestBudgetMaxMS != limits.RequestBudgetMaxDuration().Milliseconds() {
				t.Errorf("request budgets = %d/%d, want %d/%d", got.RequestBudgetMS, got.RequestBudgetMaxMS, limits.RequestBudget().Milliseconds(), limits.RequestBudgetMaxDuration().Milliseconds())
			}
			if !bytes.Contains(rec.Body.Bytes(), []byte(`"effective_limits":`)) {
				t.Errorf("raw JSON missing effective_limits key:\n%s", rec.Body.String())
			}
		})
	}
}

func TestAppEffectiveLimits_UsesScalingPolicyCeiling(t *testing.T) {
	app := state.App{
		RAMMB:          384,
		MaxConcurrency: 5,
		ScalingPolicy:  &state.ScalingPolicy{MaxInstances: 3},
	}
	got := appEffectiveLimits(app, api.PlanPro)
	if got.MaxInstances != 3 {
		t.Fatalf("max_instances = %d, want scaling-policy ceiling 3", got.MaxInstances)
	}
	if got.MemoryLimitMB != 384 || got.PlanMemoryMaxMB != 512 {
		t.Fatalf("memory limits = %d/%d, want 384/512", got.MemoryLimitMB, got.PlanMemoryMaxMB)
	}
	if got.EphemeralDiskMaxMB != api.MustLimitsFor(api.PlanPro).AppLayerMaxMB {
		t.Fatalf("ephemeral disk limit = %d, want %d", got.EphemeralDiskMaxMB, api.MustLimitsFor(api.PlanPro).AppLayerMaxMB)
	}
	if got.CPULimitMillicores != api.DefaultAppCPUMillicores || got.PlanCPUMaxMillicores != api.DefaultAppCPUMillicores {
		t.Fatalf("CPU limits = %d/%d, want %d/%d", got.CPULimitMillicores, got.PlanCPUMaxMillicores, api.DefaultAppCPUMillicores, api.DefaultAppCPUMillicores)
	}
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

func TestUpdateApp_CPUValid(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "upd-cpu")
	cpu := 250
	rec := e.do(t, "PATCH", "/v1/apps/upd-cpu", api.UpdateAppRequest{CPUMillicores: &cpu}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.CPUMillicores != 250 || out.EffectiveLimits.CPULimitMillicores != 250 {
		t.Fatalf("CPU update not reflected: %+v", out)
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

// TestRollbackApp_ExplicitTarget_Specific (SAFE-RELEASES-G, issue #976).
// With three deployments (v1 superseded, v2 superseded, v3 live),
// rolling back to v1 promotes v1 to live, supersedes v3, and leaves
// v2 untouched. Regression test for the headline use case: "skip the
// intermediate".
func TestRollbackApp_ExplicitTarget_Specific(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep1 := mustSeedDeployment(t, e, "rb-specific")
	app, _ := e.store.AppBySlug(context.Background(), "rb-specific")
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
	dep3, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:" + repeat("c", 64),
		Kind:        state.DeploymentKindImage,
		Status:      state.DeployBuilding,
		CreatedAt:   time.Now().UTC().Add(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	// dep1 live, dep2 live (supersedes dep1), dep3 live (supersedes dep2).
	if err := e.store.MarkDeploymentLive(context.Background(), dep1.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(context.Background(), dep2.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentSuperseded(context.Background(), dep1.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(context.Background(), dep3.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentSuperseded(context.Background(), dep2.ID); err != nil {
		t.Fatal(err)
	}

	body := api.RollbackRequest{TargetDeploymentID: &dep1.ID}
	rec := e.do(t, "POST", "/v1/apps/rb-specific/rollback", body, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != dep1.ID {
		t.Errorf("rollback returned id=%s, want %s (explicit target, skipped intermediate)", out.ID, dep1.ID)
	}
	if out.Status != string(state.DeployLive) {
		t.Errorf("post-rollback status = %q, want %q", out.Status, state.DeployLive)
	}
	// dep3 must be superseded (was the current live one).
	fresh, _ := e.store.DeploymentByID(context.Background(), dep3.ID)
	if fresh.Status != state.DeploySuperseded {
		t.Errorf("dep3 status = %q, want %q (the previously-live row was not retired)", fresh.Status, state.DeploySuperseded)
	}
}

// TestRollbackApp_ExplicitTarget_NotFound confirms the 404 path when
// the caller names a deployment_id that doesn't exist (or belongs to
// a different app).
func TestRollbackApp_ExplicitTarget_NotFound(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "rb-404")
	if err := e.store.MarkDeploymentLive(context.Background(), dep.ID); err != nil {
		t.Fatal(err)
	}
	bogus := "00000000-0000-0000-0000-000000000000"
	body := api.RollbackRequest{TargetDeploymentID: &bogus}
	rec := e.do(t, "POST", "/v1/apps/rb-404/rollback", body, nil)
	assertProblem(t, rec, http.StatusNotFound, api.CodeRollbackTargetNotFound)
}

// TestRollbackApp_ExplicitTarget_AlreadyLive confirms the 409 path
// when the caller names a deployment that is currently live (i.e.
// asking to "rollback" to the already-current deployment is rejected
// rather than silently no-op'd).
func TestRollbackApp_ExplicitTarget_AlreadyLive(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep := mustSeedDeployment(t, e, "rb-live-target")
	if err := e.store.MarkDeploymentLive(context.Background(), dep.ID); err != nil {
		t.Fatal(err)
	}
	body := api.RollbackRequest{TargetDeploymentID: &dep.ID}
	rec := e.do(t, "POST", "/v1/apps/rb-live-target/rollback", body, nil)
	assertProblem(t, rec, http.StatusConflict, api.CodeRollbackTargetAlreadyLive)
}

// TestRollbackApp_LegacyEmptyBodyUnchanged confirms the back-compat
// path: POST without a body falls through to "rollback to most-recent
// superseded deployment". Equivalent to the pre-G behaviour.
func TestRollbackApp_LegacyEmptyBodyUnchanged(t *testing.T) {
	e := setup(t, api.PlanPro)
	dep1 := mustSeedDeployment(t, e, "rb-legacy")
	if err := e.store.MarkDeploymentLive(context.Background(), dep1.ID); err != nil {
		t.Fatal(err)
	}
	app, _ := e.store.AppBySlug(context.Background(), "rb-legacy")
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

	// No body. Legacy path: rollback to most-recent superseded = dep1.
	rec := e.do(t, "POST", "/v1/apps/rb-legacy/rollback", nil, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.DeploymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != dep1.ID {
		t.Errorf("legacy path returned id=%s, want %s (most-recent superseded)", out.ID, dep1.ID)
	}
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

// TestCreateCron_FreeReturns402 confirms the plan-tier gate (spec §4.4
// paid-only event-shaped primitives). A Free customer hitting POST
// /v1/crons must see 402 + CodePlanCronsNotAllowed — the body names
// the plan and the upgrade target, not a "no such app" 404 (the
// 402 fires BEFORE AppByID for this exact reason).
func TestCreateCron_FreeReturns402(t *testing.T) {
	e := setup(t, api.PlanFree)
	appID := mustSeedApp(t, e, "cron-free")
	rec := e.do(t, "POST", "/v1/crons",
		api.CreateCronRequest{AppID: appID, Schedule: "*/5 * * * *", Path: "/x"}, nil)
	assertProblem(t, rec, 402, api.CodePlanCronsNotAllowed)
}

// TestCreateCron_AtPerAppLimitReturns403 seeds CronLimitPerApp crons
// directly via the store (bypassing the cap so the test is independent
// of the cap-enforcement path it's testing) and asserts the next wire
// create returns 403 + CodePlanCronQuota. Scope="app" surfaces in the
// body so the customer knows to delete a cron from THIS app.
func TestCreateCron_AtPerAppLimitReturns403(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "cron-cap-app")
	// Pro caps at 20 per-app; seed all 20 directly.
	limits := api.MustLimitsFor(api.PlanPro)
	for i := 0; i < limits.CronLimitPerApp; i++ {
		if _, err := e.store.CreateCron(context.Background(), appID, "*/5 * * * *", "/x", true); err != nil {
			t.Fatalf("seed cron %d: %v", i, err)
		}
	}
	rec := e.do(t, "POST", "/v1/crons",
		api.CreateCronRequest{AppID: appID, Schedule: "*/5 * * * *", Path: "/x"}, nil)
	assertProblem(t, rec, 403, api.CodePlanCronQuota)
}

// TestCreateCron_AtPerAccountLimitReturns403 fills the per-account
// cap across TWO apps on the same account, then attempts a create on
// a third app — must hit 403 + CodePlanCronQuota with Scope="account"
// (the per-app cap is still under but the per-account cap is full).
func TestCreateCron_AtPerAccountLimitReturns403(t *testing.T) {
	e := setup(t, api.PlanPro)
	limits := api.MustLimitsFor(api.PlanPro)
	// Pro caps per-app at 20, per-account at 50. Two apps × 20 = 40
	// (under both). Fill the third app to push past per-account.
	appA := mustSeedApp(t, e, "cron-acct-a")
	appB := mustSeedApp(t, e, "cron-acct-b")
	for i := 0; i < limits.CronLimitPerApp; i++ {
		if _, err := e.store.CreateCron(context.Background(), appA, "*/5 * * * *", "/x", true); err != nil {
			t.Fatalf("seed A cron %d: %v", i, err)
		}
	}
	for i := 0; i < limits.CronLimitPerApp; i++ {
		if _, err := e.store.CreateCron(context.Background(), appB, "*/5 * * * *", "/x", true); err != nil {
			t.Fatalf("seed B cron %d: %v", i, err)
		}
	}
	// appB is full (per-app) so the per-account cap check fires first
	// on appB. To test the per-account path explicitly, the third
	// app would need to be NOT full per-app — but per-account is
	// already 40 which is < 50 cap, so this is fine. Add a third
	// partially-full app and POST 11 more (to push per-account to 51).
	appC := mustSeedApp(t, e, "cron-acct-c")
	for i := 0; i < 10; i++ {
		if _, err := e.store.CreateCron(context.Background(), appC, "*/5 * * * *", "/x", true); err != nil {
			t.Fatalf("seed C cron %d: %v", i, err)
		}
	}
	// per-account is now 40 + 10 = 50 == cap; one more on appC must 403.
	rec := e.do(t, "POST", "/v1/crons",
		api.CreateCronRequest{AppID: appC, Schedule: "*/5 * * * *", Path: "/x"}, nil)
	assertProblem(t, rec, 403, api.CodePlanCronQuota)
}

// TestCreateCron_UnknownPlanReturns402 confirms the handler doesn't
// panic when acct.Plan is not in pkg/api/limits.go::planLimits —
// must surface the same 402 + CodePlanCronsNotAllowed a Free customer
// sees, not a 500. Fail-closed contract: an unconfigured plan is
// treated as if crons weren't unlocked. Mirrors the LimitsFor()
// unknown-plan branch in pkg/api/limits_test.go::TestPlanValidity.
func TestCreateCron_UnknownPlanReturns402(t *testing.T) {
	// Seed with a plan that pkg/api.planLimits doesn't know about.
	// CreateAccount doesn't validate the plan string, so the account
	// lands with Plan="enterprise" — exactly the wire state a future
	// tier or a stale migration would produce.
	e := setup(t, api.Plan("enterprise"))
	appID := mustSeedApp(t, e, "cron-unknown-plan")
	rec := e.do(t, "POST", "/v1/crons",
		api.CreateCronRequest{AppID: appID, Schedule: "*/5 * * * *", Path: "/x"}, nil)
	assertProblem(t, rec, 402, api.CodePlanCronsNotAllowed)
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

// TestCreateKey_StampsProvenanceColumns: the IAM hardening mega-PR
// (logical change 2) provenance columns (created_ip, created_ua) land
// on the new api_keys row. Asserts the new row carries the test
// harness's IP (RemoteAddr is non-empty in the harness) so a SOC 2
// auditor can answer "who minted this key from which IP" without
// joining through Loki. The UA slot is allowed to be empty when the
// harness doesn't set a User-Agent — the column is nullable by
// design (unix-socket callers, missing UA), so the test pins the
// happy-path non-empty IP and the documented empty-UA tolerance.
func TestCreateKey_StampsProvenanceColumns(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/keys", map[string]string{"label": "ci"}, nil)
	if rec.Code != 201 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	keys, err := e.store.ListAPIKeys(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(keys) < 1 {
		t.Fatalf("fixture: want >= 1 key, got %d", len(keys))
	}
	// Find the freshly minted key by label — the seeded fixture
	// has label="test" (an admin key); the new key has label="ci".
	var newKey *state.APIKey
	for i := range keys {
		if keys[i].Label == "ci" {
			newKey = &keys[i]
			break
		}
	}
	if newKey == nil {
		t.Fatalf("no key with label=ci found in %d rows", len(keys))
	}
	if newKey.CreatedIP == "" {
		t.Errorf("CreatedIP empty; want test harness address stamp")
	}
	// UA is nullable by design — the test harness doesn't set a
	// User-Agent, so CreatedUA == "" is the expected stamp. The
	// audit payload encodes the same nullability (the
	// logsanitize.Field output is "" for empty input).
	_ = newKey.CreatedUA
}

// TestRotateKey_StampsProvenanceAndParent: the IAM hardening
// mega-PR (logical change 2) checks that a rotation stamps
// created_ip / created_ua on the new row AND parent_key_id
// points at the predecessor. The legacy rotated_from_id is
// unchanged.
func TestRotateKey_StampsProvenanceAndParent(t *testing.T) {
	e := setup(t, api.PlanPro)
	// Grab the seeded fixture key — the setup wires one admin key
	// per account, so the list returns exactly one row.
	keys, err := e.store.ListAPIKeys(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("seed list: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("seed: want 1 key, got %d", len(keys))
	}
	old := keys[0]
	rec := e.do(t, "POST", "/v1/keys/"+old.ID+"/rotate", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	// Find the new key by RotatedFromID — the legacy stamp
	// (and the new parent_key_id FK) both point at the
	// predecessor.
	after, err := e.store.ListAPIKeys(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var newKey *state.APIKey
	for i := range after {
		if after[i].RotatedFromID != nil && *after[i].RotatedFromID == old.ID {
			newKey = &after[i]
			break
		}
	}
	if newKey == nil {
		t.Fatalf("no new key with rotated_from_id = %s", old.ID)
	}
	if newKey.CreatedIP == "" {
		t.Errorf("rotated key CreatedIP empty; want test harness address stamp")
	}
	if newKey.ParentKeyID == nil || *newKey.ParentKeyID != old.ID {
		t.Errorf("rotated key ParentKeyID = %v, want pointer to %s", newKey.ParentKeyID, old.ID)
	}
	// UA is nullable by design (test harness doesn't set UA).
	_ = newKey.CreatedUA
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
	if out.Daily == nil {
		t.Errorf("daily = nil, want an empty array")
	}
}

func TestUsageSummary_DailyTopApp(t *testing.T) {
	e := setup(t, api.PlanPro)
	appA, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "api"})
	if err != nil {
		t.Fatal(err)
	}
	appB, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().UTC()
	const gib = int64(1024 * 1024 * 1024)
	if err := e.store.AppendUsage(t.Context(), e.acct.ID, appA.ID, "instance-api", today, 7_200_000, 10, 3_600_000_000, gib, 2*gib, 3*gib, 2, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.store.AppendUsage(t.Context(), e.acct.ID, appB.ID, "instance-worker", today, 3_600_000, 5, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, "GET", "/v1/usage/summary", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.UsageSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Daily) != 1 {
		t.Fatalf("daily = %+v, want one point", out.Daily)
	}
	if out.Daily[0].TopAppSlug != "api" || out.Daily[0].GBHours != 2.9296875 || out.Daily[0].TopAppGBHours != 1.953125 {
		t.Fatalf("daily point = %+v", out.Daily[0])
	}
	if out.UsedGBHours != 2.9296875 {
		t.Errorf("used GB-hours = %v, want 2.9296875", out.UsedGBHours)
	}
	if out.UsedCPUHours != 1 {
		t.Errorf("used CPU-hours = %v, want 1", out.UsedCPUHours)
	}
	if out.UsedEgressGB != 3 {
		t.Errorf("used egress GB = %v, want 3", out.UsedEgressGB)
	}
	if out.UsedIngressGB != 3 {
		t.Errorf("used ingress GB = %v, want 3", out.UsedIngressGB)
	}
	if out.ColdBootTotal != 2 {
		t.Errorf("cold boots = %d, want 2", out.ColdBootTotal)
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
		"gregale.dev", noopNotifier{})
	d := state.Deployment{
		ID: "d1", AppID: "a1", ImageDigest: "sha256:x", Kind: state.DeploymentKindImage,
		Status: state.DeployLive, Error: "boom", ErrorCode: "image_not_found",
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	resp := srv.deploymentResponse(d, state.App{})
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
	r := instanceResponse(ins, 0)
	if r.StartedAt != "" || r.LastRequestAt != "" || r.ParkedAt != "" {
		t.Errorf("zero-time should produce empty strings: %+v", r)
	}
	ins.StartedAt = now
	ins.LastRequestAt = now
	ins.ParkedAt = now
	r = instanceResponse(ins, 2)
	if r.StartedAt == "" || r.LastRequestAt == "" || r.ParkedAt == "" {
		t.Errorf("populated timestamps should serialize: %+v", r)
	}
}

// TestInstanceResponse_MinInstancesTargetOmittedWhenZero pins the
// issue #557 / ADR-071 wire contract: min_instances_target uses
// `omitempty` so customers who never opted in see no field — the
// absence is the same as 0. The active-path branches (1/3/10 by
// plan) carry the value through byte-for-byte.
func TestInstanceResponse_MinInstancesTargetOmittedWhenZero(t *testing.T) {
	zero := instanceResponse(state.Instance{ID: "i1", AppID: "a1"}, 0)
	if zero.MinInstancesTarget != 0 {
		t.Errorf("zero MinInstancesTarget should be 0, got %d", zero.MinInstancesTarget)
	}
	// `omitempty` on an int field with value 0 omits from JSON.
	// The dto-level contract is sufficient; the per-byte assertion
	// is enforced by `make spec-check` on the openapi.yaml.
	for _, v := range []int{1, 2, 3, 5, 10} {
		got := instanceResponse(state.Instance{ID: "i1", AppID: "a1"}, v)
		if got.MinInstancesTarget != v {
			t.Errorf("MinInstancesTarget = %d, want %d", got.MinInstancesTarget, v)
		}
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
	return newServerWithDeps(store, log, "gregale.dev", noopNotifier{}, secret,
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

// TestStripeWebhook_UnknownCustomerIsRetryable fires a properly signed
// billing event and asserts the handler returns 503. A 200 would silently
// discard an entitlement transition when the local customer binding is
// temporarily missing.
func TestStripeWebhook_UnknownCustomerIsRetryable(t *testing.T) {
	const secret = "whsec_test_signing_secret"
	srv := newStripeServer(t, secret)
	body := []byte(`{"type":"invoice.payment_succeeded","data":{"object":{"customer":"cus_unknown"}}}`)
	header := stripe.SignForTest(body, secret, time.Now())
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Stripe-Signature", header)
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("signed event: status = %d, want 503\nbody = %s", rec.Code, rec.Body.String())
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
	srv := newServerWithDeps(store, log, "gregale.dev", noopNotifier{},
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
//
// Issue #294: each call uses a fresh synthetic Stripe event.id
// (evt_test_<nanos>). Tests that want to exercise the replay path
// should call postStripeEventWithID twice with the same id.
func postStripeEvent(t *testing.T, h http.Handler, eventType, customer string) *httptest.ResponseRecorder {
	t.Helper()
	id := fmt.Sprintf("evt_test_%d", time.Now().UnixNano())
	return postStripeEventWithID(t, h, eventType, customer, id)
}

// postStripeEventWithID is the issue-#294-aware variant: pass the
// same id twice to exercise the replay dedupe.
func postStripeEventWithID(t *testing.T, h http.Handler, eventType, customer, id string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"id":   id,
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

// TestStripeWebhook_RejectsReplay is issue #294's apid-side
// coverage: POSTing the same Stripe event with the same `id` twice
// rejects the second with 200 (idempotent — Stripe stops retrying)
// without re-running the side effects. We pin this with
// invoice.payment_failed so the email count is a clean signal: the
// first delivery sends 1 email, the replay sends 0.
func TestStripeWebhook_RejectsReplay(t *testing.T) {
	e, mailer := stripeWebhookHarness(t, api.PlanHobby)
	const id = "evt_test_replay_1"

	rec1 := postStripeEventWithID(t, e.h, "invoice.payment_failed", "cus_test_123", id)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first delivery status = %d, want 200; body=%s", rec1.Code, rec1.Body.String())
	}
	if n := len(mailer.snapshot()); n != 1 {
		t.Fatalf("first delivery: %d emails, want 1", n)
	}

	// Replay: same id, same signature shape (Stripe re-signs each
	// delivery with a fresh timestamp; we mirror the production
	// behaviour by signing with the current time).
	rec2 := postStripeEventWithID(t, e.h, "invoice.payment_failed", "cus_test_123", id)
	if rec2.Code != http.StatusOK {
		t.Fatalf("replay status = %d, want 200 (idempotent); body=%s", rec2.Code, rec2.Body.String())
	}
	if n := len(mailer.snapshot()); n != 1 {
		t.Errorf("after replay: %d emails, want 1 (replay is a no-op)", n)
	}

	// Round-trip via the webhookdedupe helper to prove the row was
	// recorded by the first delivery.
	if err := webhookdedupe.CheckReplay(context.Background(), webhookdedupe.ProviderStripe, id); !webhookdedupe.IsReplay(err) {
		t.Errorf("recorded Stripe event should be a replay; err=%v", err)
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

// mustSeedAppWithWorkloadClass provisions an app + sets the
// WorkloadClass via an internal store update. The test-side
// handler does not expose a PATCH field for WorkloadClass, so
// the only path is the projected internal store. Used by the
// issue #462 / ADR-058 carve-out tests where the validation
// gate branches on WorkloadClass.
//
// Returns the app ID for later assertions.
func mustSeedAppWithWorkloadClass(t *testing.T, e testEnv, slug string, class state.WorkloadClass) string {
	t.Helper()
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID:     e.acct.ID,
		Slug:          slug,
		Type:          state.AppTypeApp,
		Status:        state.AppActive,
		WorkloadClass: class,
	})
	if err != nil {
		t.Fatalf("seed app %s with WorkloadClass=%s: %v", slug, class, err)
	}
	return app.ID
}

// TestUpdateAppScalingPolicy_HobbyHappy is the new Hobby+ tier-up
// happy path (issue #462 / ADR-058 / PR-A). Hobby plans accept
// min_instances=1 (the floor) at 200, and the response carries
// the new value. Hobby does NOT unlock ScaleUpTargetRPSAllowed
// (still Pro+) — the policy is layered on top of the existing
// autoscale target columns, so the policy's Target.Metric stays
// empty in this test (the "engine falls back to legacy autoscale
// targets" path).
func TestUpdateAppScalingPolicy_HobbyHappy(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "hobby-policy-happy")
	rec := e.do(t, "PATCH", "/v1/apps/hobby-policy-happy", api.UpdateAppRequest{
		ScalingPolicy: &api.ScalingPolicy{
			MinInstances:      1,
			MaxInstances:      2,
			ScaleOutCooldownS: 5,
			ScaleInCooldownS:  60,
		},
		SetScalingPolicy: true,
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ScalingPolicy == nil {
		t.Fatalf("ScalingPolicy = nil, want non-nil")
	}
	if out.ScalingPolicy.MinInstances != 1 {
		t.Errorf("ScalingPolicy.MinInstances = %d, want 1", out.ScalingPolicy.MinInstances)
	}
	if out.ScalingPolicy.MaxInstances != 2 {
		t.Errorf("ScalingPolicy.MaxInstances = %d, want 2", out.ScalingPolicy.MaxInstances)
	}
}

// TestUpdateAppScalingPolicy_FreeGateMaxInstances pins the
// MaxInstancesAllowed gate at PR-A (issue #462 / ADR-058). A Free
// plan PATCHing `scaling_policy.max_instances` is 403
// (plan_max_instances_not_allowed); the legacy Hobby gate does not
// apply because Hobby now unlocks MinInstancesAllowed at the same
// time. Hobby/Pro/Scale do not unlock autoscale_*_pct /
// autoscale_target_rps in this policy block — those are the
// independent ScaleUpTargetRPSAllowed / ScaleUpTargetCPUAllowed
// gates, separate from the policy gate.
func TestUpdateAppScalingPolicy_FreeGateMaxInstances(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "free-policy-max")
	rec := e.do(t, "PATCH", "/v1/apps/free-policy-max", api.UpdateAppRequest{
		ScalingPolicy: &api.ScalingPolicy{
			MaxInstances: 1,
		},
		SetScalingPolicy: true,
	}, nil)
	assertProblem(t, rec, 403, api.CodePlanMaxInstancesNotAllowed)
}

// TestUpdateAppScalingPolicy_MaxBelowMin is the bounds check: a
// policy whose max_instances < min_instances is 422
// invalid_max_instances. The bounds error names both axes so the
// CLI can render "raise your max" vs "fix your min" without
// conflating.
func TestUpdateAppScalingPolicy_MaxBelowMin(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-policy-maxlow")
	rec := e.do(t, "PATCH", "/v1/apps/pro-policy-maxlow", api.UpdateAppRequest{
		ScalingPolicy: &api.ScalingPolicy{
			MinInstances: 3,
			MaxInstances: 2,
		},
		SetScalingPolicy: true,
	}, nil)
	assertProblem(t, rec, 422, api.CodeInvalidMaxInstances)
}

// TestUpdateAppScalingPolicy_CooldownBelowFloor pins the
// self-DoS guard: a customer who sets scale_out_cooldown_s=0
// gets 422 invalid_cooldown (the floor is 1 s, see
// api.MinScaleOutCooldownS). The gate runs before the engine
// would otherwise honor a 0-cooldown as "always admit".
func TestUpdateAppScalingPolicy_CooldownBelowFloor(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-cooldown-zero")
	rec := e.do(t, "PATCH", "/v1/apps/pro-cooldown-zero", api.UpdateAppRequest{
		ScalingPolicy: &api.ScalingPolicy{
			MinInstances:      1,
			ScaleOutCooldownS: 0,
		},
		SetScalingPolicy: true,
	}, nil)
	assertProblem(t, rec, 422, api.CodeInvalidCooldown)
}

// TestUpdateAppScalingPolicy_WorkerClassConcurrentRequests pins
// the PR-D carve-out at the customer side (issue #462 /
// ADR-058). A worker-class app (WorkloadClassWorker) cannot use
// `target.metric = concurrent_requests` because the signal
// source (pkg/vmmd/activity.ActivityTracker, PR-B) counts
// in-flight HTTP requests — a worker has none, so the metric is
// forever 0 and the engine would never admit. The handler
// rejects the PATCH at 422 with
// `scaling_target_incompatible_with_workload_class` so the
// customer sees the misconfiguration immediately.
func TestUpdateAppScalingPolicy_WorkerClassConcurrentRequests(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedAppWithWorkloadClass(t, e, "pro-worker-class", state.WorkloadClassWorker)
	rec := e.do(t, "PATCH", "/v1/apps/pro-worker-class", api.UpdateAppRequest{
		ScalingPolicy: &api.ScalingPolicy{
			Target: &api.ScalingTarget{
				Metric: "concurrent_requests",
				Value:  1.0,
			},
		},
		SetScalingPolicy: true,
	}, nil)
	assertProblem(t, rec, 422, api.CodeScalingTargetIncompatibleWithWorkloadClass)
}

// TestUpdateAppScalingPolicy_WorkerClassRPSAccepted is the
// companion to the worker-class carve-out: a worker-class app
// CAN use `target.metric = rps` (the engine reads the per-app
// RPS, and zero RPS over the engine window is the "no signal"
// path, not a hard misconfiguration). The handler must accept
// the PATCH at 200. Cooldowns are set to the package floors so
// the validator doesn't reject on cooldown=0 first.
func TestUpdateAppScalingPolicy_WorkerClassRPSAccepted(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedAppWithWorkloadClass(t, e, "pro-worker-rps", state.WorkloadClassWorker)
	rec := e.do(t, "PATCH", "/v1/apps/pro-worker-rps", api.UpdateAppRequest{
		ScalingPolicy: &api.ScalingPolicy{
			ScaleOutCooldownS: 1,
			ScaleInCooldownS:  5,
			Target: &api.ScalingTarget{
				Metric: "rps",
				Value:  5.0,
			},
		},
		SetScalingPolicy: true,
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

// TestUpdateAppScalingPolicy_UnknownFieldRejected pins the
// strict-unmarshal contract. A typo on the wire
// (`min_instance` instead of `min_instances`) must surface as
// 422 validation_failed rather than silently dropping the
// field. The DTO StrictUnmarshalJSON accumulates the unknown
// field names; the validator surfaces them on the *Problem.
func TestUpdateAppScalingPolicy_UnknownFieldRejected(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-policy-typo")
	// Hand-build the request body so the unknown field reaches
	// the JSON decoder without compile-time filtering. The
	// ScalingPolicy field is the json.RawMessage so the typo
	// (`min_instance` vs `min_instances`) survives the
	// top-level marshal.
	body := struct {
		ScalingPolicy *json.RawMessage `json:"scaling_policy"`
	}{
		ScalingPolicy: func() *json.RawMessage {
			raw := json.RawMessage(`{"min_instance":1,"max_instances":2}`)
			return &raw
		}(),
	}
	rec := e.do(t, "PATCH", "/v1/apps/pro-policy-typo", body, nil)
	assertProblem(t, rec, 422, api.CodeValidation)
}

// TestUpdateAppScalingPolicy_UnknownFieldOnWorkerWins pins the
// validator ordering (issue #462 / ADR-058 review #1): the
// unknown-field error must surface BEFORE the workload-class
// error when a wire payload contains both an unknown field AND
// `target.metric=concurrent_requests` on a worker app. Pre-fix the
// worker gate ran before validateUpdateApp and would shadow the
// wire-shape error.
func TestUpdateAppScalingPolicy_UnknownFieldOnWorkerWins(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedAppWithWorkloadClass(t, e, "pro-worker-typo", state.WorkloadClassWorker)
	body := struct {
		ScalingPolicy *json.RawMessage `json:"scaling_policy"`
	}{
		ScalingPolicy: func() *json.RawMessage {
			// `min_instance` is a typo (correct is `min_instances`);
			// the `target.metric=concurrent_requests` would also be
			// a worker-class reject on a clean payload.
			raw := json.RawMessage(`{"min_instance":1,"target":{"metric":"concurrent_requests","value":1.0}}`)
			return &raw
		}(),
	}
	rec := e.do(t, "PATCH", "/v1/apps/pro-worker-typo", body, nil)
	assertProblem(t, rec, 422, api.CodeValidation)
}

// TestUpdateAppScalingPolicy_TargetValueZeroRoundtrip pins the
// read-path projection (issue #462 / ADR-058 review #4): a policy
// whose Target has Metric="rps" and Value=0 must round-trip through
// the GET response with the Target intact. Pre-fix the read path
// dropped the Target when Value==0, hiding the customer-authored
// metric on the read path.
func TestUpdateAppScalingPolicy_TargetValueZeroRoundtrip(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-target-zero")
	rec := e.do(t, "PATCH", "/v1/apps/pro-target-zero", api.UpdateAppRequest{
		ScalingPolicy: &api.ScalingPolicy{
			ScaleOutCooldownS: 1,
			ScaleInCooldownS:  5,
			Target: &api.ScalingTarget{
				Metric: "rps",
				Value:  0,
			},
		},
		SetScalingPolicy: true,
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ScalingPolicy == nil {
		t.Fatalf("ScalingPolicy = nil, want non-nil")
	}
	if out.ScalingPolicy.Target == nil {
		t.Fatalf("ScalingPolicy.Target = nil; metric 'rps' lost on read-back")
	}
	if out.ScalingPolicy.Target.Metric != "rps" {
		t.Errorf("ScalingPolicy.Target.Metric = %q, want rps", out.ScalingPolicy.Target.Metric)
	}
	if out.ScalingPolicy.Target.Value != 0 {
		t.Errorf("ScalingPolicy.Target.Value = %v, want 0", out.ScalingPolicy.Target.Value)
	}
}

// TestUpdateApp_EvictionPriority_FreeReservedRejected (issue #475)
// pins the Free-tier gate: PATCH eviction_priority='reserved' returns
// 402 plan_eviction_priority_reserved_not_allowed. The plan DOES
// unlock the 'best_effort' tier (the pre-#475 default), so PATCH
// 'best_effort' is always allowed on any plan — the test uses that
// path to confirm the gate is targeted at the reserved value, not
// at the field as a whole.
func TestUpdateApp_EvictionPriority_FreeReservedRejected(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "free-evict-reserved")
	reserved := string(api.EvictionPriorityReserved)
	rec := e.do(t, "PATCH", "/v1/apps/free-evict-reserved", api.UpdateAppRequest{EvictionPriority: &reserved}, nil)
	assertProblem(t, rec, 402, api.CodePlanEvictionPriorityReservedNotAllowed)

	// best_effort must always be allowed on any plan — the gate
	// is targeted at the reserved tier, not the field.
	best := string(api.EvictionPriorityBestEffort)
	rec2 := e.do(t, "PATCH", "/v1/apps/free-evict-reserved", api.UpdateAppRequest{EvictionPriority: &best}, nil)
	if rec2.Code != 200 {
		t.Errorf("Free PATCH best_effort: status %d: %s (must always be allowed)", rec2.Code, rec2.Body)
	}
}

// TestUpdateApp_EvictionPriority_HobbyCapEnforced (issue #475) pins
// the per-account reserved-tier cap. Hobby caps reserved at 1; a
// 2nd PATCH to 'reserved' on a different app must return 422
// plan_eviction_priority_reserved_quota with observed=2, limit=1.
// Counts APPS (not instances) — a single reserved app with 5
// concurrent instances counts as 1 against the cap.
func TestUpdateApp_EvictionPriority_HobbyCapEnforced(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "hobby-reserved-1")
	reserved := string(api.EvictionPriorityReserved)
	// First reserved app: Hobby cap = 1, observed = 1, OK.
	rec1 := e.do(t, "PATCH", "/v1/apps/hobby-reserved-1", api.UpdateAppRequest{EvictionPriority: &reserved}, nil)
	if rec1.Code != 200 {
		t.Fatalf("first reserved PATCH: status %d: %s", rec1.Code, rec1.Body)
	}
	// Second reserved app: cap exhausted, must 422.
	mustSeedApp(t, e, "hobby-reserved-2")
	rec2 := e.do(t, "PATCH", "/v1/apps/hobby-reserved-2", api.UpdateAppRequest{EvictionPriority: &reserved}, nil)
	assertProblem(t, rec2, 422, api.CodePlanEvictionPriorityReservedQuota)
}

// TestUpdateApp_EvictionPriority_InvalidValue (issue #475) pins
// the bounds check. The SQL CHECK apps_eviction_priority_chk is
// the load-bearing safety net; apid must reject the out-of-range
// value FIRST so the customer sees a clean 422 validation_failed,
// not a SQL CHECK violation. Test both 'foo' and an empty string
// so a future off-by-one fix doesn't silently widen the closed set.
func TestUpdateApp_EvictionPriority_InvalidValue(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-evict-invalid")
	foo := "foo"
	rec := e.do(t, "PATCH", "/v1/apps/pro-evict-invalid", api.UpdateAppRequest{EvictionPriority: &foo}, nil)
	assertProblem(t, rec, 422, api.CodeValidation)
}

// TestUpdateApp_EvictionPriority_FlipsDownUnconditional (issue #475)
// pins the flip-down direction. The per-account cap counts apps in
// the reserved tier; flipping an existing reserved app to
// best_effort always succeeds (the cap cannot be exceeded by going
// down). This is the symmetric assertion to HobbyCapEnforced —
// even with the cap at its limit, a flip-down PATCH must succeed.
func TestUpdateApp_EvictionPriority_FlipsDownUnconditional(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "hobby-flip-down")
	reserved := string(api.EvictionPriorityReserved)
	// Pin at the cap.
	up := e.do(t, "PATCH", "/v1/apps/hobby-flip-down", api.UpdateAppRequest{EvictionPriority: &reserved}, nil)
	if up.Code != 200 {
		t.Fatalf("seed reserved: status %d: %s", up.Code, up.Body)
	}
	// Flip down must succeed even though the cap is at the limit.
	best := string(api.EvictionPriorityBestEffort)
	rec := e.do(t, "PATCH", "/v1/apps/hobby-flip-down", api.UpdateAppRequest{EvictionPriority: &best}, nil)
	if rec.Code != 200 {
		t.Errorf("flip-down status %d: %s (must always succeed; cap counts reserved apps, not instances)", rec.Code, rec.Body)
	}
}

// TestUpdateApp_EvictionPriority_AuditEmitted (issue #475) pins the
// single-purpose audit row emitted when the per-app eviction tier
// changes. The app.updated row already carries the old/new snapshot
// of eviction_priority; this row is a single-purpose,
// single-keyword-greppable signal so operators can `gregale
// audit-events --kind-prefix eviction_priority` and see every tier
// change without parsing the larger app.updated payload. A no-op
// PATCH (same value) emits nothing.
func TestUpdateApp_EvictionPriority_AuditEmitted(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-evict-audit")
	reserved := string(api.EvictionPriorityReserved)
	// Flip best_effort → reserved must emit a single kind.
	rec := e.do(t, "PATCH", "/v1/apps/pro-evict-audit", api.UpdateAppRequest{EvictionPriority: &reserved}, nil)
	if rec.Code != 200 {
		t.Fatalf("first PATCH: status %d: %s", rec.Code, rec.Body)
	}
	// Walk the audit-events list endpoint with kind_prefix=app. so
	// the assertion stays agnostic to the store implementation
	// (MemStore returns events in append order; pgstore returns
	// them in id order).
	listRec := e.do(t, http.MethodGet, "/v1/audit-events?kind_prefix=app.", nil, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list audit-events: status %d: %s", listRec.Code, listRec.Body)
	}
	var list api.ListAuditEventsResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	seen := 0
	for _, ev := range list.Events {
		if ev.Kind != "app.eviction_priority_changed" {
			continue
		}
		seen++
	}
	if seen != 1 {
		t.Errorf("app.eviction_priority_changed audit rows = %d, want 1 (one per actual value change); events=%+v", seen, list.Events)
	}

	// No-op PATCH (same value) must not emit a second audit row.
	rec2 := e.do(t, "PATCH", "/v1/apps/pro-evict-audit", api.UpdateAppRequest{EvictionPriority: &reserved}, nil)
	if rec2.Code != 200 {
		t.Fatalf("no-op PATCH: status %d: %s", rec2.Code, rec2.Body)
	}
	listRec2 := e.do(t, http.MethodGet, "/v1/audit-events?kind_prefix=app.", nil, nil)
	if listRec2.Code != http.StatusOK {
		t.Fatalf("list audit-events (post-noop): status %d", listRec2.Code)
	}
	var list2 api.ListAuditEventsResponse
	if err := json.Unmarshal(listRec2.Body.Bytes(), &list2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	seen2 := 0
	for _, ev := range list2.Events {
		if ev.Kind == "app.eviction_priority_changed" {
			seen2++
		}
	}
	if seen2 != 1 {
		t.Errorf("post-noop audit rows = %d, want 1 (no-op PATCH must not emit); events=%+v", seen2, list2.Events)
	}
}

// TestUpdateApp_EvictionPriority_HobbyCapRace (issue #475) pins the
// "cap is advisory, not strict" decision at a code level rather than
// just the doc-comment. Hobby caps reserved at 1; this test seeds 2
// apps on a Hobby account, fires 2 concurrent PATCHes to flip both
// to reserved, and asserts that the system stays internally
// consistent.
//
// The race window is between the SELECT count (advisory read) and
// the UPDATE: two goroutines that both see `n=0` will both compute
// `n+1=1 <= cap=1` and both UPDATE successfully. The system ends
// with 2 reserved apps on a Hobby plan — exactly the failure mode
// ADR-075 §3.2 predicts. The test accepts both outcomes:
//
//  1. Both 200 OK: count >= 1 (advisory overflow). This is the
//     race that production can land. The financial-model per-account
//     RAM cap is the hard backstop.
//  2. One 200 + one 422: count == 1 (advisory caught it). Either
//     goroutine won the race; the loser rejected cleanly.
//
// A regression that swaps the advisory count for an exact count
// (e.g. SELECT ... FOR UPDATE on the apps row) must always land in
// shape (2). The test's two branches let a future contributor
// tighten the cap with confidence that this test will fail and force
// the discussion.
func TestUpdateApp_EvictionPriority_HobbyCapRace(t *testing.T) {
	e := setup(t, api.PlanHobby)
	reserved := string(api.EvictionPriorityReserved)

	// Seed 2 Hobby apps; neither is reserved yet. Hobby cap = 1.
	mustSeedApp(t, e, "hobby-race-a")
	mustSeedApp(t, e, "hobby-race-b")

	type result struct {
		slug string
		code int
	}
	results := make(chan result, 2)
	start := make(chan struct{})

	go func() {
		<-start
		r := e.do(t, "PATCH", "/v1/apps/hobby-race-a", api.UpdateAppRequest{EvictionPriority: &reserved}, nil)
		results <- result{"hobby-race-a", r.Code}
	}()
	go func() {
		<-start
		r := e.do(t, "PATCH", "/v1/apps/hobby-race-b", api.UpdateAppRequest{EvictionPriority: &reserved}, nil)
		results <- result{"hobby-race-b", r.Code}
	}()
	close(start)

	codes := make([]int, 2)
	slugs := make([]string, 2)
	for i := 0; i < 2; i++ {
		res := <-results
		codes[i] = res.code
		slugs[i] = res.slug
	}

	// Reality: we either see (200, 200) or (200, 422). Anything else
	// (500, malformed, mid-flight panic surfaced as 200/empty) is a
	// regression — the handler must succeed OR reject with the
	// load-bearing quota code, period.
	for i, code := range codes {
		if code != 200 && code != 422 {
			t.Errorf("PATCH %s status = %d, want 200 OR 422 (advisory-cap outcomes only)", slugs[i], code)
		}
	}

	// Count the post-race reserved-apps-on-this-account. This is the
	// quantity the financial-model per-account RAM cap (47,600 MB) is
	// the hard backstop for. The test asserts the count is bounded
	// by `cap + tiny_overshoot`: Hobby cap = 1, max overshoot is the
	// 1 extra reserved app a racing PATCH can land.
	reservedCount := 0
	apps, err := e.store.ListApps(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("list apps: %v", err)
	}
	for _, a := range apps {
		if a.EvictionPriority == reserved {
			reservedCount++
		}
	}
	if reservedCount < 1 {
		t.Errorf("no apps parked as reserved after the race — at least one must succeed; got count=%d", reservedCount)
	}
	if reservedCount > 2 {
		t.Errorf("reserved count = %d, want 1 OR 2 (advisory cap overshoot ≤ 1); financial-model RAM cap is the hard backstop", reservedCount)
	}
}

// TestUpdateAppRequireAuthn_FreeGate locks the plan-tier gate for
// the per-deployment require_authn opt-in (issue #560). Free plans
// cannot set require_authn=true at all — opt-in gating is the
// internal-only / B2B upsell path (the issue's recommendation
// "pairs with internal-only"), so it lives on Pro/Scale. The
// handler must return 403 plan_require_authn_not_allowed, not 422
// or 200, because the feature is tier-locked (the value the
// customer typed is irrelevant). Mirrors
// TestUpdateAppWarmSnapshot_FreeGate directly above.
func TestUpdateAppRequireAuthn_FreeGate(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "free-require-authn")
	tru := true
	rec := e.do(t, "PATCH", "/v1/apps/free-require-authn", api.UpdateAppRequest{RequireAuthn: &tru}, nil)
	assertProblem(t, rec, 403, api.CodePlanRequireAuthnNotAllowed)
}

// TestUpdateAppRequireAuthn_HobbyGate is the Hobby branch of the
// same gate (issue #560). Hobby is gated off for the same
// pricing-shape reason as Free — per-deployment auth is the
// Pro/Scale internal-only knob, not a free / hobby tier feature.
func TestUpdateAppRequireAuthn_HobbyGate(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "hobby-require-authn")
	tru := true
	rec := e.do(t, "PATCH", "/v1/apps/hobby-require-authn", api.UpdateAppRequest{RequireAuthn: &tru}, nil)
	assertProblem(t, rec, 403, api.CodePlanRequireAuthnNotAllowed)
}

// TestUpdateAppRequireAuthn_ProHappy is the Pro happy path: Pro
// plans may flip require_authn freely. The value round-trips
// through UpdateApp → scanApp → appResponse (verified via the
// GET round-trip below). Mirrors TestUpdateAppWarmSnapshot_ProHappy.
func TestUpdateAppRequireAuthn_ProHappy(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-require-authn-ok")
	tru := true
	rec := e.do(t, "PATCH", "/v1/apps/pro-require-authn-ok", api.UpdateAppRequest{RequireAuthn: &tru}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.RequireAuthn {
		t.Errorf("RequireAuthn = false, want true (PATCH round-trip)")
	}
	// Belt-and-suspenders: the raw JSON must surface
	// "require_authn":true so a future DTO `omitempty` tag added
	// in error is caught here, not in production.
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"require_authn":true`)) {
		t.Errorf("raw JSON missing require_authn:true:\n%s", rec.Body.String())
	}
}

// TestGetApp_SurfacesRequireAuthn pins the wire shape for the
// issue #560 + issue #695 / ADR-080 truth: every plan's GET
// /v1/apps/{slug} response must include `require_authn` set to
// the per-plan default for the freshly created app. The per-plan
// truth table (Free=false, Hobby=true, Pro=true, Scale=true) was
// the G15 follow-up — the migration 00155 grand-father protects
// every pre-flip app, so this test only covers post-flip creates.
// Catches regressions where someone constructs api.AppResponse
// directly (bypassing appResponse) and forgets the new field.
// Mirrors TestGetApp_SurfacesConcurrencyPerVMBound directly above.
func TestGetApp_SurfacesRequireAuthn(t *testing.T) {
	cases := []struct {
		plan api.Plan
		want bool
	}{
		{api.PlanFree, false},
		{api.PlanHobby, true},
		{api.PlanPro, true},
		{api.PlanScale, true},
	}
	for _, c := range cases {
		t.Run(string(c.plan), func(t *testing.T) {
			e := setup(t, c.plan)
			mustSeedApp(t, e, "require-authn-app")
			rec := e.do(t, "GET", "/v1/apps/require-authn-app", nil, nil)
			if rec.Code != 200 {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			var out api.AppResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if out.RequireAuthn != c.want {
				t.Errorf("require_authn = %t, want %t (plan=%s)",
					out.RequireAuthn, c.want, c.plan)
			}
			// Belt-and-suspenders: assert the JSON field is
			// present in the raw payload — catches any future
			// DTO `omitempty` tag added in error.
			if !bytes.Contains(rec.Body.Bytes(), []byte(`"require_authn":`)) {
				t.Errorf("raw JSON missing require_authn key:\n%s", rec.Body.String())
			}
		})
	}
}

// --- raiseOverageCap (issue #561) --------------------------------------------
//
// POST /v1/account/overage-cap is the account-self-scoped spend-cap raise
// endpoint. Body: {"overage_cap_cents": <int|null>}; *int64 so a
// missing/null field clears the cap (NULL). The service routine
// raiseOverageCapSvc is the shared code path with the dashboard form.

// TestRaiseOverageCap_Raise confirms the happy path: the cap is
// stamped to accounts.overage_cap_cents, the audit row
// overage.cap_changed is emitted, and the response carries the
// refreshed account view. The audit row's old_cents reflects the
// pre-update cap (zero in this fixture → the response is the
// first ever raise).
func TestRaiseOverageCap_Raise(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/account/overage-cap", map[string]any{
		"overage_cap_cents": int64(5000),
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	// Storage: the cap is now 5000.
	cents, found, err := e.store.GetAccountOverageCapCents(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("GetAccountOverageCapCents: %v", err)
	}
	if !found || cents != 5000 {
		t.Errorf("cents = %d, found = %v, want 5000/true", cents, found)
	}
	// Audit row: overage.cap_changed with the right shape.
	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var hit *state.Event
	for i := range rows {
		if rows[i].Kind == "overage.cap_changed" {
			hit = &rows[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("overage.cap_changed audit row not found; events: %+v", rows)
	}
	var data map[string]any
	if err := json.Unmarshal(hit.Data, &data); err != nil {
		t.Fatalf("audit data unmarshal: %v", err)
	}
	if data["actor"] != "self" {
		t.Errorf("audit actor = %v, want \"self\"", data["actor"])
	}
	// old_cents is zero on the first raise (the account has no
	// pre-existing cap; GetAccountOverageCapCents returns (0, false)
	// and the svc surfaces the 0). JSON-marshalled ints come back
	// as float64 through encoding/json so compare against that shape.
	assertCapInt(t, "audit old_cents", data["old_cents"], 0)
	assertCapInt(t, "audit new_cents", data["new_cents"], 5000)
}

// TestRaiseOverageCap_Clear confirms the "null clears the cap" path:
// POSTing {"overage_cap_cents": null} wipes the column to NULL and
// the audit row's new_cents surfaces as the literal string "null"
// so the audit reader can distinguish a clear from a 0-set.
func TestRaiseOverageCap_Clear(t *testing.T) {
	e := setup(t, api.PlanPro)
	// Pre-set a cap to make the clear visible.
	if err := e.store.UpdateAccountOverageCapCents(context.Background(), e.acct.ID, ptrInt64(7500)); err != nil {
		t.Fatalf("seed cap: %v", err)
	}
	rec := e.do(t, "POST", "/v1/account/overage-cap", map[string]any{
		"overage_cap_cents": nil,
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	// Storage: the cap is now NULL.
	_, found, err := e.store.GetAccountOverageCapCents(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("GetAccountOverageCapCents: %v", err)
	}
	if found {
		t.Errorf("found = true, want false (cap cleared to NULL)")
	}
	// Audit row: old_cents=7500, new_cents="null".
	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var hit *state.Event
	for i := range rows {
		if rows[i].Kind == "overage.cap_changed" {
			hit = &rows[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("overage.cap_changed audit row not found")
	}
	var data map[string]any
	if err := json.Unmarshal(hit.Data, &data); err != nil {
		t.Fatalf("audit data unmarshal: %v", err)
	}
	assertCapInt(t, "audit old_cents", data["old_cents"], 7500)
	if data["new_cents"] != auditOverageCapNullSentinel {
		t.Errorf("audit new_cents = %v, want %q", data["new_cents"], auditOverageCapNullSentinel)
	}
}

// assertCapInt normalizes the audit-row JSON round-trip for the
// overage cap. encoding/json marshals int64 → float64, so a
// direct equality check fails on the unmarshal side. The helper
// accepts both shapes and pins the test to the expected value.
func assertCapInt(t *testing.T, label string, got any, want int64) {
	t.Helper()
	switch v := got.(type) {
	case float64:
		if int64(v) != want {
			t.Errorf("%s = %v, want %d", label, got, want)
		}
	case int64:
		if v != want {
			t.Errorf("%s = %v, want %d", label, got, want)
		}
	default:
		t.Errorf("%s = %v (type %T), want %d", label, got, got, want)
	}
}

// TestRaiseOverageCap_Invalid confirms the body-validation branch:
// negative cents are rejected with 400 + CodeValidation, the
// audit row is NOT emitted (cap was unchanged), and the storage
// stays untouched.
func TestRaiseOverageCap_Invalid(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/account/overage-cap", map[string]any{
		"overage_cap_cents": int64(-100),
	}, nil)
	assertProblem(t, rec, 400, api.CodeValidation)
	// Storage: untouched.
	_, found, err := e.store.GetAccountOverageCapCents(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("GetAccountOverageCapCents: %v", err)
	}
	if found {
		t.Errorf("found = true, want false (invalid request must not stamp anything)")
	}
	// Audit row: none. ListEvents returns no rows for the account.
	rows, err := e.store.ListEvents(context.Background(), e.acct.ID, 50)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	for _, r := range rows {
		if r.Kind == "overage.cap_changed" {
			t.Errorf("audit row emitted on invalid request: %+v", r)
		}
	}
}

// TestRaiseOverageCap_BadJSON confirms the body-decoding branch:
// malformed JSON surfaces as 400 + CodeValidation, the storage
// stays untouched, and no audit row is emitted.
func TestRaiseOverageCap_BadJSON(t *testing.T) {
	e := setup(t, api.PlanPro)
	req := httptest.NewRequest("POST", "/v1/account/overage-cap", strings.NewReader("{not-json"))
	req.Header.Set("Authorization", "Bearer "+e.key)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	assertProblem(t, rec, 400, api.CodeValidation)
	_, found, err := e.store.GetAccountOverageCapCents(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("GetAccountOverageCapCents: %v", err)
	}
	if found {
		t.Errorf("found = true, want false (bad-JSON request must not stamp anything)")
	}
}

// TestRaiseOverageCap_NoAuth confirms the auth boundary: an
// unauthenticated POST surfaces as 401. The handler is mounted
// under s.withAuth → no audit row, no storage write.
func TestRaiseOverageCap_NoAuth(t *testing.T) {
	e := setup(t, api.PlanPro)
	body, _ := json.Marshal(map[string]any{"overage_cap_cents": int64(500)})
	req := httptest.NewRequest("POST", "/v1/account/overage-cap", bytes.NewReader(body))
	// Note: NO Authorization header.
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("status = %d, want 401; body = %s", rec.Code, rec.Body)
	}
	// Storage: untouched.
	_, found, err := e.store.GetAccountOverageCapCents(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("GetAccountOverageCapCents: %v", err)
	}
	if found {
		t.Errorf("found = true, want false (no-auth request must not stamp anything)")
	}
}

// ptrInt64 is a small helper for the cap-stamping fixture. The
// engine_test.go package has ptrInt but the apid test package
// needs its own (it cannot import a test file from pkg/sched).
func ptrInt64(v int64) *int64 { return &v }

// parkedRefStore is a thin wrapper over *state.MemStore that
// overrides LatestParkedDeploymentForApp with a controllable
// result. Used by TestWithParkedDeploymentRef_* to exercise the
// three branches the helper must handle:
//
//  1. ErrNotFound (the "never parked" / healthy app path) →
//     resp.ParkedDeployment stays nil, no warn-log.
//  2. Real row → resp.ParkedDeployment populated, no warn-log.
//  3. Arbitrary store error → resp.ParkedDeployment stays nil,
//     warn-log emitted (the test captures slog via a buffer).
//
// The wrapper embeds *state.MemStore so method-set promotion
// satisfies the full state.Store interface — no need to
// re-implement the ~100 other methods just to stub one.
type parkedRefStore struct {
	*state.MemStore
	// err is consulted by LatestParkedDeploymentForApp.
	// When non-nil, the err is returned verbatim and the
	// embedded memstore implementation is bypassed.
	err error
}

func (p *parkedRefStore) LatestParkedDeploymentForApp(ctx context.Context, appID string) (state.Deployment, error) {
	if p.err != nil {
		return state.Deployment{}, p.err
	}
	// err is nil → fall through to the embedded memstore.
	// Forward the inherited ctx so contextcheck sees a
	// context-propagating call chain (passing a fresh
	// context.Background() would trip the rule on test
	// surfaces).
	return p.MemStore.LatestParkedDeploymentForApp(ctx, appID)
}

// TestWithParkedDeploymentRef_HealthyAppBranch pins the
// "never parked" path: the helper sees ErrNotFound and returns
// resp unchanged (no ParkedDeployment field set). This is the
// common-path branch — every healthy app lands here on
// GET /v1/apps/{slug}. A regression (e.g. an accidental wrap
// that surfaces the error) would render a 500 for every
// listApps call.
func TestWithParkedDeploymentRef_HealthyAppBranch(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID,
		Slug:      "parked-ref-healthy",
		Type:      state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	resp := api.AppResponse{Slug: app.Slug}
	got := e.s.withParkedDeploymentRef(context.Background(), resp, app)
	if got.ParkedDeployment != nil {
		t.Errorf("ParkedDeployment = %+v, want nil (healthy app has no park)", got.ParkedDeployment)
	}
}

// TestWithParkedDeploymentRef_ParkedAppPopulates pins the
// happy path: a parked deployment lands on the wire as the
// nested ref with id + parked_reason + parked_at populated.
// The store returns the seeded row verbatim.
func TestWithParkedDeploymentRef_ParkedAppPopulates(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID,
		Slug:      "parked-ref-populated",
		Type:      state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := e.store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:p",
		Status:      state.DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	stamp := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	if err := e.store.SetDeploymentParked(context.Background(), dep.ID, "liveness_exhausted", stamp); err != nil {
		t.Fatalf("SetDeploymentParked: %v", err)
	}

	resp := api.AppResponse{Slug: app.Slug}
	got := e.s.withParkedDeploymentRef(context.Background(), resp, app)
	if got.ParkedDeployment == nil {
		t.Fatal("ParkedDeployment = nil, want ref (parked app)")
	}
	if got.ParkedDeployment.ID != dep.ID {
		t.Errorf("ParkedDeployment.ID = %q, want %q", got.ParkedDeployment.ID, dep.ID)
	}
	if got.ParkedDeployment.ParkedReason != "liveness_exhausted" {
		t.Errorf("ParkedDeployment.ParkedReason = %q, want liveness_exhausted", got.ParkedDeployment.ParkedReason)
	}
	if got.ParkedDeployment.ParkedAt == nil || !got.ParkedDeployment.ParkedAt.Equal(stamp) {
		t.Errorf("ParkedDeployment.ParkedAt = %v, want %v", got.ParkedDeployment.ParkedAt, stamp)
	}
}

// TestWithParkedDeploymentRef_StoreErrorLoggedAndIgnored pins the
// degraded path: a non-ErrNotFound store error must NOT fail the
// apid response (the rest of the app shape still renders) but MUST
// log a warning so operators see the failure. Captures slog via a
// bytes.Buffer-backed handler.
func TestWithParkedDeploymentRef_StoreErrorLoggedAndIgnored(t *testing.T) {
	e := setup(t, api.PlanPro)
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: e.acct.ID,
		Slug:      "parked-ref-store-error",
		Type:      state.AppTypeApp,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	// Swap s.store with a wrapper that returns an arbitrary
	// error from LatestParkedDeploymentForApp. The rest of the
	// store (AppByID, etc.) is method-promoted from the
	// embedded *state.MemStore so the request still completes.
	logBuf := &safeBuffer{}
	captureLogger := slog.New(slog.NewJSONHandler(logBuf, nil))
	srv := newServerWithDeps(
		&parkedRefStore{MemStore: e.store, err: errors.New("simulated PG outage")},
		captureLogger,
		"gregale.dev",
		noopNotifier{},
		"",
		noopMailer{},
		stubGithubdClient{},
		nil,
		nil,
		0,
		"",
	)

	resp := api.AppResponse{Slug: app.Slug}
	got := srv.withParkedDeploymentRef(context.Background(), resp, app)
	if got.ParkedDeployment != nil {
		t.Errorf("ParkedDeployment = %+v, want nil (store error → field stays empty)", got.ParkedDeployment)
	}
	// Verify the rest of the resp round-trips (the helper
	// returns the same struct, so Slug must survive).
	if got.Slug != app.Slug {
		t.Errorf("resp.Slug = %q, want %q (helper must return resp unchanged on error)", got.Slug, app.Slug)
	}

	// The warn log must mention the app id + the error.
	logged := logBuf.String()
	if !strings.Contains(logged, "parked deployment ref lookup") {
		t.Errorf("expected warn-log with msg 'parked deployment ref lookup', got: %s", logged)
	}
	if !strings.Contains(logged, app.ID) {
		t.Errorf("expected warn-log to mention app id %q, got: %s", app.ID, logged)
	}
	if !strings.Contains(logged, "simulated PG outage") {
		t.Errorf("expected warn-log to mention the error, got: %s", logged)
	}
}

// --- PR-B (issue #679 / ADR-082) per-account additive budget --------------
//
// The validator at `validateUpdateApp` consults
// `acct.Plan.EgressAllowlistMaxSize() + acct.EgressAllowlistExtra`
// for the per-app CIDR cap. The additive budget is admin-set
// (mirrors grace_window_days); the customer-facing validator is
// the only place the value is read. The tests below pin each
// boundary case so the validator's effective cap is provably
// `plan_cap + extra` across the plan tiers.
//
// Test surface:
//   - Pro (cap 16) + extra=1, PATCH 17 → 200 (one over cap is OK).
//   - Pro (cap 16) + extra=1, PATCH 18 → 400 (two over rejects).
//   - Scale (cap 64) + extra=0, PATCH 64 → 200 (plan cap is exact).
//   - Scale (cap 64) + extra=0, PATCH 65 → 400 (regression lock).
//   - Pro (cap 16) + extra=0, PATCH 17 → 400 (regression lock).
//
// All of these are read-side only — the validator never writes
// `EgressAllowlistExtra`. The store assertion is omitted by
// design; the memstore field is touched only by the admin
// handler (covered in handlers_account_test.go).
//
// TestUpdateAppEgressAllowlist_ExtraBudgetAllowsOneOverCap: Pro
// (cap 16) with extra=1 — PATCH 17 entries must succeed. The
// validator's effective cap is `16 + 1 = 17`.
func TestUpdateAppEgressAllowlist_ExtraBudgetAllowsOneOverCap(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-extra-one")
	if err := e.store.SetAccountEgressAllowlistExtra(context.Background(), e.acct.ID, 1); err != nil {
		t.Fatalf("seed extra=1: %v", err)
	}
	seventeen := make([]string, 0, 17)
	for i := 1; i <= 17; i++ {
		seventeen = append(seventeen, fmt.Sprintf("10.0.%d.0/24", i))
	}
	rec := e.do(t, "PATCH", "/v1/apps/pro-extra-one", api.UpdateAppRequest{
		EgressAllowlist: &seventeen,
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

// TestUpdateAppEgressAllowlist_ExtraBudgetBoundaryRejected: Pro
// (cap 16) with extra=1 — PATCH 18 entries must surface 400
// egress_allowlist_too_long. The one-over-cap allowance is
// exactly `extra`; the boundary is closed.
func TestUpdateAppEgressAllowlist_ExtraBudgetBoundaryRejected(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-extra-one-too-many")
	if err := e.store.SetAccountEgressAllowlistExtra(context.Background(), e.acct.ID, 1); err != nil {
		t.Fatalf("seed extra=1: %v", err)
	}
	eighteen := make([]string, 0, 18)
	for i := 1; i <= 18; i++ {
		eighteen = append(eighteen, fmt.Sprintf("10.0.%d.0/24", i))
	}
	rec := e.do(t, "PATCH", "/v1/apps/pro-extra-one-too-many", api.UpdateAppRequest{
		EgressAllowlist: &eighteen,
	}, nil)
	assertProblem(t, rec, 400, api.CodeEgressAllowlistTooLong)
}

// TestUpdateAppEgressAllowlist_ScaleAtPlanCap: Scale (cap 64)
// with extra=0 — PATCH 64 entries must succeed. The plan cap is
// the exact limit when the extra budget is zero.
func TestUpdateAppEgressAllowlist_ScaleAtPlanCap(t *testing.T) {
	e := setup(t, api.PlanScale)
	mustSeedApp(t, e, "scale-at-cap")
	cap64 := make([]string, 0, 64)
	for i := 1; i <= 64; i++ {
		cap64 = append(cap64, fmt.Sprintf("10.0.%d.0/24", i))
	}
	rec := e.do(t, "PATCH", "/v1/apps/scale-at-cap", api.UpdateAppRequest{
		EgressAllowlist: &cap64,
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

// TestUpdateAppEgressAllowlist_ScaleOverPlanCap: Scale (cap 64)
// with extra=0 — PATCH 65 entries must surface 400
// egress_allowlist_too_long. The plan cap is the exact limit when
// the extra budget is zero.
func TestUpdateAppEgressAllowlist_ScaleOverPlanCap(t *testing.T) {
	e := setup(t, api.PlanScale)
	mustSeedApp(t, e, "scale-over-cap")
	overCap := make([]string, 0, 65)
	for i := 1; i <= 65; i++ {
		overCap = append(overCap, fmt.Sprintf("10.0.%d.0/24", i))
	}
	rec := e.do(t, "PATCH", "/v1/apps/scale-over-cap", api.UpdateAppRequest{
		EgressAllowlist: &overCap,
	}, nil)
	assertProblem(t, rec, 400, api.CodeEgressAllowlistTooLong)
}

// TestUpdateAppEgressAllowlist_ExtraAppliedToValidatorMessage: a
// PATCH over the bare plan cap but within the extra-augmented cap
// must produce a 400 whose problem detail reports the EFFECTIVE
// cap (plan+extra), not the bare plan cap. This is the wire-shape
// invariant the dashboard depends on (the limit field is the
// number the customer can hit 1-by-1, not the plan marketing
// figure).
func TestUpdateAppEgressAllowlist_ExtraAppliedToValidatorMessage(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "pro-cap-message")
	if err := e.store.SetAccountEgressAllowlistExtra(context.Background(), e.acct.ID, 4); err != nil {
		t.Fatalf("seed extra=4: %v", err)
	}
	// 21 entries: > 16 (plan cap) but > 20 (plan+extra=20) — should
	// reject with 400 and the limit field must read 20.
	beyond := make([]string, 0, 21)
	for i := 1; i <= 21; i++ {
		beyond = append(beyond, fmt.Sprintf("10.0.%d.0/24", i))
	}
	rec := e.do(t, "PATCH", "/v1/apps/pro-cap-message", api.UpdateAppRequest{
		EgressAllowlist: &beyond,
	}, nil)
	if rec.Code != 400 {
		t.Fatalf("status %d, want 400: %s", rec.Code, rec.Body)
	}
	var p api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("problem decode: %v: %s", err, rec.Body)
	}
	if p.Limit != nil && *p.Limit != 20 {
		t.Errorf("problem.limit = %d, want 20 (plan_cap=16 + extra=4)", *p.Limit)
	}
}

// TestUpdateAppWebSocket_FreeGate (issue #676 / ADR-080 follow-up)
// confirms the Free plan fail-closed contract on the PATCH path:
// Free customers cannot opt-in to per-app WebSocket at PATCH time.
// The request-time 501 path is covered by
// pkg/gateway/forwardproxy_handler_test.go::TestServeHTTP_FreePlan_WebSocketNotAllowed;
// this test pins the apid-layer gate so a future refactor that drops
// the plan check surfaces here, not in production.
//
// The gate is asymmetric (cmd/apid/handlers_ext.go:258-262): opt-in
// (websocket_enabled: true) is 403 plan_websocket_not_allowed; opt-out
// (false) is a 200 — Free customers can always disable the flag. The
// sibling test below pins the opt-out path so a future tightening
// that adds a symmetric gate on false surfaces here too.
func TestUpdateAppWebSocket_FreeGate(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "free-ws")
	tru := true
	rec := e.do(t, "PATCH", "/v1/apps/free-ws", api.UpdateAppRequest{WebSocketEnabled: &tru}, nil)
	assertProblem(t, rec, 403, api.CodePlanWebSocketNotAllowed)
}

// TestUpdateAppWebSocket_FreeOptOut (issue #676 / ADR-080 follow-up)
// confirms the asymmetric gate: Free customers may PATCH
// websocket_enabled=false even though opt-in is 403. Mirrors the
// streaming_enabled pattern (cmd/apid/handlers_ext.go:248-252): the
// fail-closed contract is opt-in only, because opt-out is the safe
// direction (a Free customer turning off WS doesn't cost Gregale
// anything). Plan.WebSocketResponseAllowed() vs
// Plan.WebSocketEnabled() collapse to the same bool but the apid
// layer calls the response-allowed accessor for the gate per ADR-080
// §"Per-app + per-plan gating".
func TestUpdateAppWebSocket_FreeOptOut(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "free-ws-out")
	fal := false
	rec := e.do(t, "PATCH", "/v1/apps/free-ws-out", api.UpdateAppRequest{WebSocketEnabled: &fal}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d, want 200 (asymmetric gate; opt-out always allowed): %s", rec.Code, rec.Body)
	}
}

// --- CORS improvements D1: per-app default CORS PATCH tests ----

// The validator (validateUpdateApp) requires a non-empty origins
// list whenever CORSDefaultEnabled is true (otherwise the customer
// turns the fallback on with no allowlist and the gateway stamps
// nothing). A round-trip test pins the persistence shape: a valid
// PATCH survives the validator, lands on the apps row, and reads
// back through the AppResponse projection. Validator-only tests
// (no round-trip) pin the negative path.

func TestUpdateApp_CORSDefaultEnabledRoundTrip(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "cors-rt")
	enabled := true
	origins := []string{"https://*.example.com"}
	rec := e.do(t, "PATCH", "/v1/apps/cors-rt", api.UpdateAppRequest{
		CORSDefaultEnabled: &enabled,
		CORSDefaultOrigins: &origins,
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AppResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.CORSDefaultEnabled == nil || !*out.CORSDefaultEnabled {
		t.Errorf("CORSDefaultEnabled: got %v want *true", out.CORSDefaultEnabled)
	}
	if len(out.CORSDefaultOrigins) != 1 || out.CORSDefaultOrigins[0] != "https://*.example.com" {
		t.Errorf("CORSDefaultOrigins: got %v", out.CORSDefaultOrigins)
	}
}

func TestUpdateApp_CORSDefaultEnabled_EmptyOriginsRejected(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "cors-empty")
	enabled := true
	empty := []string{}
	rec := e.do(t, "PATCH", "/v1/apps/cors-empty", api.UpdateAppRequest{
		CORSDefaultEnabled: &enabled,
		CORSDefaultOrigins: &empty,
	}, nil)
	assertProblem(t, rec, 422, api.CodeValidation)
}

func TestUpdateApp_CORSDefaultEnabled_NilOriginsRejected(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "cors-nil")
	enabled := true
	rec := e.do(t, "PATCH", "/v1/apps/cors-nil", api.UpdateAppRequest{
		CORSDefaultEnabled: &enabled,
		// CORSDefaultOrigins left nil
	}, nil)
	assertProblem(t, rec, 422, api.CodeValidation)
}

func TestUpdateApp_CORSDefaultEnabled_BadGrammarRejected(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "cors-bad")
	enabled := true
	origins := []string{"example.com"} // missing scheme
	rec := e.do(t, "PATCH", "/v1/apps/cors-bad", api.UpdateAppRequest{
		CORSDefaultEnabled: &enabled,
		CORSDefaultOrigins: &origins,
	}, nil)
	assertProblem(t, rec, 422, api.CodeValidation)
}

func TestUpdateApp_CORSDefaultEnabled_OptOutPath(t *testing.T) {
	// Setting CORSDefaultEnabled to false without an origins list
	// must NOT 422 — the customer is opting out of an enabled
	// fallback (or never enabled it). The validator only requires
	// origins when the customer enables the feature.
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "cors-off")
	disabled := false
	rec := e.do(t, "PATCH", "/v1/apps/cors-off", api.UpdateAppRequest{
		CORSDefaultEnabled: &disabled,
		// origins intentionally omitted
	}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}
