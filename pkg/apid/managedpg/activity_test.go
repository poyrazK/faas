package managedpg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestParseActivitySince(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		raw  string
		want time.Time
		ok   bool
	}{
		{"", now.Add(-24 * time.Hour), true},
		{"7d", now.Add(-7 * 24 * time.Hour), true},
		{"90m", now.Add(-90 * time.Minute), true},
		{"2026-11-25T00:00:00Z", time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC), true},
		{"2026-11-27T00:00:00Z", time.Time{}, false},
		{"0d", time.Time{}, false},
		{"-1h", time.Time{}, false},
		{"soon", time.Time{}, false},
	} {
		got, ok := ParseActivitySince(tt.raw, now)
		if ok != tt.ok || (ok && !got.Equal(tt.want)) {
			t.Errorf("ParseActivitySince(%q) = %v, %v; want %v, %v", tt.raw, got, ok, tt.want, tt.ok)
		}
	}
}

type fakeActivity struct {
	accountID, databaseID string
	since                 time.Time
}

func (f *fakeActivity) Activity(_ context.Context, accountID, databaseID string, since, now time.Time) (managedpostgres.DatabaseActivity, error) {
	f.accountID, f.databaseID, f.since = accountID, databaseID, since
	started := now.Add(-time.Hour)
	return managedpostgres.DatabaseActivity{Database: managedpostgres.Database{ID: databaseID, Name: "orders"}, Since: since, Until: now,
		Starts:     []managedpostgres.ComputeStart{{StartedAt: started, Cause: managedpostgres.CauseExternal}},
		Causes:     []managedpostgres.ComputeStartCause{{Cause: managedpostgres.CauseExternal, Starts: 1, AwakeSeconds: 3600, FirstStartedAt: started}},
		StartCount: 1, AwakeSeconds: 3600}, nil
}

func TestActivityGet(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	service := &fakeActivity{}
	h := Activity{Service: service, Now: func() time.Time { return now },
		Problem: func(w http.ResponseWriter, err error) { http.Error(w, err.Error(), http.StatusInternalServerError) }}
	get := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/postgres/databases/db-1/activity"+query, nil)
		req.SetPathValue("id", "db-1")
		rec := httptest.NewRecorder()
		h.Get(rec, req, state.Account{ID: "acct-1"})
		return rec
	}
	rec := get("?since=7d")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var view api.ManagedPostgresActivity
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if service.accountID != "acct-1" || service.databaseID != "db-1" || !service.since.Equal(now.Add(-7*24*time.Hour)) {
		t.Fatalf("service called with %+v", service)
	}
	if view.DatabaseName != "orders" || view.Starts != 1 || len(view.ComputeStarts) != 1 || view.ComputeStarts[0].SuspendedAt != nil ||
		len(view.Causes) != 1 || view.Hints == nil || view.DocsURL != api.ManagedPostgresActivityDocsURL {
		t.Fatalf("view = %+v", view)
	}
	if rec := get("?since=soon"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad since: status %d", rec.Code)
	}
	unconfigured := false
	h = Activity{NotConfigured: func(w http.ResponseWriter) { unconfigured = true; w.WriteHeader(http.StatusServiceUnavailable) }}
	if rec := get(""); rec.Code != http.StatusServiceUnavailable || !unconfigured {
		t.Fatalf("unconfigured: status %d", rec.Code)
	}
}
