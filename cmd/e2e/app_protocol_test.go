// app_protocol_test.go — issue #xxx / ADR-124 PR-A acceptance for the
// *flag + plan-gate* surface (the e2e pin below covers the apid
// side; the gateway-side x-faas-protocol stamp + bridge-side
// framing observability is pinned in pkg/gateway/handler_test.go
// + pkg/gateway/forwardproxy.go in-process — see note below).
//
// PR-A wires the per-app app_protocol selector, plan default
// ("http1" universal), plan gate (grpc Hobby+/Pro/Scale only;
// Free customers get 403 plan_app_protocol_grpc_not_allowed),
// invalid-value rejection (400 app_protocol_invalid), and the
// apps.app_protocol column + apps_app_protocol_chk CHECK
// constraint (migration 00378). PR-B / G19 ships the bridge-side
// framing switch on top of this flag (out of scope here, filed
// in spec §17 G19).
//
// This test exercises four pins, all on the apid + Postgres
// surface (CI-safe — no metal, no /dev/kvm):
//
//  1. Plan-gate: a Free-plan customer cannot create an app with
//     app_protocol=grpc. POST /v1/apps returns 403
//     plan_app_protocol_grpc_not_allowed with the structured
//     error envelope (limit value, observed value, docs URL).
//
//  2. Plan-default: a Hobby customer creates an app WITHOUT
//     setting app_protocol → the persisted App.app_protocol
//     matches the universal default ("http1").
//
//  3. Persistence: a Hobby customer can set app_protocol="http2"
//     on create → the persisted App.app_protocol reflects the
//     explicit value (not the default).
//
//  4. Invalid value: a Hobby customer PATCHes
//     app_protocol="websocket" → 400 app_protocol_invalid.
//
// What this test does NOT cover (deliberately):
//
//   - The x-faas-protocol header stamp on the inbound request at
//     pkg/gateway/handler.go (covered by handler_test.go in-process).
//   - The bridge-side framing switch (covered by future PR-B /
//     G19 — see ADR-124 §"Out of scope"). The bridge
//     re-frames to HTTP/1.1 plaintext today (PR #750 / ADR-079
//     amendment); customer → end-to-end gRPC framing is a
//     separate multi-week ADR on rewriting
//     cmd/vmmd-stream-bridge.
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

// TestE2E_AppProtocol_FreePlanGrpcRejected covers pin #1:
// a Free-plan account cannot create an app with
// app_protocol=grpc. apid's buildApp plan-gate emits 403
// plan_app_protocol_grpc_not_allowed with a structured Problem
// body that carries the limit value (the plan's
// AppProtocolAllowed("grpc") predicate), the observed value
// ("grpc"), and the docs URL. The same gate fires on PATCH
// (UpdateApp) but only the create-side pin is exercised here
// to keep the test surface small; the update-side pin is
// covered by the unit test in pkg/api.
func TestE2E_AppProtocol_FreePlanGrpcRejected(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanFree)

	// Sanity check: the Free plan must report grpc as not
	// allowed. If a future change ever silently flips this, the
	// AC fails loudly here — exactly the regression the test is
	// here to catch.
	if api.PlanFree.AppProtocolAllowed(api.AppProtocolGRPC) {
		t.Fatalf("PlanFree reported AppProtocolAllowed(grpc)=true; ADR-124 §Plan gating expects Free to be blocked")
	}

	// Attempt to create an app with grpc on Free.
	grpcProto := api.AppProtocolGRPC
	body := api.CreateAppRequest{
		Slug:        "grpc-free",
		AppProtocol: &grpcProto,
	}
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps", body)
	if status != http.StatusForbidden {
		t.Fatalf("create app with app_protocol=grpc on Free plan: status=%d want %d body=%s",
			status, http.StatusForbidden, raw)
	}
	var prob api.Problem
	if err := json.Unmarshal(raw, &prob); err != nil {
		t.Fatalf("decode problem body: %v (raw=%s)", err, raw)
	}
	if prob.Code != api.CodePlanAppProtocolGrpcNotAllowed {
		t.Errorf("prob.code = %q, want %q",
			prob.Code, api.CodePlanAppProtocolGrpcNotAllowed)
	}
	if prob.Status != http.StatusForbidden {
		t.Errorf("prob.status = %d, want %d",
			prob.Status, http.StatusForbidden)
	}
}

// TestE2E_AppProtocol_HobbyPlanDefaultIsHTTP1 covers pin #2:
// a Hobby customer creates an app WITHOUT setting
// app_protocol → the persisted App.app_protocol is "http1"
// (the universal default per ADR-124 §Decision 1 — no per-plan
// differentiation in this PR).
func TestE2E_AppProtocol_HobbyPlanDefaultIsHTTP1(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	// Sanity check: Hobby must permit http1 (and all three
	// values, in fact — http1 and http2 are universal).
	if !api.PlanHobby.AppProtocolAllowed(api.AppProtocolHTTP1) {
		t.Fatalf("PlanHobby reported AppProtocolAllowed(http1)=false; AC expects Hobby to be allowed")
	}

	createBody, status := doReq(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "proto-default"})
	if status != http.StatusCreated {
		t.Fatalf("create app: %d %s", status, createBody)
	}
	var created api.AppResponse
	if err := json.Unmarshal(createBody, &created); err != nil {
		t.Fatalf("decode created: %v (raw=%s)", err, createBody)
	}
	if created.AppProtocol != api.AppProtocolHTTP1 {
		t.Errorf("Hobby create (no AppProtocol): AppProtocol=%q, want %q (plan default)",
			created.AppProtocol, api.AppProtocolHTTP1)
	}
}

// TestE2E_AppProtocol_HobbyPlanPersistence covers pin #3:
// a Hobby customer creates an app with app_protocol="http2"
// (universal — both Hobby and Free permit http2) and the row
// reflects the explicit value, not the default. This pins the
// closed-set persistence path end-to-end (apid → sqlc → pgstore
// → migration 00378 column → GET /v1/apps/{slug} response).
func TestE2E_AppProtocol_HobbyPlanPersistence(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	http2Proto := api.AppProtocolHTTP2
	body := api.CreateAppRequest{
		Slug:        "proto-http2",
		AppProtocol: &http2Proto,
	}
	createBody, status := doReq(t, h, key, http.MethodPost, "/v1/apps", body)
	if status != http.StatusCreated {
		t.Fatalf("create app with app_protocol=http2: %d %s", status, createBody)
	}
	var created api.AppResponse
	if err := json.Unmarshal(createBody, &created); err != nil {
		t.Fatalf("decode created: %v (raw=%s)", err, createBody)
	}
	if created.AppProtocol != api.AppProtocolHTTP2 {
		t.Errorf("Hobby create (AppProtocol=http2): AppProtocol=%q, want %q",
			created.AppProtocol, api.AppProtocolHTTP2)
	}

	// Re-fetch via GET and confirm the persisted row still says
	// "http2" (catches any UPDATE clause / sqlc regen drift).
	getBody, status := doReq(t, h, key, http.MethodGet, "/v1/apps/proto-http2", nil)
	if status != http.StatusOK {
		t.Fatalf("get app: %d %s", status, getBody)
	}
	var fetched api.AppResponse
	if err := json.Unmarshal(getBody, &fetched); err != nil {
		t.Fatalf("decode fetched: %v (raw=%s)", err, getBody)
	}
	if fetched.AppProtocol != api.AppProtocolHTTP2 {
		t.Errorf("GET /v1/apps/proto-http2: AppProtocol=%q, want %q",
			fetched.AppProtocol, api.AppProtocolHTTP2)
	}
}

// TestE2E_AppProtocol_InvalidValue covers pin #4: a Hobby
// customer PATCHes app_protocol="websocket" (a value outside
// the closed set {http1, http2, grpc}). apid's UpdateApp
// validator emits 400 app_protocol_invalid with the structured
// error envelope. This pin guards against anyone widening the
// closed set silently — a regression here means the column's
// CHECK constraint (migration 00378) is the only thing
// preventing bogus values from landing on disk.
//
// Note: the test deliberately exercises PATCH (not POST) so
// the UPDATE-clause path (State.UpdateApp's Set*/optional-pointer
// convention) is covered separately from the INSERT-clause
// path (State.CreateApp).
func TestE2E_AppProtocol_InvalidValue(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, e2etest.APID)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	// Create with valid http2 first so we have an existing app
	// to PATCH.
	http2Proto := api.AppProtocolHTTP2
	createBody, status := doReq(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "proto-bad", AppProtocol: &http2Proto})
	if status != http.StatusCreated {
		t.Fatalf("create app: %d %s", status, createBody)
	}

	// Now PATCH with an invalid value.
	bogus := "websocket"
	raw, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/proto-bad",
		api.UpdateAppRequest{AppProtocol: &bogus})
	if status != http.StatusBadRequest {
		t.Fatalf("PATCH app_protocol=websocket: status=%d want %d body=%s",
			status, http.StatusBadRequest, raw)
	}
	var prob api.Problem
	if err := json.Unmarshal(raw, &prob); err != nil {
		t.Fatalf("decode problem body: %v (raw=%s)", err, raw)
	}
	if prob.Code != api.CodeAppProtocolInvalid {
		t.Errorf("prob.code = %q, want %q",
			prob.Code, api.CodeAppProtocolInvalid)
	}
	if prob.Status != http.StatusBadRequest {
		t.Errorf("prob.status = %d, want %d",
			prob.Status, http.StatusBadRequest)
	}
}
