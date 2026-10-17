package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSitesPreviewsRequireOptInAndReviewedGeneration(t *testing.T) {
	calls := 0
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "DELETE" {
			t.Error(r.Method)
		}
		var req api.RevokeHostingPreviewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.ExpectedGeneration != 1 {
			t.Fatal(req)
		}
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(api.NewProblem(409, "hosting_release_conflict", "Refresh first", ""))
	})
	args := []string{"previews", "revoke", "--site", reviewSiteID, "--preview", reviewReleaseID, "--expected-generation", "1"}
	t.Setenv("FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED", "")
	if cmdSites(args) == 0 || calls != 0 {
		t.Fatal("missing preview flag reached API")
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED", "1")
	for _, invalid := range [][]string{{"previews", "create", "--site", reviewSiteID, "--release", reviewReleaseID, "--ttl", "73h"}, {"previews", "revoke", "--site", reviewSiteID, "--preview", reviewReleaseID}, {"previews", "list", "--site", reviewSiteID, "--release", reviewReleaseID}} {
		if cmdSites(invalid) == 0 {
			t.Fatal("accepted unsafe options", invalid)
		}
	}
	if calls != 0 {
		t.Fatal("invalid options made API calls")
	}
	if cmdSites(args) == 0 || calls != 1 {
		t.Fatal("revoke conflict was retried", calls)
	}
}
func TestSitesPreviewsCreateBindsReleaseAndTTLWithoutPromotion(t *testing.T) {
	calls := 0
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/internal/hosting/sites/"+reviewSiteID+"/previews" {
			t.Fatal(r.Method, r.URL.Path)
		}
		var req api.CreateHostingPreviewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.ReleaseID != reviewReleaseID || req.TTLSeconds != 300 {
			t.Fatal(req)
		}
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(api.HostingPreview{ID: reviewReleaseID})
	})
	t.Setenv("FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED", "1")
	if cmdSites([]string{"previews", "create", "--site", reviewSiteID, "--release", reviewReleaseID, "--ttl", "5m"}) != 0 || calls != 1 {
		t.Fatal("preview creation changed production", calls)
	}
}
