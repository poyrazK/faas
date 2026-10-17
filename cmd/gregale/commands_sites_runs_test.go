package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hosting/activity"
)

func TestSitesRunsReadsOnlyTheExplicitPageWithIndependentOptIn(t *testing.T) {
	// adr: 202610171147 — listing does not select, retry, promote or mutate an intent.
	cursor := activity.Cursor(time.Date(2026, 10, 17, 11, 47, 0, 0, time.UTC), reviewReleaseID)
	calls := 0
	badResponse := false
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/internal/hosting/sites/"+reviewSiteID+"/runs" || r.URL.Query().Get("before") != cursor || r.Header.Get("Idempotency-Key") != "" {
			t.Error("unexpected operation", r.Method, r.URL.Path)
		}
		page := api.HostingActivityPage{SiteID: reviewSiteID, ServingHealth: "not_checked", Entries: []api.HostingActivityEntry{}, ObservedAt: time.Now().UTC()}
		if badResponse {
			page.SiteID = reviewReleaseID
		}
		_ = json.NewEncoder(w).Encode(page)
	})
	var out bytes.Buffer
	old := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = old })
	args := []string{"runs", "--site", reviewSiteID, "--before", cursor}
	t.Setenv("FAAS_FRONTEND_HOSTING_ACTIVITY_ENABLED", "")
	if cmdSites(args) == 0 || calls != 0 {
		t.Fatal("default off")
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_ACTIVITY_ENABLED", "1")
	for _, invalid := range [][]string{{"runs", "--site", "bad"}, {"runs", "--site", reviewSiteID, "--before", "bad"}, {"runs", "--site", reviewSiteID, "unexpected"}} {
		if cmdSites(invalid) == 0 || calls != 0 {
			t.Fatal("invalid input reached API")
		}
	}
	if cmdSites(args) != 0 || calls != 1 {
		t.Fatal("page read", out.String())
	}
	var page api.HostingActivityPage
	if json.Unmarshal(out.Bytes(), &page) != nil || page.SiteID != reviewSiteID || page.ServingHealth != "not_checked" {
		t.Fatal(out.String())
	}
	out.Reset()
	badResponse = true
	if cmdSites(args) == 0 || out.Len() != 0 {
		t.Fatal("foreign/partial report accepted", out.String())
	}
}
