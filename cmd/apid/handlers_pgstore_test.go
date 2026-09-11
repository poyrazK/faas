//go:build !no_pg

package main

// Handler-level Postgres coverage for the production persistence seam.
// MemStore remains useful for fast unit tests, but these cases deliberately
// construct the real PgStore so SQL constraints, transactions, and query
// projections are exercised through the same HTTP handlers customers use.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/wire"
)

type pgHandlerEnv struct {
	h     http.Handler
	s     *server
	store state.Store
	pool  *pgxpool.Pool
	key   string
	acct  state.Account
}

func setupPGHandler(t *testing.T, plan api.Plan) pgHandlerEnv {
	t.Helper()
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(context.Background(), "pg-handler-"+uuid.NewString()+"@example.com", plan)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "pg-handler", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	ops := wire.NewOpsMetrics("apid_pg_handler_test")
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).
		WithOpsMetrics(context.Background(), ops)
	return pgHandlerEnv{h: srv.handler(), s: srv, store: store, pool: pool, key: plain, acct: acct}
}

func (e pgHandlerEnv) do(t *testing.T, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		r = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Authorization", "Bearer "+e.key)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e pgHandlerEnv) addAdminSession(t *testing.T, req *http.Request) {
	t.Helper()
	sid := uuid.NewString()
	if _, err := e.store.CreateSession(req.Context(), sid, e.acct.ID, "192.0.2.10", "pg-handler-test"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	token, err := e.s.sessions.IssueWithSessionAndBindingHashAndStepUp(sid, e.acct.ID, "", time.Now(), false)
	if err != nil {
		t.Fatalf("IssueWithSessionAndBindingHashAndStepUp: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
}

func (e pgHandlerEnv) doAdminWithKey(t *testing.T, method, path string, body any, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		r = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, path, r)
	e.addAdminSession(t, req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idempotencyKey)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func seedPGApp(t *testing.T, e pgHandlerEnv, slug string) state.App {
	t.Helper()
	app, err := e.store.CreateApp(context.Background(), state.App{
		AccountID:      e.acct.ID,
		Slug:           slug,
		Type:           state.AppTypeApp,
		Status:         state.AppActive,
		RequireAuthn:   e.acct.Plan.RequireAuthnDefault(),
		PublicAuthMode: e.acct.Plan.PublicAuthModeDefault(),
	})
	if err != nil {
		t.Fatalf("CreateApp(%q): %v", slug, err)
	}
	return app
}

// spec: §4.4, §11 — quota enforcement must hold at the SQL boundary when
// concurrent requests race through the create-app handler.
func TestPGHandler_CreateAppConcurrentQuota(t *testing.T) {
	e := setupPGHandler(t, api.PlanFree)
	const n = 6
	var (
		created atomic.Int32
		quota   atomic.Int32
		other   atomic.Int32
		wg      sync.WaitGroup
	)
	start := make(chan struct{})
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			<-start
			rec := e.do(t, http.MethodPost, "/v1/apps", map[string]string{"slug": fmt.Sprintf("pg-quota-%d", i)}, map[string]string{"Content-Type": "application/json"})
			switch rec.Code {
			case http.StatusCreated:
				created.Add(1)
			case http.StatusForbidden:
				var problem api.Problem
				if err := json.Unmarshal(rec.Body.Bytes(), &problem); err == nil && problem.Code == api.CodePlanLimitApps {
					quota.Add(1)
					return
				}
				other.Add(1)
			default:
				other.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if created.Load() != 1 || quota.Load() != n-1 || other.Load() != 0 {
		t.Fatalf("quota race results: created=%d quota403=%d other=%d; want 1/%d/0", created.Load(), quota.Load(), other.Load(), n-1)
	}
	count, err := e.store.CountDeployedApps(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("CountDeployedApps: %v", err)
	}
	if count != 1 {
		t.Fatalf("stored app count = %d, want 1", count)
	}
}

// spec: §7.2, §11 — usage responses must be assembled from the account's
// persisted app and usage projection, not an in-memory substitute.
func TestPGHandler_AppUsageReadsPgStore(t *testing.T) {
	e := setupPGHandler(t, api.PlanHobby)
	seedPGApp(t, e, "pg-usage")
	rec := e.do(t, http.MethodGet, "/v1/apps/pg-usage/usage", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out api.AppUsageSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Slug != "pg-usage" || out.Source != "usage_minutes" {
		t.Fatalf("usage response = %+v, want pg-backed empty usage for pg-usage", out)
	}
	if out.GBHours != 0 || out.MBSeconds != 0 || out.OverageGBHours != 0 {
		t.Fatalf("empty usage response = %+v, want zero usage", out)
	}
}

// TestPGHandler_DebuggerRequestAndRegressionReadPaths exercises the complete
// customer debugger loop against the real HTTP handler and PgStore: request
// list/get, an empty regression result, evidence without enrichment, then a
// non-empty regression result and matching evidence after an upsert.
func TestPGHandler_DebuggerRequestAndRegressionReadPaths(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	app := seedPGApp(t, e, "pg-debugger")
	deploymentID := uuid.New()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := e.store.InsertRequestTelemetry(context.Background(), sqlc.InsertRequestTelemetryParams{
		AccountID:    pgtype.UUID{Bytes: uuid.MustParse(e.acct.ID), Valid: true},
		AppID:        pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true},
		DeploymentID: pgtype.UUID{Bytes: deploymentID, Valid: true},
		Route:        "GET /debug",
		Method:       "GET",
		Status:       200,
		LatencyMs:    87,
		ColdBoot:     false,
		TraceID:      pgtype.Text{},
		ReceivedAt:   pgtype.Timestamptz{Time: now, Valid: true},
		Count:        1,
		UaFamily:     "__unknown__",
		ReferrerHost: "__none__",
		Country:      "__unknown__",
	}); err != nil {
		t.Fatalf("InsertRequestTelemetry: %v", err)
	}

	listRec := e.do(t, http.MethodGet, "/v1/apps/pg-debugger/debug/requests?since=24h", nil, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("debug request list status = %d: %s", listRec.Code, listRec.Body.String())
	}
	var listed api.DebugTelemetryListResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode debug request list: %v", err)
	}
	if len(listed.Requests) != 1 {
		t.Fatalf("debug request list = %+v, want one row", listed)
	}
	reqID := listed.Requests[0].ID

	coverageRec := e.do(t, http.MethodGet, "/v1/apps/pg-debugger/debug/coverage?since=24h", nil, nil)
	if coverageRec.Code != http.StatusOK {
		t.Fatalf("debug coverage status = %d: %s", coverageRec.Code, coverageRec.Body.String())
	}
	var coverage api.DebugCoverageResponse
	if err := json.Unmarshal(coverageRec.Body.Bytes(), &coverage); err != nil {
		t.Fatalf("decode debug coverage: %v", err)
	}
	if coverage.AppID != app.ID || coverage.RepresentedRequests != 1 || coverage.TelemetryRows != 1 {
		t.Fatalf("debug coverage = %+v, want one represented request and one row", coverage)
	}
	if coverage.TraceLinked.Requests != 0 || coverage.SpanEvidence.Requests != 0 || coverage.WakeEvidence.Requests != 0 || coverage.GuestEvidence.Requests != 0 {
		t.Fatalf("debug coverage optional signals = %+v, want zero for fixture", coverage)
	}

	getRec := e.do(t, http.MethodGet, "/v1/apps/pg-debugger/debug/requests/"+reqID, nil, nil)
	if getRec.Code != http.StatusOK {
		t.Fatalf("debug request get status = %d: %s", getRec.Code, getRec.Body.String())
	}
	var got api.DebugTelemetryRequestItem
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode debug request get: %v", err)
	}
	if got.ID != reqID || got.Route != "GET /debug" || got.LatencyMS != 87 {
		t.Fatalf("debug request get = %+v, want id=%s route=GET /debug latency=87", got, reqID)
	}

	regRec := e.do(t, http.MethodGet, "/v1/apps/pg-debugger/debug/regressions?since=24h", nil, nil)
	if regRec.Code != http.StatusOK {
		t.Fatalf("empty debug regressions status = %d: %s", regRec.Code, regRec.Body.String())
	}
	var empty api.DebugRegressionsResponse
	if err := json.Unmarshal(regRec.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decode empty debug regressions: %v", err)
	}
	if len(empty.Regressions) != 0 {
		t.Fatalf("empty debug regressions = %+v, want zero rows", empty.Regressions)
	}

	evidenceRec := e.do(t, http.MethodGet, "/v1/apps/pg-debugger/debug/requests/"+reqID+"/evidence", nil, nil)
	if evidenceRec.Code != http.StatusOK {
		t.Fatalf("debug evidence without regression status = %d: %s", evidenceRec.Code, evidenceRec.Body.String())
	}
	var evidence api.DebugRequestEvidenceResponse
	if err := json.Unmarshal(evidenceRec.Body.Bytes(), &evidence); err != nil {
		t.Fatalf("decode debug evidence: %v", err)
	}
	if evidence.Request.ID != reqID || evidence.Regression != nil || evidence.Explanation.Status != "unobserved" {
		t.Fatalf("debug evidence without regression = %+v, want request + nil regression + unobserved", evidence)
	}

	factor := pgtype.Numeric{}
	if err := factor.Scan("1.50"); err != nil {
		t.Fatalf("scan regression factor: %v", err)
	}
	if err := e.store.UpsertRegressionObservation(context.Background(), sqlc.UpsertRegressionObservationParams{
		AppID:        pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true},
		DeploymentID: pgtype.UUID{Bytes: deploymentID, Valid: true},
		Route:        "GET /debug", P95Ms: 300, P95BaseMs: 200, AffectedCount: 4,
		RegressionFactor: factor,
	}); err != nil {
		t.Fatalf("UpsertRegressionObservation: %v", err)
	}

	regRec = e.do(t, http.MethodGet, "/v1/apps/pg-debugger/debug/regressions?since=24h", nil, nil)
	if regRec.Code != http.StatusOK {
		t.Fatalf("non-empty debug regressions status = %d: %s", regRec.Code, regRec.Body.String())
	}
	var nonEmpty api.DebugRegressionsResponse
	if err := json.Unmarshal(regRec.Body.Bytes(), &nonEmpty); err != nil {
		t.Fatalf("decode non-empty debug regressions: %v", err)
	}
	if len(nonEmpty.Regressions) != 1 || nonEmpty.Regressions[0].Route != "GET /debug" || nonEmpty.Regressions[0].Factor != "1.50" {
		t.Fatalf("non-empty debug regressions = %+v, want GET /debug at 1.50x", nonEmpty.Regressions)
	}

	evidenceRec = e.do(t, http.MethodGet, "/v1/apps/pg-debugger/debug/requests/"+reqID+"/evidence", nil, nil)
	if evidenceRec.Code != http.StatusOK {
		t.Fatalf("debug evidence with regression status = %d: %s", evidenceRec.Code, evidenceRec.Body.String())
	}
	if err := json.Unmarshal(evidenceRec.Body.Bytes(), &evidence); err != nil {
		t.Fatalf("decode debug evidence with regression: %v", err)
	}
	if evidence.Regression == nil || evidence.Regression.Route != "GET /debug" || evidence.Explanation.Status != "regression_detected" {
		t.Fatalf("debug evidence with regression = %+v, want matching regression_detected explanation", evidence)
	}
}

func TestPGHandler_DebuggerEvidenceDegradesWhenRegressionReadFails(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	app := seedPGApp(t, e, "pg-debugger-degraded")
	if err := e.store.InsertRequestTelemetry(context.Background(), sqlc.InsertRequestTelemetryParams{
		AccountID:    pgtype.UUID{Bytes: uuid.MustParse(e.acct.ID), Valid: true},
		AppID:        pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true},
		DeploymentID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		Route:        "GET /degraded",
		Method:       "GET",
		Status:       500,
		LatencyMs:    42,
		ColdBoot:     false,
		// request_telemetry.trace_id follows the W3C 32-character lowercase
		// hexadecimal format; the request row ID is not a valid trace ID.
		TraceID:      pgtype.Text{String: "0123456789abcdef0123456789abcdef", Valid: true},
		ReceivedAt:   pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		Count:        1,
		UaFamily:     "__unknown__",
		ReferrerHost: "__none__",
		Country:      "__unknown__",
	}); err != nil {
		t.Fatalf("InsertRequestTelemetry: %v", err)
	}
	listRec := e.do(t, http.MethodGet, "/v1/apps/pg-debugger-degraded/debug/requests?since=24h", nil, nil)
	if listRec.Code != http.StatusOK {
		t.Fatalf("debug request list status = %d: %s", listRec.Code, listRec.Body.String())
	}
	var listed api.DebugTelemetryListResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil || len(listed.Requests) != 1 {
		t.Fatalf("decode debug request list = %+v (err=%v)", listed, err)
	}

	if _, err := e.pool.Exec(context.Background(), `drop table debug_regression_observations`); err != nil {
		t.Fatalf("drop regression table: %v", err)
	}
	reqID := listed.Requests[0].ID
	evidenceRec := e.do(t, http.MethodGet, "/v1/apps/pg-debugger-degraded/debug/requests/"+reqID+"/evidence", nil, nil)
	if evidenceRec.Code != http.StatusOK {
		t.Fatalf("degraded debug evidence status = %d: %s", evidenceRec.Code, evidenceRec.Body.String())
	}
	var evidence api.DebugRequestEvidenceResponse
	if err := json.Unmarshal(evidenceRec.Body.Bytes(), &evidence); err != nil {
		t.Fatalf("decode degraded debug evidence: %v", err)
	}
	if evidence.Request.ID != reqID || evidence.Regression != nil || evidence.Explanation.Status != "regression_unavailable" {
		t.Fatalf("degraded debug evidence = %+v, want request + nil regression + regression_unavailable", evidence)
	}

	regRec := e.do(t, http.MethodGet, "/v1/apps/pg-debugger-degraded/debug/regressions?since=24h", nil, nil)
	if regRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("degraded debug regressions status = %d: %s", regRec.Code, regRec.Body.String())
	}
	var problem api.Problem
	if err := json.Unmarshal(regRec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode degraded debug regressions problem: %v", err)
	}
	if problem.Code != api.CodeDebugRegressionUnavailable {
		t.Fatalf("degraded debug regressions code = %q, want %q", problem.Code, api.CodeDebugRegressionUnavailable)
	}
}

// spec: §7.1, §11 — deployment history is read through the production store
// and keeps the SQL ordering/cursor contract at the handler boundary.
func TestPGHandler_DeploymentHistoryPaginates(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	app := seedPGApp(t, e, "pg-history")
	base := time.Now().UTC().Truncate(time.Second).Add(-3 * time.Minute)
	for i := 0; i < 3; i++ {
		if _, err := e.store.CreateDeployment(context.Background(), state.Deployment{
			AppID:       app.ID,
			ImageDigest: fmt.Sprintf("sha256:%064d", i+1),
			Kind:        state.DeploymentKindImage,
			Status:      state.DeployBuilding,
			CreatedAt:   base.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("CreateDeployment(%d): %v", i, err)
		}
	}
	page1 := e.do(t, http.MethodGet, "/v1/apps/pg-history/deployments?limit=2", nil, nil)
	if page1.Code != http.StatusOK {
		t.Fatalf("page 1 status = %d: %s", page1.Code, page1.Body.String())
	}
	var first api.DeploymentListResponse
	if err := json.Unmarshal(page1.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode page 1: %v", err)
	}
	if len(first.Items) != 2 || first.NextBefore == "" || first.Items[0].CreatedAt <= first.Items[1].CreatedAt {
		t.Fatalf("page 1 = %+v, want two newest rows and a cursor", first)
	}
	page2 := e.do(t, http.MethodGet, "/v1/apps/pg-history/deployments?limit=2&before="+first.NextBefore, nil, nil)
	if page2.Code != http.StatusOK {
		t.Fatalf("page 2 status = %d: %s", page2.Code, page2.Body.String())
	}
	var second api.DeploymentListResponse
	if err := json.Unmarshal(page2.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode page 2: %v", err)
	}
	if len(second.Items) != 1 || second.NextBefore != "" {
		t.Fatalf("page 2 = %+v, want final row without a cursor", second)
	}
}

// spec: §11 — operator credit issuance must commit its credit, ledger, and
// idempotency rows through PgStore while replaying the same request safely.
func TestPGHandler_AdminCreditIdempotency(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	e.s.WithAdminAllowlist(e.acct.Email)
	e.s.WithBillingProvider(&consumeRefundProvider{})
	target, err := e.store.CreateAccount(context.Background(), "pg-credit-target-"+uuid.NewString()+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount target: %v", err)
	}
	path := "/v1/admin/accounts/" + target.ID + "/credits"
	body := map[string]any{"cents": 500, "reason": "pg handler test"}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{"cents":500,"reason":"pg handler test"}`))
	e.addAdminSession(t, req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "pg-credit-idempotency")
	first := httptest.NewRecorder()
	e.h.ServeHTTP(first, req)
	if first.Code != http.StatusCreated {
		t.Fatalf("first credit status = %d: %s", first.Code, first.Body.String())
	}
	var firstOut api.AccountCreditResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstOut); err != nil {
		t.Fatalf("decode first credit: %v", err)
	}
	second := e.doAdminWithKey(t, http.MethodPost, path, body, "pg-credit-idempotency")
	if second.Code != http.StatusCreated {
		t.Fatalf("replay status = %d: %s", second.Code, second.Body.String())
	}
	var secondOut api.AccountCreditResponse
	if err := json.Unmarshal(second.Body.Bytes(), &secondOut); err != nil {
		t.Fatalf("decode replay: %v", err)
	}
	if firstOut.ID != secondOut.ID {
		t.Fatalf("replay id = %q, first id = %q", secondOut.ID, firstOut.ID)
	}
	rows, err := e.store.ListAccountCredits(context.Background(), target.ID, false)
	if err != nil {
		t.Fatalf("ListAccountCredits: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("credit rows = %d, want 1", len(rows))
	}
}

// spec: §6.2, §11 — compute-node operator routes must round-trip lifecycle
// state through the same PgStore used by schedd and vmmd.
func TestPGHandler_ComputeNodeRoundTrip(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	e.s.WithAdminAllowlist(e.acct.Email)
	body := map[string]any{
		"name":                 "pg-box-east",
		"target_url":           "tcp://100.64.0.1:50051",
		"vpcpus":               8,
		"mem_mb":               8192,
		"max_concurrency":      16,
		"admission_ceiling_mb": 4096,
	}
	created := e.do(t, http.MethodPost, "/v1/compute-nodes", body, map[string]string{"Content-Type": "application/json"})
	if created.Code != http.StatusOK {
		t.Fatalf("upsert status = %d: %s", created.Code, created.Body.String())
	}
	var out computeNodeResponse
	if err := json.Unmarshal(created.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode upsert: %v", err)
	}
	if out.Name != "pg-box-east" || out.ID == "" || !out.Active {
		t.Fatalf("upsert response = %+v", out)
	}
	listed := e.do(t, http.MethodGet, "/v1/compute-nodes", nil, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", listed.Code, listed.Body.String())
	}
	var nodes []computeNodeResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &nodes); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, node := range nodes {
		if node.Name == "pg-box-east" {
			return
		}
	}
	t.Fatalf("upserted PgStore node missing from list: %+v", nodes)
}
