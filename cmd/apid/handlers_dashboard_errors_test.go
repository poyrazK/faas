package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type dashboardErrorsStore struct {
	*state.MemStore
	groups   []state.AppErrorGroup
	requests []state.AppErrorRequestRow
	sample   state.AppErrorSampleRow
}

func (s *dashboardErrorsStore) ListAppErrorGroups(context.Context, sqlc.ListAppErrorGroupsParams) ([]state.AppErrorGroup, error) {
	return s.groups, nil
}

func (s *dashboardErrorsStore) ListAppErrorRequests(context.Context, sqlc.ListAppErrorRequestsParams) ([]state.AppErrorRequestRow, error) {
	return s.requests, nil
}

func (s *dashboardErrorsStore) GetAppErrorSample(context.Context, sqlc.GetAppErrorSampleParams) (state.AppErrorSampleRow, error) {
	return s.sample, nil
}

func TestDashboardHandler_AppErrorsSummaryAndDetail(t *testing.T) {
	_, cookie, base, mgr := newAuthedDashboardServerFullFull(t, "hobby", "errors@example.com")
	acct, err := base.AccountByEmail(t.Context(), "errors@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	_, err = base.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "errors-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	now := time.Now().UTC()
	fingerprint := strings.Repeat("a", 64)
	requestID := uuid.New()
	wrapped := &dashboardErrorsStore{
		MemStore: base,
		groups:   []state.AppErrorGroup{{ID: uuid.New(), Fingerprint: fingerprint, ErrorClass: "upstream_timeout", Route: "GET /api", HTTPStatus: 504, Count: 4, RequestCount: 3, FirstSeenAt: now.Add(-time.Hour), LastSeenAt: now, SampleMessage: "upstream timed out"}},
		requests: []state.AppErrorRequestRow{{ID: uuid.New(), RequestID: requestID, ReceivedAt: now, Route: "GET /api", HTTPStatus: 504, ErrorClass: "upstream_timeout", SampleMessage: "upstream timed out"}},
		sample:   state.AppErrorSampleRow{AppErrorRequestRow: state.AppErrorRequestRow{ID: uuid.New(), RequestID: requestID, ReceivedAt: now.Add(-2 * time.Hour), Route: "GET /api", HTTPStatus: 504, ErrorClass: "upstream_timeout", SampleMessage: "upstream timed out"}, HeadersSample: []byte(`{"content-type":"application/json","x-request-id":"redacted"}`), Redactions: []string{"authorization"}},
	}
	srv := newServerWithDeps(wrapped, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr, nil, 15*time.Minute, "")
	h := srv.handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/errors-app/errors", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("summary code = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"Errors: <code>errors-app</code>", "upstream_timeout", "upstream timed out", "last 24 hours", "Triage"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("summary body missing %q\n%s", want, rec.Body.String())
		}
	}

	detail := httptest.NewRecorder()
	detailReq := httptest.NewRequest(http.MethodGet, "/dashboard/apps/errors-app/errors?fingerprint="+url.QueryEscape(fingerprint), nil)
	detailReq.AddCookie(cookie)
	h.ServeHTTP(detail, detailReq)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail code = %d, want 200\nbody = %s", detail.Code, detail.Body.String())
	}
	body := detail.Body.String()
	for _, want := range []string{"Error detail", fingerprint, "First occurrence", requestID.String(), "content-type", "authorization"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail body missing %q\n%s", want, body)
		}
	}
}

func TestDashboardHandler_AppErrorsFreePlanShowsUpgrade(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "free-errors", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/"+app.Slug+"/errors", nil)
	req.AddCookie(cookie)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "available on Hobby") || !strings.Contains(rec.Body.String(), "/dashboard/pricing") {
		t.Fatalf("free-plan page = %d %s", rec.Code, rec.Body.String())
	}
}

func TestParseAppErrorsPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want string
		ok   bool
	}{
		{path: "errors-app/errors", want: "errors-app", ok: true},
		{path: "errors-app/errors/", want: "errors-app", ok: true},
		{path: "errors-app", ok: false},
		{path: "errors-app/errors/abc", ok: false},
	} {
		got, ok := parseAppErrorsPath(tc.path)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseAppErrorsPath(%q) = (%q, %v), want (%q, %v)", tc.path, got, ok, tc.want, tc.ok)
		}
	}
}
