package appnames

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRefusal(t *testing.T) {
	if p := Refusal(nil, []string{"*-login"}, "acct", "my-shop"); p != nil {
		t.Fatalf("unblocked name refused: %+v", p)
	}
	p := Refusal(nil, []string{"*-login"}, "acct", "bank-login")
	if p == nil || p.Status != http.StatusConflict || p.Code != api.CodeAppNameUnavailable {
		t.Fatalf("got %+v", p)
	}
}

func TestReporterListsLiveAndRestorableApps(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	later, earlier := now.Add(time.Hour), now.Add(-time.Hour)
	live := []state.App{
		{ID: "a1", Slug: "paypal", Status: state.AppActive},
		{ID: "a2", Slug: "bank-login", Status: state.AppActive},
		{ID: "a3", Slug: "my-shop", Status: state.AppActive},
	}
	deleted := []state.App{
		{ID: "d1", Slug: "paypal-login", Status: state.AppDeleted, DeleteGraceUntil: &later},
		{ID: "d2", Slug: "old-login", Status: state.AppDeleted, DeleteGraceUntil: &earlier},
	}
	blocklist := []string{}
	rp := Reporter{
		Blocklist:       func() []string { return blocklist },
		ListApps:        func(context.Context) ([]state.App, error) { return live, nil },
		ListDeletedApps: func(context.Context) ([]state.App, error) { return deleted, nil },
		Now:             func() time.Time { return now },
	}
	report := func(query string, wantStatus int) api.AppNameReportResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		rp.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/app-names/report"+query, nil))
		if rec.Code != wantStatus {
			t.Fatalf("report%s: status %d: %s", query, rec.Code, rec.Body)
		}
		var out api.AppNameReportResponse
		if wantStatus == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}
	if got := report("", http.StatusOK); got.Source != "live" || got.Entries != 0 || len(got.Matches) != 0 {
		t.Fatalf("empty live list: %+v", got)
	}
	// The restorable tombstone is listed; the expired one is not.
	got := report("?blocklist=paypal,*-login", http.StatusOK)
	var slugs []string
	for _, m := range got.Matches {
		slugs = append(slugs, m.Slug+"="+m.Entry)
	}
	if got.Source != "preview" || got.Entries != 2 ||
		fmt.Sprint(slugs) != "[bank-login=*-login paypal=paypal paypal-login=*-login]" {
		t.Fatalf("preview: %+v", got)
	}
	blocklist = []string{"my-*"}
	if got := report("", http.StatusOK); len(got.Matches) != 1 || got.Matches[0].Slug != "my-shop" {
		t.Fatalf("live: %+v", got)
	}
	report("?blocklist=pay_pal", http.StatusBadRequest)
}

// A truncated report holds the first matches by slug, not the newest apps.
func TestReportSortsBeforeTruncating(t *testing.T) {
	apps := make([]state.App, 0, ReportMaxMatches+10)
	for i := ReportMaxMatches + 9; i >= 0; i-- {
		apps = append(apps, state.App{ID: fmt.Sprint(i), Slug: fmt.Sprintf("app-%05d-login", i)})
	}
	got := Report(apps, []string{"*-login"}, "preview")
	if !got.Truncated || len(got.Matches) != ReportMaxMatches ||
		got.Matches[0].Slug != "app-00000-login" || got.Matches[ReportMaxMatches-1].Slug != fmt.Sprintf("app-%05d-login", ReportMaxMatches-1) {
		t.Fatalf("truncated=%v len=%d first=%s", got.Truncated, len(got.Matches), got.Matches[0].Slug)
	}
}
