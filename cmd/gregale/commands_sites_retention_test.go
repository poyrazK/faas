package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestSitesRetentionPlansAreReadOnlyAndUpdatesKeepReviewedRevision(t *testing.T) {
	// adr: 202610171001 — cleanup planning carries no mutation authority.
	var methods, paths []string
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/capabilities") {
			_ = json.NewEncoder(w).Encode(api.HostingCapabilities{RetentionEnabled: true})
			return
		}
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.RequestURI())
		if r.Method == "PUT" {
			var req api.SetHostingRetentionRequest
			if json.NewDecoder(r.Body).Decode(&req) != nil || req.ExpectedRevision != 7 || req.Enabled || r.Header.Get("Idempotency-Key") != "" {
				t.Error("unreviewed/memoized policy", req)
			}
			_ = json.NewEncoder(w).Encode(api.HostingRetentionPolicy{Revision: 8})
			return
		}
		_ = json.NewEncoder(w).Encode(api.HostingCleanupPlan{Policy: api.HostingRetentionPolicy{Revision: 7}})
	})
	t.Setenv("FAAS_FRONTEND_HOSTING_RETENTION_ENABLED", "1")
	for _, args := range [][]string{{"cleanup", "--site", reviewSiteID, "--pending-age", "1h", "--staged-age", "2h"}, {"retention", "show", "--site", reviewSiteID}, {"retention", "set", "--site", reviewSiteID, "--enabled=false", "--expected-revision", "7", "--pending-age", "24h", "--staged-age", "24h"}} {
		if main.CmdSites(args) != 0 {
			t.Fatal("retention workflow failed", args)
		}
	}
	if strings.Join(methods, ",") != "GET,GET,PUT" || !strings.Contains(paths[0], "pending_age_seconds=3600") || !strings.Contains(paths[0], "staged_age_seconds=7200") {
		t.Fatal(methods, paths)
	}
}
func TestSitesRetentionRequiresExplicitIntentBeforeMutating(t *testing.T) {
	calls := 0
	reviewCLI(t, func(http.ResponseWriter, *http.Request) { calls++ })
	t.Setenv("FAAS_FRONTEND_HOSTING_RETENTION_ENABLED", "1")
	for _, args := range [][]string{{"retention", "set", "--site", reviewSiteID}, {"retention", "set", "--site", reviewSiteID, "--enabled=true"}, {"retention", "set", "--site", reviewSiteID, "--enabled=true", "--expected-revision", "0", "--pending-age", "1s"}, {"cleanup", "--site", reviewSiteID, "--enabled=true"}} {
		if main.CmdSites(args) == 0 {
			t.Fatal("implicit intent accepted", args)
		}
	}
	if calls != 0 {
		t.Fatal("invalid intent reached API", calls)
	}
}
