package dataapiusage

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	usage "github.com/onebox-faas/faas/pkg/state/dataapiusage"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

var now = time.Date(2026, 10, 19, 10, 30, 0, 0, time.UTC)

type fakeStore struct {
	account, app, report string
	hours                []usage.Hour
	access               []byte
	calls                int
	err                  error
	stored               []usage.Hour
}

func (s *fakeStore) Record(_ context.Context, account, app, report string, hours []usage.Hour, access []byte, _ time.Time) (usage.MonthToDate, error) {
	s.calls++
	s.account, s.app, s.report, s.hours, s.access = account, app, report, hours, access
	return usage.MonthToDate{Month: usage.MonthStart(now), Requests: 12, AnonymousRequests: 5}, s.err
}

func (s *fakeStore) Hours(_ context.Context, _ string, from, to time.Time) ([]usage.Hour, error) {
	var out []usage.Hour
	for _, h := range s.stored {
		if !h.Hour.Before(from) && h.Hour.Before(to) {
			out = append(out, h)
		}
	}
	return out, s.err
}

func (s *fakeStore) MonthToDate(context.Context, string, time.Time) (usage.MonthToDate, error) {
	return usage.MonthToDate{Month: usage.MonthStart(now), Requests: 30, AnonymousRequests: 9}, s.err
}

func signer(t *testing.T) (*workloadidentity.Signer, *workloadidentity.Verifier) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "usage-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	v, err := workloadidentity.NewVerifier(s.JWKS(), workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	return s, v
}

func report(t *testing.T, id string, hours ...api.DataAPIUsageHour) string {
	t.Helper()
	body, err := json.Marshal(api.DataAPIUsageReport{Version: 1, ReportID: id, Hours: hours})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestReportHandlerAuthenticatesAndBoundsReports(t *testing.T) {
	s, verifier := signer(t)
	account, app, instance := uuid.NewString(), uuid.NewString(), uuid.NewString()
	token, err := s.Mint(now, account, app, instance, workloadidentity.DataAPIUsageAudience)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := s.Mint(now, account, app, instance, workloadidentity.DataAPIConnectionsAudience)
	if err != nil {
		t.Fatal(err)
	}
	hour := now.Truncate(time.Hour)
	valid := report(t, "i-1:7", api.DataAPIUsageHour{Hour: hour, DataAPIUsageCounts: api.DataAPIUsageCounts{AnonymousRequests: 3, RowsReturned: 9}})
	for _, tc := range []struct {
		name, bearer, body string
		status             int
	}{
		{"valid", token.AccessToken, valid, 200},
		{"empty report", token.AccessToken, report(t, "i-1:8"), 200},
		{"connections token", wrong.AccessToken, valid, 401},
		{"no token", "", valid, 401},
		{"unknown field", token.AccessToken, strings.Replace(valid, `"version":1`, `"version":1,"extra":true`, 1), 400},
		{"version", token.AccessToken, strings.Replace(valid, `"version":1`, `"version":2`, 1), 400},
		{"report id", token.AccessToken, report(t, "bad id"), 400},
		{"unaligned", token.AccessToken, report(t, "a", api.DataAPIUsageHour{Hour: hour.Add(time.Minute)}), 400},
		{"too old", token.AccessToken, report(t, "a", api.DataAPIUsageHour{Hour: hour.Add(-49 * time.Hour)}), 400},
		{"future", token.AccessToken, report(t, "a", api.DataAPIUsageHour{Hour: hour.Add(2 * time.Hour)}), 400},
		{"duplicate hour", token.AccessToken, report(t, "a", api.DataAPIUsageHour{Hour: hour}, api.DataAPIUsageHour{Hour: hour}), 400},
		{"negative", token.AccessToken, report(t, "a", api.DataAPIUsageHour{Hour: hour, DataAPIUsageCounts: api.DataAPIUsageCounts{RowsReturned: -1}}), 400},
		{"huge", token.AccessToken, report(t, "a", api.DataAPIUsageHour{Hour: hour, DataAPIUsageCounts: api.DataAPIUsageCounts{ResponseBytes: api.DataAPIUsageMaxDelta + 1}}), 400},
		{"two values", token.AccessToken, valid + valid, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{}
			h := ReportHandler{Store: store, Verifier: verifier, Now: func() time.Time { return now }}
			req := httptest.NewRequest(http.MethodPost, "/v1/runtime/data-api/usage", strings.NewReader(tc.body))
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("reports must not be cached")
			}
			if tc.status != 200 {
				if store.calls != 0 {
					t.Fatal("a refused report reached the store")
				}
				return
			}
			if store.account != account || store.app != app {
				t.Fatalf("authority came from somewhere other than the token: %+v", store)
			}
			var out api.DataAPIUsageReportResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.MonthToDate != (api.DataAPIUsageMonthToDate{Month: "2026-10", Requests: 12, AnonymousRequests: 5}) {
				t.Fatalf("response = %+v (%v)", out, err)
			}
		})
	}
}

func TestReportHandlerMapsStoreFailures(t *testing.T) {
	s, verifier := signer(t)
	token, _ := s.Mint(now, uuid.NewString(), uuid.NewString(), uuid.NewString(), workloadidentity.DataAPIUsageAudience)
	for err, status := range map[error]int{usage.ErrNotFound: 404, errors.New("db down"): 503} {
		h := ReportHandler{Store: &fakeStore{err: err}, Verifier: verifier, Now: func() time.Time { return now }}
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(report(t, "r")))
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != status || strings.Contains(rec.Body.String(), "db down") {
			t.Fatalf("%v: status = %d body = %s", err, rec.Code, rec.Body)
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(report(t, "r")))
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	ReportHandler{Verifier: verifier, Now: func() time.Time { return now }}.ServeHTTP(rec, req)
	if rec.Code != 503 {
		t.Fatalf("without a store: %d", rec.Code)
	}
}

func TestQueryHandlerZeroFillsBucketsAndTotals(t *testing.T) {
	hour := now.Truncate(time.Hour)
	store := &fakeStore{stored: []usage.Hour{
		{Hour: hour.Add(-30 * time.Hour), Counts: usage.Counts{UserRequests: 1}},
		{Hour: hour.Add(-2 * time.Hour), Counts: usage.Counts{AnonymousRequests: 4, RowsReturned: 10}},
		{Hour: hour, Counts: usage.Counts{ServerRequests: 2, CappedRequests: 1}},
	}}
	get := func(query string) (int, api.DataAPIUsage) {
		rec := httptest.NewRecorder()
		QueryHandler{Store: store, Now: func() time.Time { return now }}.Serve(rec, httptest.NewRequest(http.MethodGet, "/v1/apps/shop-data/data-api/usage"+query, nil), "app-id", "shop-data")
		var out api.DataAPIUsage
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	status, day := get("?since=24h")
	if status != 200 || day.Granularity != "hour" || len(day.Buckets) != 25 || day.App != "shop-data" {
		t.Fatalf("24h view: %d %+v", status, day)
	}
	if day.Totals.AnonymousRequests != 4 || day.Totals.ServerRequests != 2 || day.Totals.UserRequests != 0 || day.Buckets[24].CappedRequests != 1 || day.Buckets[22].RowsReturned != 10 {
		t.Fatalf("24h buckets/totals: %+v", day)
	}
	if day.MonthToDate != (api.DataAPIUsageMonthToDate{Month: "2026-10", Requests: 30, AnonymousRequests: 9}) {
		t.Fatalf("month to date = %+v", day.MonthToDate)
	}
	status, week := get("")
	if status != 200 || week.Granularity != "day" || len(week.Buckets) != 8 || week.Totals.UserRequests != 1 || !week.From.Equal(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("default view: %d %+v", status, week)
	}
	for _, query := range []string{"?since=91d", "?since=0d", "?since=7w", "?since=3d&granularity=hour", "?granularity=minute", "?since=1d&since=2d", "?other=1"} {
		if status, _ := get(query); status != 400 {
			t.Fatalf("%s accepted: %d", query, status)
		}
	}
}

func TestReportHandlerStoresOnlyValidAccessSettings(t *testing.T) {
	s, verifier := signer(t)
	token, _ := s.Mint(now, uuid.NewString(), uuid.NewString(), uuid.NewString(), workloadidentity.DataAPIUsageAudience)
	hash := strings.Repeat("ab", 32)
	send := func(access *api.DataAPIAccessConfig) (int, *fakeStore) {
		body, _ := json.Marshal(api.DataAPIUsageReport{Version: 1, ReportID: "r", Access: access})
		store := &fakeStore{}
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer "+token.AccessToken)
		rec := httptest.NewRecorder()
		ReportHandler{Store: store, Verifier: verifier, Now: func() time.Time { return now }}.ServeHTTP(rec, req)
		return rec.Code, store
	}
	readOnly := false
	valid := &api.DataAPIAccessConfig{Version: 1, Schemas: []string{"api"}, PublicRead: []string{"api.products"},
		ServerKeys: []api.DataAPIServerKey{{Slot: "a", SHA256: hash, Permissions: &api.DataAPIServerKeyPermissionSet{Access: "read", Relations: []string{"api.orders"}, Subjects: &readOnly}}}}
	status, store := send(valid)
	if status != 200 || !strings.Contains(string(store.access), `"public_read":["api.products"]`) || !strings.Contains(string(store.access), hash) {
		t.Fatalf("valid settings: %d %s", status, store.access)
	}
	if status, store = send(nil); status != 200 || store.access != nil {
		t.Fatal("a report without settings must keep the stored ones")
	}
	for name, bad := range map[string]*api.DataAPIAccessConfig{
		"version":   {Version: 2, Schemas: []string{"api"}},
		"schemas":   {Version: 1},
		"relation":  {Version: 1, Schemas: []string{"api"}, PublicRead: []string{"products"}},
		"slot":      {Version: 1, Schemas: []string{"api"}, ServerKeys: []api.DataAPIServerKey{{Slot: "c", SHA256: hash}}},
		"hash":      {Version: 1, Schemas: []string{"api"}, ServerKeys: []api.DataAPIServerKey{{Slot: "a", SHA256: "ABCD"}}},
		"duplicate": {Version: 1, Schemas: []string{"api"}, ServerKeys: []api.DataAPIServerKey{{Slot: "a", SHA256: hash}, {Slot: "a", SHA256: hash}}},
		"access":    {Version: 1, Schemas: []string{"api"}, ServerKeys: []api.DataAPIServerKey{{Slot: "a", SHA256: hash, Permissions: &api.DataAPIServerKeyPermissionSet{Access: "admin"}}}},
	} {
		if status, store := send(bad); status != 400 || store.calls != 0 {
			t.Fatalf("%s: status %d calls %d", name, status, store.calls)
		}
	}
}
