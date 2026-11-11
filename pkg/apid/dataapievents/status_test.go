package dataapievents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/dataapiusage"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type fakeReports struct {
	account, app string
	report       api.DataAPIChangeHookReport
	err          error
}

func (r *fakeReports) RecordChangeHooks(_ context.Context, accountID, appID string, report api.DataAPIChangeHookReport, _ time.Time) error {
	r.account, r.app, r.report = accountID, appID, report
	return r.err
}

func (f *fixture) serveStatus(reports ReportStore, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/runtime/data-api/change-status", strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	StatusHandler{Verifier: f.handler.Verifier, Store: f.store, Reports: reports, Now: func() time.Time { return now }}.ServeHTTP(w, r)
	return w
}

// A live runtime reports delivery for its own app, under its events identity.
func TestStatusHandlerStoresTheAppsOwnReport(t *testing.T) {
	f := newFixture(t)
	reports := &fakeReports{}
	body := `{"version":1,"pending":3,"failed":1,"oldest_pending_at":"2026-10-22T11:00:00Z","last_published_at":"2026-10-22T11:59:00Z","last_failure_code":"event_schema_invalid","publishing":"retrying"}`
	w := f.serveStatus(reports, f.token(t, workloadidentity.DataAPIEventsAudience), body)
	if w.Code != http.StatusNoContent || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if reports.app != f.store.app.ID || reports.account != f.store.app.AccountID || reports.report.Pending != 3 || reports.report.Failed != 1 ||
		reports.report.Publishing != api.DataAPIChangeHooksPublishingRetrying || reports.report.LastFailureCode != "event_schema_invalid" {
		t.Fatalf("stored %+v", reports)
	}
}

func TestStatusHandlerRefusesOtherIdentitiesAndBadReports(t *testing.T) {
	f := newFixture(t)
	reports := &fakeReports{}
	valid := `{"version":1,"pending":0,"failed":0,"publishing":"ok"}`
	for name, tc := range map[string]struct {
		token, body string
		want        int
	}{
		"no token":           {"", valid, http.StatusUnauthorized},
		"usage audience":     {f.token(t, workloadidentity.DataAPIUsageAudience), valid, http.StatusUnauthorized},
		"unknown field":      {f.token(t, workloadidentity.DataAPIEventsAudience), `{"version":1,"pending":0,"failed":0,"publishing":"ok","rows":[]}`, http.StatusBadRequest},
		"version":            {f.token(t, workloadidentity.DataAPIEventsAudience), `{"version":2,"pending":0,"failed":0,"publishing":"ok"}`, http.StatusBadRequest},
		"negative":           {f.token(t, workloadidentity.DataAPIEventsAudience), `{"version":1,"pending":-1,"failed":0,"publishing":"ok"}`, http.StatusBadRequest},
		"pending w/o oldest": {f.token(t, workloadidentity.DataAPIEventsAudience), `{"version":1,"pending":2,"failed":0,"publishing":"ok"}`, http.StatusBadRequest},
		"future":             {f.token(t, workloadidentity.DataAPIEventsAudience), `{"version":1,"pending":1,"failed":0,"oldest_pending_at":"2026-10-23T12:00:00Z","publishing":"ok"}`, http.StatusBadRequest},
		"code":               {f.token(t, workloadidentity.DataAPIEventsAudience), `{"version":1,"pending":0,"failed":1,"last_failure_code":"Bad Code","publishing":"ok"}`, http.StatusBadRequest},
		"publishing":         {f.token(t, workloadidentity.DataAPIEventsAudience), `{"version":1,"pending":0,"failed":0,"publishing":"stuck"}`, http.StatusBadRequest},
		"two values":         {f.token(t, workloadidentity.DataAPIEventsAudience), valid + valid, http.StatusBadRequest},
		"oversized":          {f.token(t, workloadidentity.DataAPIEventsAudience), `{"version":1,"pending":0,"failed":0,"publishing":"ok"` + strings.Repeat(" ", api.DataAPIChangeHookReportMaxBytes) + `}`, http.StatusBadRequest},
	} {
		if w := f.serveStatus(reports, tc.token, tc.body); w.Code != tc.want {
			t.Errorf("%s: status %d, want %d: %s", name, w.Code, tc.want, w.Body)
		}
	}
	if reports.app != "" {
		t.Fatalf("a refused report was stored: %+v", reports)
	}
}

func TestStatusHandlerWithoutTrustOrStore(t *testing.T) {
	f := newFixture(t)
	valid := `{"version":1,"pending":0,"failed":0,"publishing":"ok"}`
	if w := f.serveStatus(nil, f.token(t, workloadidentity.DataAPIEventsAudience), valid); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("without a report store: %d", w.Code)
	}
	if w := f.serveStatus(&fakeReports{err: dataapiusage.ErrNotFound}, f.token(t, workloadidentity.DataAPIEventsAudience), valid); w.Code != http.StatusUnauthorized {
		t.Fatalf("a deleted app: %d", w.Code)
	}
	f.store.instance.State = string(state.StateParked)
	if w := f.serveStatus(&fakeReports{}, f.token(t, workloadidentity.DataAPIEventsAudience), valid); w.Code != http.StatusUnauthorized {
		t.Fatalf("a parked instance: %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/runtime/data-api/change-status", strings.NewReader(valid))
	w := httptest.NewRecorder()
	StatusHandler{Store: f.store, Reports: &fakeReports{}}.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("without workload trust: %d", w.Code)
	}
}
