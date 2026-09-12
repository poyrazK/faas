// invocations_e2e_test.go — M7 §14 acceptance: the customer-facing
// async-invoke + queues path through real HTTP and a real schedd
// subprocess. Closes the gap called out in e2e-gap-analysis-m7.md
// ("M7 async-invoke / queues e2e gate — not on the radar").
//
// Tests:
//
//   - TestE2E_AsyncInvoke_PostEnqueuesRowAndDrainCompletesIt
//       Headline: POST /v1/apps/{slug}/invoke/async → 202 → row visible
//       via GET /v1/invocations/{id} → drain claims + dispatches via the
//       gatewayd synth socket → row transitions to state='completed'.
//       Asserts the wire-format, the pg_notify trip, the drain tick, and
//       the Wake fast-path return (the engine returns the seeded RUNNING
//       instance without consulting vmmd — that's the KVM-free trick).
//
//   - TestE2E_AsyncInvoke_PlanCap_FreePlanRejects
//       Free plan has AsyncInvokeAllowed=false (pkg/api/limits.go).
//       POST /invoke/async → 402 + CodePlanFeatureGated. No drain needed.
//
//   - TestE2E_QueueSend_PlanCap_QueueDepth
//       Hobby plan has MaxQueueDepth=5. Pre-fill the per-app queue to 5
//       via direct INSERTs, then POST /queues/send → 403 + CodePlanQueueDepth.
//       (Free's MaxQueueDepth=0 would short-circuit to 402/FeatureGated
//       — use Hobby to actually exercise the depth branch.)
//
//   - TestE2E_QueueSend_DrainLongPoll
//       Queue/send → queue/receive long-poll. The receive hits the drain's
//       invocation_done notify path; pair-asserts the drain loop's
//       notification fan-out without needing a real runner.
//
// Build tag: (none). CI-safe. Runs under `go test ./cmd/e2e/...`.
// Gated at runtime by `pgtest.OpenMigrated(t)` (returns nil if Postgres is
// unreachable — test exits silently, mirroring quota_e2e_test.go).
//
// Helper policy: reuses doReq / assertProblem / dbMigrateUp from
// quota_e2e_test.go (same package). New helpers (seedLiveDeployment etc.)
// are package-local so other e2e files can't accidentally depend on the
// invocation-specific shape.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestE2E_AsyncInvoke_PostEnqueuesRowAndDrainCompletesIt is the headline
// M7 gate. It boots apid + schedd and a successful synth stub. The stub
// represents a 2xx customer handler at the gateway boundary, so the test
// covers completion without requiring KVM while preserving real 5xx retry
// semantics.
//
// The fast-path trick that keeps this KVM-free: schedd's engine.Wake
// Phase-1 (pkg/sched/engine.go:268-281) is a pure DB read — if there's a
// RUNNING instance for the app, it returns without consulting vmmd. We
// pre-seed one with `state.CreateInstance{State: StateRunning, ...}`
// (mirrored from meterd_quota_e2e_test.go:88-101) so the drain's wake
// resolves on the first attempt.
func TestE2E_AsyncInvoke_PostEnqueuesRowAndDrainCompletesIt(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		t.Skip("pgtest.Open returned nil")
	}
	ctx := context.Background()
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("dbMigrateUp: %v", err)
	}

	h := e2etest.Start(t, pool,
		e2etest.APID|e2etest.Schedd|e2etest.GatewaySynthStub)

	key := h.SeedAccount(ctx, api.PlanHobby, "async-headline")
	store := state.NewPgStore(h.Pool)
	nodeID := defaultLocalComputeNodeID(t, ctx, store)

	slug := "asyncheadline"
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: string(state.AppTypeApp),
	})
	if status != http.StatusCreated {
		t.Fatalf("create app: status=%d body=%s", status, body)
	}
	var appResp api.AppResponse
	if err := json.Unmarshal(body, &appResp); err != nil {
		t.Fatalf("decode app response: %v body=%s", err, body)
	}

	seedLiveDeployment(t, ctx, store, appResp.ID, nodeID)

	body, status = doReq(t, h, key, http.MethodPost,
		"/v1/apps/"+slug+"/invoke/async",
		api.InvokeRequest{Payload: json.RawMessage(`{"hello":"world"}`)})
	if status != http.StatusAccepted {
		t.Fatalf("POST /invoke/async: status=%d want 202; body=%s", status, body)
	}
	var asyncResp api.AsyncInvokeResponse
	if err := json.Unmarshal(body, &asyncResp); err != nil {
		t.Fatalf("decode async response: %v body=%s", err, body)
	}
	if asyncResp.ID == "" {
		t.Fatalf("AsyncInvokeResponse.ID empty: %s", body)
	}
	if asyncResp.StatusURL == "" {
		t.Fatalf("AsyncInvokeResponse.StatusURL empty: %s", body)
	}

	// The drain's 1s safety ticker is the realistic bound; the pg_notify
	// path can land it faster, but we don't depend on that.
	inv := pollUntilCompleted(t, h, key, asyncResp.ID, 10*time.Second)

	if inv.State != "completed" {
		t.Fatalf("invocation state=%q want completed; last_error=%q", inv.State, inv.LastError)
	}
	if inv.Attempts < 1 {
		t.Errorf("attempts=%d want >=1", inv.Attempts)
	}
	if inv.InstanceID == "" {
		t.Errorf("instance_id empty; Wake fast path didn't return the seeded row")
	}
	if inv.LastError != "" {
		t.Errorf("last_error=%q want empty", inv.LastError)
	}
	if inv.CompletedAt == nil {
		t.Errorf("completed_at nil; drain didn't stamp it")
	}
}

// TestE2E_AsyncInvoke_PlanCap_FreePlanRejects asserts the plan gate on
// async-invoke. Free plan has AsyncInvokeAllowed=false (pkg/api/limits.go).
// The handler short-circuits before EnqueueInvocation; no drain needed.
func TestE2E_AsyncInvoke_PlanCap_FreePlanRejects(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		t.Skip("pgtest.Open returned nil")
	}
	ctx := context.Background()
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("dbMigrateUp: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)
	key := h.SeedAccount(ctx, api.PlanFree, "async-free")

	slug := "asyncfree"
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: string(state.AppTypeApp),
	})
	if status != http.StatusCreated {
		t.Fatalf("create app: status=%d body=%s", status, body)
	}

	// Direct assertion: handler returns ErrPlanFeatureGated → 402 + the
	// plan-feature-gated problem code. assertProblem in quota_e2e_test.go
	// decodes the RFC 7807 envelope and checks code+status.
	assertProblem(t, h, key, http.MethodPost,
		"/v1/apps/"+slug+"/invoke/async",
		api.InvokeRequest{Payload: json.RawMessage(`{"x":1}`)},
		http.StatusPaymentRequired, api.CodePlanFeatureGated)
}

// TestE2E_QueueSend_PlanCap_QueueDepth asserts the per-app queue depth
// cap. Hobby has MaxQueueDepth=5; we pre-fill the queue to 5 via direct
// INSERTs (bypassing the handler's own cap check) so the 6th POST must
// hit the cap branch in the handler.
//
// Why Hobby, not Free? Free has MaxQueueDepth=0 which short-circuits to
// 402 + CodePlanFeatureGated (the plan-feature gate), not 403 +
// CodePlanQueueDepth. Hobby is the cheapest plan that exercises the
// depth branch.
func TestE2E_QueueSend_PlanCap_QueueDepth(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		t.Skip("pgtest.Open returned nil")
	}
	ctx := context.Background()
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("dbMigrateUp: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)
	key := h.SeedAccount(ctx, api.PlanHobby, "queue-depth")

	slug := "queuedepth"
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: string(state.AppTypeApp),
	})
	if status != http.StatusCreated {
		t.Fatalf("create app: status=%d body=%s", status, body)
	}
	var appResp api.AppResponse
	if err := json.Unmarshal(body, &appResp); err != nil {
		t.Fatalf("decode app: %v body=%s", err, body)
	}

	prefillQueueRows(t, h, ctx, appResp.ID, 5)

	assertProblem(t, h, key, http.MethodPost,
		"/v1/apps/"+slug+"/queues/send",
		api.QueueSendRequest{Payload: json.RawMessage(`{"x":1}`)},
		http.StatusForbidden, api.CodePlanQueueDepth)
}

// TestE2E_QueueSend_DrainLongPoll — bonus coverage: send a row, then
// long-poll receive. Pairs the queue send with the drain's
// invocation_done notify so the receive handler unblocks. The seeded
// RUNNING instance keeps the Wake fast-path off vmmd, and the successful
// synth stub gives the drain a real terminal delivery (same shape as the
// headline test).
func TestE2E_QueueSend_DrainLongPoll(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		t.Skip("pgtest.Open returned nil")
	}
	ctx := context.Background()
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("dbMigrateUp: %v", err)
	}

	h := e2etest.Start(t, pool,
		e2etest.APID|e2etest.Schedd|e2etest.GatewaySynthStub)
	key := h.SeedAccount(ctx, api.PlanHobby, "queue-longpoll")
	store := state.NewPgStore(h.Pool)
	nodeID := defaultLocalComputeNodeID(t, ctx, store)

	slug := "queuepoll"
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: string(state.AppTypeApp),
	})
	if status != http.StatusCreated {
		t.Fatalf("create app: status=%d body=%s", status, body)
	}
	var appResp api.AppResponse
	if err := json.Unmarshal(body, &appResp); err != nil {
		t.Fatalf("decode app: %v body=%s", err, body)
	}
	seedLiveDeployment(t, ctx, store, appResp.ID, nodeID)

	body, status = doReq(t, h, key, http.MethodPost,
		"/v1/apps/"+slug+"/queues/send",
		api.QueueSendRequest{Payload: json.RawMessage(`{"hello":"queue"}`)})
	if status != http.StatusCreated {
		t.Fatalf("POST /queues/send: status=%d want 201; body=%s", status, body)
	}
	var sendResp api.QueueSendResponse
	if err := json.Unmarshal(body, &sendResp); err != nil {
		t.Fatalf("decode send response: %v body=%s", err, body)
	}

	// The handler's long-poll blocks up to 30s server-side for paid
	// plans. Poll with a generous deadline; success here proves the drain
	// emitted invocation_done and the notifier woke the receive handler.
	deadline := time.Now().Add(35 * time.Second)
	for {
		body, status := doReq(t, h, key, http.MethodPost,
			"/v1/apps/"+slug+"/queues/receive", nil)
		switch status {
		case http.StatusOK:
			var rr api.QueueReceiveResponse
			if err := json.Unmarshal(body, &rr); err != nil {
				t.Fatalf("decode receive: %v body=%s", err, body)
			}
			if rr.ID != sendResp.ID {
				t.Errorf("receive id=%q want %q", rr.ID, sendResp.ID)
			}
			return
		case http.StatusNoContent:
			// 204 = long-poll timeout; loop and try again.
			if time.Now().After(deadline) {
				t.Fatalf("queue/receive kept timing out after 35s; body=%s", body)
			}
			time.Sleep(200 * time.Millisecond)
			continue
		default:
			t.Fatalf("POST /queues/receive: status=%d body=%s", status, body)
		}
	}
}

// --- helpers ------------------------------------------------------------

// defaultLocalComputeNodeID resolves the seed compute_node row that
// migration 00024 inserts. Every instance we create needs a node_id FK,
// and the seeded row is the canonical target for e2e tests.
func defaultLocalComputeNodeID(t *testing.T, ctx context.Context, store *state.PgStore) string {
	t.Helper()
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("ComputeNodeByName(default-local): %v", err)
	}
	return node.ID
}

// seedLiveDeployment inserts a DeployLive deployment + a StateRunning
// instance so the drain's engine.Wake fast path returns the seeded row
// without consulting vmmd (the KVM-free trick — pkg/sched/engine.go:268-281).
//
// Mirrors cmd/e2e/meterd_quota_e2e_test.go:88-101.
func seedLiveDeployment(t *testing.T, ctx context.Context, store *state.PgStore, appID, nodeID string) {
	t.Helper()
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       appID,
		Status:      state.DeployLive,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	if _, err := store.CreateInstance(ctx, appID, dep.ID, string(state.StateRunning), 256, nodeID, ""); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
}

// prefillQueueRows inserts `count` rows directly into invocations with
// source='queue' state='pending'. Used to push the per-app queue depth
// to MaxQueueDepth so the next handler POST hits the cap branch.
//
// Direct INSERTs via the Store bypass the handler's own cap check; the
// handler can't see a pre-existing overflow before its count query
// lands.
func prefillQueueRows(t *testing.T, h *e2etest.Harness, ctx context.Context, appID string, count int) {
	t.Helper()
	app, err := state.NewPgStore(h.Pool).AppByID(ctx, appID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	for range count {
		_, err := state.NewPgStore(h.Pool).EnqueueInvocation(ctx, state.Invocation{
			AppID:     appID,
			AccountID: app.AccountID,
			Source:    state.InvocationQueue,
			Method:    "POST",
			Path:      "/",
			Payload:   json.RawMessage(`{}`),
			DueAt:     time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("EnqueueInvocation: %v", err)
		}
	}
}

// pollUntilCompleted polls GET /v1/invocations/{id} every 100 ms until
// the row reaches a terminal state. Returns the final row.
//
// The handler's GET returns 200 with the wire-mirror of state.Invocation
// (api.Invocation). Per pkg-api-cannot-import-pkg-state.md the type
// lives in pkg/api so the handler can ship it without a cycle.
func pollUntilCompleted(t *testing.T, h *e2etest.Harness, key, id string, deadline time.Duration) api.Invocation {
	t.Helper()
	end := time.Now().Add(deadline)
	for {
		body, status := doReq(t, h, key, http.MethodGet, "/v1/invocations/"+id, nil)
		if status != http.StatusOK {
			t.Fatalf("GET /invocations/%s: status=%d body=%s", id, status, body)
		}
		var inv api.Invocation
		if err := json.Unmarshal(body, &inv); err != nil {
			t.Fatalf("decode invocation: %v body=%s", err, body)
		}
		switch inv.State {
		case "completed", "failed", "cancelled":
			return inv
		}
		if time.Now().After(end) {
			t.Fatalf("invocation %s did not reach terminal state within %s; last state=%s last_error=%q",
				id, deadline, inv.State, inv.LastError)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// --- M8 §14 row 6 (cold-wake transparency UX, ux_spec §6.5) --------------
//
// ux_spec §6.5 ("Cold-wake transparency surfaces") requires that the
// floor of `min_instances` instances per app be persisted across the
// idle reaper's park window. The Free plan gates the knob entirely
// (MinInstancesAllowed=false in pkg/api/limits.go:182-227); Hobby+
// was unlocked in issue #462 / ADR-058 PR-A, so Hobby/Pro/Scale opt
// in. Three tests pin the cross-process surface:
//
//   - Free   → 403 plan_min_instances_not_allowed (the canonical
//     "plan forbids it" pin).
//   - Hobby  → 200 + apps.min_instances persisted (issue #462
//     tier-up: Hobby now unlocks the knob).
//   - Pro    → 200 + apps.min_instances persisted.
//
// The unit-level pins live in cmd/apid/handlers_ext_test.go:130
// (`TestExt_UpdateApp_HobbyAcceptsMinInstances` post-PR-A) +
// the 422 over-cap case at :160. The cross-process layer below
// catches the failures that those can't: the wire format, the
// apid→apid→PG write path, and the post-write idleness floor.

// TestColdWake_MinInstances_Free_Rejects — cross-process equivalent
// of the plan-gate pin in cmd/apid/handlers_ext_test.go. Pre-#462
// this test exercised Hobby (the plan that used to gate the knob);
// PR-A's Hobby+ tier-up means Hobby now accepts, so Free is the
// remaining "plan forbids it" plan. PATCH /v1/apps/{slug} with
// min_instances=1 on a Free account must 403 with
// CodePlanMinInstancesNotAllowed. The wire code is the same
// contract the dashboard form relies on (deferred to Move 3 / the
// ux_spec §6.5 surfaces).
//
// Note: MinInstances is set via PATCH /v1/apps/{slug}
// (api.UpdateAppRequest), not on create — CreateAppRequest is the
// minimal "register an app" surface. The plan gate fires on PATCH,
// which is what the dashboard form actually uses.
func TestColdWake_MinInstances_Free_Rejects(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)
	key := h.SeedAccount(context.Background(), api.PlanFree)

	// Create the app first (Free allows create without the
	// floor; it only rejects setting the floor).
	if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "floor-free", RAMMB: 128}); status != http.StatusCreated {
		t.Fatalf("create app (Free): status=%d", status)
	}

	minInstances := 1
	body, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/floor-free",
		api.UpdateAppRequest{MinInstances: &minInstances})
	if status != http.StatusForbidden {
		t.Fatalf("PATCH /v1/apps/floor-free (Free + min_instances=1): status=%d, want 403\nbody=%s", status, body)
	}
	var prob api.Problem
	if err := json.Unmarshal(body, &prob); err != nil {
		t.Fatalf("decode problem: %v body=%s", err, body)
	}
	if prob.Code != api.CodePlanMinInstancesNotAllowed {
		t.Errorf("problem.code = %q, want %q", prob.Code, api.CodePlanMinInstancesNotAllowed)
	}
}

// TestColdWake_MinInstances_Hobby_Accepts — cross-process equivalent
// of the post-PR-A Hobby+ tier-up pin in cmd/apid/handlers_ext_test.go
// (issue #462 / ADR-058). Hobby now unlocks MinInstancesAllowed;
// PATCH /v1/apps/{slug} with min_instances=1 on a Hobby account must
// 200 and the apps.min_instances column must round-trip to 1 when
// GET /v1/apps/{slug} fires. Catches the regression where the
// handler-side validation accepts the knob but the UPDATE drops it
// (the kind of drift that handlers_ext_test.go's pure in-memory
// PgStore can mask).
func TestColdWake_MinInstances_Hobby_Accepts(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "floor-hobby", RAMMB: 256}); status != http.StatusCreated {
		t.Fatalf("create app (Hobby): status=%d", status)
	}

	minInstances := 1
	body, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/floor-hobby",
		api.UpdateAppRequest{MinInstances: &minInstances})
	if status != http.StatusOK {
		t.Fatalf("PATCH /v1/apps/floor-hobby (Hobby + min_instances=1): status=%d, want 200\nbody=%s", status, body)
	}

	// GET must echo min_instances=1 — the regression pin (write
	// accepted but value not persisted). Mirrors the
	// TestColdWake_MinInstances_Pro_Accepts shape.
	getBody, getStatus := doReq(t, h, key, http.MethodGet, "/v1/apps/floor-hobby", nil)
	if getStatus != http.StatusOK {
		t.Fatalf("GET /v1/apps/floor-hobby: status=%d, want 200\nbody=%s", getStatus, getBody)
	}
	var app api.AppResponse
	if err := json.Unmarshal(getBody, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, getBody)
	}
	if app.MinInstances != 1 {
		t.Errorf("GET /v1/apps/floor-hobby min_instances = %d, want 1", app.MinInstances)
	}
}

// TestColdWake_MinInstances_Pro_Accepts — Pro has
// MinInstancesAllowed=true. PATCH /v1/apps/{slug} with
// min_instances=1 must 200 and the apps.min_instances column must
// round-trip to 1 when GET /v1/apps/{slug} fires. Catches the
// regression where the handler-side validation accepts the knob
// but the UPDATE drops it (the kind of drift that
// handlers_ext_test.go's pure in-memory PgStore can mask).
func TestColdWake_MinInstances_Pro_Accepts(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)
	key := h.SeedAccount(context.Background(), api.PlanPro)

	if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "floor-pro", RAMMB: 512}); status != http.StatusCreated {
		t.Fatalf("create app (Pro): status=%d", status)
	}

	minInstances := 1
	body, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/floor-pro",
		api.UpdateAppRequest{MinInstances: &minInstances})
	if status != http.StatusOK {
		t.Fatalf("PATCH /v1/apps/floor-pro (Pro + min_instances=1): status=%d, want 200\nbody=%s", status, body)
	}
	var updated api.AppResponse
	if err := json.Unmarshal(body, &updated); err != nil {
		t.Fatalf("decode app: %v body=%s", err, body)
	}
	if updated.MinInstances != 1 {
		t.Errorf("update response: min_instances = %d, want 1", updated.MinInstances)
	}

	// Round-trip via GET to assert the PG row actually carries the
	// value (the response DTO is also built in-memory by the
	// handler so a round-trip-only failure here means a write-side
	// bug — the kind that the unit tests at
	// handlers_ext_test.go:160 can't catch).
	raw, status := doReq(t, h, key, http.MethodGet, "/v1/apps/floor-pro", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/apps/floor-pro: status=%d body=%s", status, raw)
	}
	var got api.AppResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode app: %v body=%s", err, raw)
	}
	if got.MinInstances != 1 {
		t.Errorf("PG-roundtrip: min_instances = %d, want 1", got.MinInstances)
	}
}

// TestColdWake_FloorKeepsInstancesWarm — the ux_spec §6.5 contract:
// once `min_instances=N` is set, the apps row carries N through the
// wake path AND the reaper-side honor is pinned at the cross-process
// layer.
//
// Cross-process surface we can pin without a real FC fleet: the wire
// DTO round-trips the floor across GET → PATCH → GET (a wake cycle
// must NOT silently zero the floor; that's the kind of regression a
// future hot path that calls `apps.MinInstances = 0` on wake would
// introduce). The actual no-KVM-reaper floor enforcement is pinned
// in-process at pkg/sched/reaper_test.go:158
// (TestReapIdleRespectsMinInstancesFloor) — the watchdog here runs
// against `firecracker: executable file not found in $PATH` so
// instance rows are parked, but the apps.min_instances column
// invariant is what the dashboard renders ("always-on floor: 1")
// and is what the customer is billed against.
//
// If a future refactor collapses "min_instances" into a derived
// field on wake, the assertion below fires at CI time.
func TestColdWake_FloorKeepsInstancesWarm(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)
	ctx := context.Background()
	key := h.SeedAccount(ctx, api.PlanPro)

	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "floor-warm", RAMMB: 512})
	if status != http.StatusCreated {
		t.Fatalf("create app: status=%d", status)
	}
	var created api.AppResponse
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatalf("decode created app: %v body=%s", err, raw)
	}
	store := state.NewPgStore(h.Pool)
	seedLiveDeployment(t, ctx, store, created.ID, defaultLocalComputeNodeID(t, ctx, store))
	minInstances := 1
	if _, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/floor-warm",
		api.UpdateAppRequest{MinInstances: &minInstances}); status != http.StatusOK {
		t.Fatalf("patch app: status=%d", status)
	}

	// Wake is a no-op on a freshly-created app (no snapshot, no
	// FC) — but the wire path runs through the same handler that
	// schedules a wake on the floor. We assert 2xx (the wake
	// handler is idempotent).
	if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/floor-warm/wake", nil); status < 200 || status >= 300 {
		t.Fatalf("wake: status=%d, want 2xx", status)
	}

	// The load-bearing invariant: apps.min_instances is STILL 1
	// after the wake cycle. A regression where the wake handler
	// clears the floor (e.g. by overwriting with the apps row's
	// default) fails here.
	raw, status = doReq(t, h, key, http.MethodGet, "/v1/apps/floor-warm", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/apps/floor-warm: status=%d body=%s", status, raw)
	}
	var got api.AppResponse
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, raw)
	}
	if got.MinInstances != 1 {
		t.Errorf("apps.min_instances after wake: %d, want 1 — wake handler zeroed the floor", got.MinInstances)
	}
}
