package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSitesHistoryUsesExplicitCursorAndOptIn(t *testing.T) {
	calls := 0
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/internal/hosting/sites/"+reviewSiteID+"/history" || r.URL.Query().Get("before_revision") != "51" {
			t.Fatal(r.Method, r.URL.String())
		}
		_ = json.NewEncoder(w).Encode(api.HostingHistoryPage{Events: []api.HostingPromotionEvent{}, RetentionLimit: 1000})
	})
	args := []string{"history", "--site", reviewSiteID, "--before-revision", "51"}
	t.Setenv("FAAS_FRONTEND_HOSTING_HISTORY_ENABLED", "")
	if cmdSites(args) == 0 || calls != 0 {
		t.Fatal("history bypassed workstation opt-in")
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_HISTORY_ENABLED", "1")
	if cmdSites(args) != 0 || calls != 1 {
		t.Fatal(calls)
	}
	if cmdSites([]string{"history", "--site", reviewSiteID, "--before-revision", "-1"}) == 0 || calls != 1 {
		t.Fatal("invalid cursor reached API")
	}
}
func TestSitesDeployProvenanceIsExplicitAndNotPublishedInManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("home"), 0600); err != nil {
		t.Fatal(err)
	}
	created := false
	capsEnabled := false
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/internal/hosting/sites/" + reviewSiteID:
			_ = json.NewEncoder(w).Encode(api.HostingSite{ID: reviewSiteID, Revision: 1})
		case "/v1/internal/hosting/capabilities":
			_ = json.NewEncoder(w).Encode(api.HostingCapabilities{HistoryEnabled: capsEnabled, StagedReleasesSupported: true})
		case "/v1/internal/hosting/sites/" + reviewSiteID + "/releases":
			if r.Method != "POST" {
				t.Fatal(r.Method)
			}
			var req api.CreateHostingReleaseRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if req.Provenance == nil || req.Provenance.CommitSHA != strings.Repeat("a", 40) || req.Provenance.Repository != "org/site" {
				t.Fatal(req)
			}
			raw, _ := json.Marshal(req.HostingManifest)
			if strings.Contains(string(raw), "provenance") {
				t.Fatal("source data entered manifest")
			}
			created = true
			w.WriteHeader(503)
			_ = json.NewEncoder(w).Encode(api.NewProblem(503, "provider_unavailable", "Unavailable", ""))
		default:
			t.Fatal("unexpected request", r.URL.Path)
		}
	})
	args := []string{"deploy", "--site", reviewSiteID, "--path", dir, "--no-promote", "--source-repository", "org/site", "--source-commit", strings.Repeat("a", 40)}
	t.Setenv("FAAS_FRONTEND_HOSTING_HISTORY_ENABLED", "")
	if cmdSites(args) == 0 || created {
		t.Fatal("missing workstation opt-in")
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_HISTORY_ENABLED", "1")
	if cmdSites(args) == 0 || created {
		t.Fatal("API capability not checked before create")
	}
	capsEnabled = true
	if cmdSites(args) == 0 || !created {
		t.Fatal("source intent missing")
	}
}
