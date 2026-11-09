//go:build !no_pg

package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// adr: 127 — a public request ID names one request. Two requests that share
// a trace (a propagated traceparent) each resolve to their own row: get,
// evidence and replay all accept the public ID. A bare-path route filter
// matches the path under every method.
func TestDebugRequestResolutionPG(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	ctx := t.Context()
	store := *e.Store()
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "resolve-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:" + strings.Repeat("1", 64), Scope: "default", Status: state.DeployPending, TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}

	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	start := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Millisecond)
	type request struct {
		publicID, route, method string
		status, latency         int32
		started                 time.Time
	}
	first := request{"a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", "GET /checkout", "GET", 500, 40, start}
	second := request{"b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2", "POST /checkout", "POST", 200, 10, start.Add(2 * time.Second)}
	journal, ok := store.(state.RequestIDJournalWriter)
	if !ok {
		t.Fatal("PgStore does not record the request-ID journal")
	}
	for _, req := range []request{first, second} {
		if err := store.InsertRequestTelemetry(ctx, sqlc.InsertRequestTelemetryParams{
			AccountID: pgtype.UUID{Bytes: uuid.MustParse(app.AccountID), Valid: true}, AppID: pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true},
			DeploymentID: pgtype.UUID{Bytes: uuid.MustParse(dep.ID), Valid: true},
			// The gateway stamps a row when the response ends.
			ReceivedAt: pgtype.Timestamptz{Time: req.started.Add(time.Duration(req.latency) * time.Millisecond), Valid: true},
			Route:      req.route, Method: req.method, Status: req.status, LatencyMs: req.latency, Count: 1,
			TraceID:  pgtype.Text{String: traceID, Valid: true},
			UaFamily: "__unknown__", ReferrerHost: "__none__", Country: "__unknown__",
		}); err != nil {
			t.Fatal(err)
		}
		if err := journal.RecordRequestIDJournal(ctx, state.RequestIDJournalEntry{
			ID: uuid.NewString(), AccountID: app.AccountID, AppID: app.ID, RequestID: req.publicID,
			TraceID: traceID, ReceivedAt: req.started, ExpiresAt: time.Now().Add(24 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}

	get := func(path string, out any) int {
		t.Helper()
		rec := e.Do(t, http.MethodGet, "/v1/apps/"+app.Slug+path, nil, nil)
		if out != nil && rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
				t.Fatalf("decode %s: %v body=%s", path, err, rec.Body)
			}
		}
		return rec.Code
	}
	for _, req := range []request{first, second} {
		var item api.DebugTelemetryRequestItem
		if code := get("/debug/requests/"+req.publicID, &item); code != http.StatusOK ||
			item.RequestID != req.publicID || item.Route != req.route || item.Status != int(req.status) {
			t.Fatalf("get %s = %d %+v; want its own %s %d", req.publicID, code, item, req.route, req.status)
		}
		var evidence api.DebugRequestEvidenceResponse
		if code := get("/debug/requests/"+req.publicID+"/evidence", &evidence); code != http.StatusOK ||
			evidence.Request.Route != req.route || evidence.Request.Status != int(req.status) {
			t.Fatalf("evidence %s = %d %+v; want its own %s", req.publicID, code, evidence.Request, req.route)
		}
	}
	// Replay finds the request by its public ID; with no mirror rule it is
	// refused as unsupported rather than not found.
	rec := e.Do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/debug/requests/"+first.publicID+"/replay", nil, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), api.CodeDebugReplayUnsupported) {
		t.Fatalf("replay by public request ID = %d %s; want 409 %s", rec.Code, rec.Body, api.CodeDebugReplayUnsupported)
	}

	for _, tc := range []struct {
		route string
		want  int
	}{{"/checkout", 2}, {"GET /checkout", 1}, {"/missing", 0}} {
		var page api.DebugTelemetryListResponse
		if code := get("/debug/requests?since=1h&route="+url.QueryEscape(tc.route), &page); code != http.StatusOK || len(page.Requests) != tc.want {
			t.Fatalf("list route=%q = %d with %d rows, want %d", tc.route, code, len(page.Requests), tc.want)
		}
	}
}
