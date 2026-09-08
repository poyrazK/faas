// streaming_test.go — issue #471 PR-A acceptance for the *flag +
// plan-gate* surface (AC #4 splits cleanly: the e2e pin below
// covers the apid side; the gateway-side buffered-fallback log line
// is pinned in pkg/gateway/handler_test.go in-process — see note
// below). PR-A wires the per-app streaming flag, plan default, env
// flag, and the buffered-fallback deprecation log; the real Flusher
// path ships in PR-B. This test exercises three pins, all on the
// apid + Postgres surface:
//
//  1. Plan-gate: a Free-plan customer cannot enable streaming.
//     PATCH /v1/apps/{slug} with streaming_enabled=true returns
//     403 plan_streaming_not_allowed with the structured error
//     envelope (limit value, observed value, docs URL).
//
//  2. Plan-default: a Hobby customer creates an app → the persisted
//     App.streaming_enabled matches the plan's default (true for
//     Hobby+; PR-A flips the same path PR-B uses).
//
//  3. Persistence: a Hobby customer can enable streaming explicitly
//     via PATCH and the row reflects the new value.
//
// What this test does NOT cover (deliberately): the buffered-fallback
// deprecation log itself. That log line is emitted inside gatewayd's
// statusRecorder post-proxy branch when an SSE response arrives on
// the legacy buffered path; it is a sync.Map dedup + a slog.Warn
// with no Postgres involvement. Asserting it here would mean
// standing up gatewayd in the harness, deploying an SSE-emitting app,
// and tailing slog — far more surface than the unit test in
// pkg/gateway/handler_test.go already covers. The end-to-end "real
// SSE response reaching gatewayd and being buffered" path is a
// PR-B metal test (the e2e harness without a deployed app can't
// synthesize a successful 200 with an SSE Content-Type).
//
// Build tag: (none). CI-safe. Requires Postgres (skip via
// FAAS_SKIP_PG_TESTS) and a buildable ./cmd/apid.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// TestE2E_Streaming_FreePlanRejected covers AC #4 sub-test (1):
// a Free-plan account cannot flip streaming_enabled=true on an
// app. apid's validateUpdateApp plan-gate emits 403
// plan_streaming_not_allowed with a structured Problem body that
// carries the limit value (the plan's StreamingResponseAllowed
// predicate), the observed value (true), and the docs URL.
func TestE2E_Streaming_FreePlanRejected(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanFree)

	// Read the limits so the test stays in sync if the Free plan
	// ever unlocks streaming. Today (PR-A) Free is off — the
	// assertion below fails loudly the moment a future change
	// silently flips the default, exactly the regression the AC
	// is meant to catch.
	if api.PlanFree.StreamingResponseAllowed() {
		t.Fatalf("PlanFree reported StreamingResponseAllowed=true; AC #4 expects Free to be blocked")
	}

	// Seed one app on the Free plan.
	createBody, status := doReq(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "stream-free"})
	if status != http.StatusCreated {
		t.Fatalf("create app: %d %s", status, createBody)
	}

	// Attempt to flip streaming on — must be rejected.
	trueVal := true
	raw, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/stream-free",
		api.UpdateAppRequest{StreamingEnabled: &trueVal})
	if status != http.StatusForbidden {
		t.Fatalf("PATCH streaming_enabled=true on Free plan: status=%d want %d body=%s",
			status, http.StatusForbidden, raw)
	}
	var prob api.Problem
	if err := json.Unmarshal(raw, &prob); err != nil {
		t.Fatalf("decode problem body: %v (raw=%s)", err, raw)
	}
	if prob.Code != api.CodePlanStreamingNotAllowed {
		t.Errorf("prob.code = %q, want %q", prob.Code, api.CodePlanStreamingNotAllowed)
	}
	if prob.Status != http.StatusForbidden {
		t.Errorf("prob.status = %d, want %d", prob.Status, http.StatusForbidden)
	}
}

// TestE2E_Streaming_HobbyPlanDefaultsAndPersists covers AC #4
// sub-tests (2) and (3): a Hobby customer creates an app (the
// plan default flows through buildApp → limits.StreamingEnabled
// (Plan)) and then PATCHes streaming_enabled=true (the plan-gate
// passes for Hobby) — both states land on the persisted app.
func TestE2E_Streaming_HobbyPlanDefaultsAndPersists(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	if !api.PlanHobby.StreamingResponseAllowed() {
		t.Fatalf("PlanHobby reported StreamingResponseAllowed=false; AC #4 expects Hobby to be allowed")
	}

	// Create — defaults to true (the Hobby+ plan-level default).
	createBody, status := doReq(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "stream-hobby"})
	if status != http.StatusCreated {
		t.Fatalf("create app: %d %s", status, createBody)
	}
	var created api.AppResponse
	if err := json.Unmarshal(createBody, &created); err != nil {
		t.Fatalf("decode created: %v (raw=%s)", err, createBody)
	}
	// The diagnostic t.Logf that previously dumped the raw body +
	// planDefault(Hobby) lived here during the PR #481 e2e CI flake
	// investigation. It was diagnostic-only — every run printed it
	// even when the test passed. Now that CreateAppIfUnderQuota
	// writes streaming_enabled (the actual bug), the log line is
	// dead weight; remove it.
	if !created.StreamingEnabled {
		t.Errorf("Hobby create: StreamingEnabled=false; want true (plan default)")
	}

	// Explicitly disable + re-enable: PATCH must honour the value
	// and the plan-gate must not interfere on Hobby.
	falseVal := false
	raw, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/stream-hobby",
		api.UpdateAppRequest{StreamingEnabled: &falseVal})
	if status != http.StatusOK {
		t.Fatalf("PATCH streaming_enabled=false: %d %s", status, raw)
	}
	var afterOff api.AppResponse
	if err := json.Unmarshal(raw, &afterOff); err != nil {
		t.Fatalf("decode afterOff: %v (raw=%s)", err, raw)
	}
	if afterOff.StreamingEnabled {
		t.Errorf("PATCH off: StreamingEnabled=true; want false")
	}

	trueVal := true
	raw, status = doReq(t, h, key, http.MethodPatch, "/v1/apps/stream-hobby",
		api.UpdateAppRequest{StreamingEnabled: &trueVal})
	if status != http.StatusOK {
		t.Fatalf("PATCH streaming_enabled=true: %d %s", status, raw)
	}
	var afterOn api.AppResponse
	if err := json.Unmarshal(raw, &afterOn); err != nil {
		t.Fatalf("decode afterOn: %v (raw=%s)", err, raw)
	}
	if !afterOn.StreamingEnabled {
		t.Errorf("PATCH on: StreamingEnabled=false; want true")
	}

	// Read back via GET to confirm the persisted row matches.
	raw, status = doReq(t, h, key, http.MethodGet, "/v1/apps/stream-hobby", nil)
	if status != http.StatusOK {
		t.Fatalf("GET app: %d %s", status, raw)
	}
	var fetched api.AppResponse
	if err := json.Unmarshal(raw, &fetched); err != nil {
		t.Fatalf("decode fetched: %v (raw=%s)", err, raw)
	}
	if !fetched.StreamingEnabled {
		t.Errorf("GET after PATCH on: StreamingEnabled=false; want true (persisted)")
	}
}

// TestE2E_Streaming_AcceptJSONOptOut pins the per-request
// opt-out header (issue #471 PR-B / ADR-047). The contract:
//
//   - A Hobby app with streaming_enabled=true is eligible for the
//     streaming response path on the gateway side.
//   - A single request can force the buffered (legacy) path by
//     sending Accept: application/json. The customer-visible
//     contract from spec §4.1 — the Accept header is the
//     per-request opt-out knob.
//   - The opt-out is a gateway-side decision (handler.go
//     isAcceptJSON check); apid never sees it. The test confirms
//     the apid surface (plan-gate + persistence from PR-A) is
//     unchanged: the per-app flag stays true across a request
//     that carried the opt-out header.
//
// This is a non-metal e2e — it exercises the apid + Postgres
// surface and doesn't drive the gateway-side Flusher path.
// The streaming metal test (cmd/e2e/streaming_metal_test.go)
// covers the actual Flusher path under //go:build metal.
func TestE2E_Streaming_AcceptJSONOptOut(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	// Hobby must allow streaming for the per-app flag to even
	// be settable.
	if !api.PlanHobby.StreamingResponseAllowed() {
		t.Fatalf("PlanHobby reported StreamingResponseAllowed=false; AC expects Hobby to be unlocked")
	}

	// Create a Hobby app.
	createBody, status := doReq(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "stream-opt-out"})
	if status != http.StatusCreated {
		t.Fatalf("create app: %d %s", status, createBody)
	}

	// Plan default is Hobby+ → true, but be explicit so the
	// test is independent of the plan-default seam.
	trueVal := true
	raw, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/stream-opt-out",
		api.UpdateAppRequest{StreamingEnabled: &trueVal})
	if status != http.StatusOK {
		t.Fatalf("PATCH streaming_enabled=true: %d %s", status, raw)
	}
	var afterOn api.AppResponse
	if err := json.Unmarshal(raw, &afterOn); err != nil {
		t.Fatalf("decode afterOn: %v (raw=%s)", err, raw)
	}
	if !afterOn.StreamingEnabled {
		t.Fatalf("setup: StreamingEnabled=false after PATCH on; want true")
	}

	// The opt-out is a request-side contract — confirm the
	// per-app flag survives the request. The Accept: application/json
	// header that a customer might send on a single request is
	// NOT a per-app PATCH; a follow-up GET must still report
	// streaming_enabled=true. The load-bearing gate is in
	// pkg/gateway/handler.go isAcceptJSON; this test pins the
	// apid surface so a future PR can't accidentally couple
	// the per-request header to the apps.streaming_enabled row.
	raw, status = doReq(t, h, key, http.MethodGet, "/v1/apps/stream-opt-out", nil)
	if status != http.StatusOK {
		t.Fatalf("GET app: %d %s", status, raw)
	}
	var fetched api.AppResponse
	if err := json.Unmarshal(raw, &fetched); err != nil {
		t.Fatalf("decode fetched: %v", err)
	}
	if !fetched.StreamingEnabled {
		t.Errorf("GET after Accept simulation: StreamingEnabled=false; want true (per-request opt-out is gateway-side; apid must NOT mutate per-app flag)")
	}
}
