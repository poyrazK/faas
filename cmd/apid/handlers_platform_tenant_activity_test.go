package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type platformTenantActivityTestStore struct {
	state.Store
	state.PlatformTenantStore
	rows []sqlc.ListRequestTelemetryByPlatformTenantRow
	args []sqlc.ListRequestTelemetryByPlatformTenantParams
}

func (s *platformTenantActivityTestStore) ListRequestTelemetryByPlatformTenant(_ context.Context, arg sqlc.ListRequestTelemetryByPlatformTenantParams) ([]sqlc.ListRequestTelemetryByPlatformTenantRow, error) {
	s.args = append(s.args, arg)
	start := 0
	if arg.CursorID.Valid {
		start = 1
	}
	if start >= len(s.rows) {
		return []sqlc.ListRequestTelemetryByPlatformTenantRow{}, nil
	}
	rows := append([]sqlc.ListRequestTelemetryByPlatformTenantRow(nil), s.rows[start:]...)
	if int(arg.Limit) < len(rows) {
		rows = rows[:arg.Limit]
	}
	return rows, nil
}

func TestListPlatformTenantActivityPagesTenantScopedTelemetry(t *testing.T) {
	e := setup(t, api.PlanPro)
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "customer-activity", "Customer Activity", 100)
	if err != nil {
		t.Fatal(err)
	}
	appID, deploymentID := uuid.New(), uuid.New()
	firstAt := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Microsecond)
	store := &platformTenantActivityTestStore{
		Store: e.store, PlatformTenantStore: e.store,
		rows: []sqlc.ListRequestTelemetryByPlatformTenantRow{
			platformTenantActivityTestRow(uuid.New(), appID, deploymentID, firstAt, 503, 3),
			platformTenantActivityTestRow(uuid.New(), appID, deploymentID, firstAt.Add(-time.Minute), 503, 4),
		},
	}
	e.s.store = store

	path := "/v1/account/platform-tenants/" + tenant.ID + "/activity?since=2h&app_id=" + appID.String() + "&status=503&limit=1"
	first := e.do(t, http.MethodGet, path, nil, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first page: %d %s", first.Code, first.Body.String())
	}
	var page api.PlatformTenantActivityResponse
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.TenantID != tenant.ID || page.PageComplete || page.NextCursor == "" || page.PageTelemetryRows != 1 || page.PageRepresentedRequests != 3 || page.PageErrorRequests != 3 {
		t.Fatalf("first page summary = %+v", page)
	}
	if len(page.Requests) != 1 || page.Requests[0].AppID != appID.String() || page.Requests[0].Request.Status != 503 || page.Requests[0].Request.Count != 3 {
		t.Fatalf("first page request = %+v", page.Requests)
	}

	query := url.Values{"since": {"2h"}, "app_id": {appID.String()}, "status": {"503"}, "limit": {"1"}, "cursor": {page.NextCursor}}
	second := e.do(t, http.MethodGet, "/v1/account/platform-tenants/"+tenant.ID+"/activity?"+query.Encode(), nil, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second page: %d %s", second.Code, second.Body.String())
	}
	var next api.PlatformTenantActivityResponse
	if err := json.Unmarshal(second.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if !next.PageComplete || next.NextCursor != "" || len(next.Requests) != 1 || next.Requests[0].Request.Count != 4 {
		t.Fatalf("second page = %+v", next)
	}
	if len(store.args) != 2 {
		t.Fatalf("query count = %d, want 2", len(store.args))
	}
	wantAccountID, wantTenantID := uuid.MustParse(e.acct.ID), uuid.MustParse(tenant.ID)
	for _, arg := range store.args {
		if !arg.AccountID.Valid || uuid.UUID(arg.AccountID.Bytes) != wantAccountID || !arg.PlatformTenantID.Valid || uuid.UUID(arg.PlatformTenantID.Bytes) != wantTenantID || arg.AppIDFilter != appID.String() || arg.StatusFilter != 503 || arg.Limit != 2 {
			t.Fatalf("query was not account/tenant/filter scoped: %+v", arg)
		}
	}
	if !store.args[1].CursorReceivedAt.Valid || !store.args[1].CursorID.Valid {
		t.Fatalf("second query did not carry keyset cursor: %+v", store.args[1])
	}

	query.Set("status", "500")
	mismatch := e.do(t, http.MethodGet, "/v1/account/platform-tenants/"+tenant.ID+"/activity?"+query.Encode(), nil, nil)
	if mismatch.Code != http.StatusBadRequest || len(store.args) != 2 {
		t.Fatalf("mismatched cursor status=%d query_count=%d body=%s", mismatch.Code, len(store.args), mismatch.Body.String())
	}
}

func TestListPlatformTenantActivityRejectsInvalidWindowAndFilters(t *testing.T) {
	e := setup(t, api.PlanPro)
	tenant, _, err := e.store.CreatePlatformTenant(context.Background(), e.acct.ID, "customer-activity-invalid", "Customer Activity", 100)
	if err != nil {
		t.Fatal(err)
	}
	store := &platformTenantActivityTestStore{Store: e.store, PlatformTenantStore: e.store}
	e.s.store = store
	for _, rawQuery := range []string{"since=0h", "since=9223372036854775807d", "status=99", "limit=201", "app_id=not-a-uuid"} {
		rec := e.do(t, http.MethodGet, "/v1/account/platform-tenants/"+tenant.ID+"/activity?"+rawQuery, nil, nil)
		if rec.Code != http.StatusBadRequest || len(store.args) != 0 {
			t.Errorf("query %q status=%d queries=%d body=%s", rawQuery, rec.Code, len(store.args), rec.Body.String())
		}
	}
}

func platformTenantActivityTestRow(id uuid.UUID, appID, deploymentID uuid.UUID, at time.Time, status, count int32) sqlc.ListRequestTelemetryByPlatformTenantRow {
	return sqlc.ListRequestTelemetryByPlatformTenantRow{
		ID: pgtype.UUID{Bytes: id, Valid: true}, AppID: pgtype.UUID{Bytes: appID, Valid: true},
		DeploymentID: pgtype.UUID{Bytes: deploymentID, Valid: true}, Route: "GET /orders/{id}",
		Method: "GET", Status: status, LatencyMs: 42, Count: count,
		TraceID:    pgtype.Text{String: "4bf92f3577b34da6a3ce929d0e0e4736", Valid: true},
		ReceivedAt: pgtype.Timestamptz{Time: at, Valid: true}, GuestRuntime: "__unknown__",
		GuestOutcome: "missing", NodeID: "node-1", Region: "eu-west",
	}
}
