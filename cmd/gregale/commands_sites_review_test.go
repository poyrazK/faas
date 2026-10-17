package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const reviewSiteID = "11111111-1111-4111-8111-111111111111"
const reviewReleaseID = "22222222-2222-4222-8222-222222222222"

func reviewCLI(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	t.Setenv("FAAS_FRONTEND_HOSTING_ENABLED", "1")
	t.Setenv("FAAS_TOKEN", "operator-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer operator-token" {
			t.Error("missing operator authentication")
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
}

func TestSitesReviewedPromotionUsesSuppliedRevisionWithoutReadOrRetry(t *testing.T) {
	// adr: 202610161920 — promotion must never refresh a reviewed revision.
	for _, status := range []int{202, 409, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/v1/internal/hosting/sites/"+reviewSiteID+"/promotions" {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				}
				var req api.PromoteHostingReleaseRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatal(err)
				}
				if req.ExpectedRevision != 7 || req.ReleaseID != reviewReleaseID {
					t.Errorf("replaced reviewed intent %+v", req)
				}
				w.WriteHeader(status)
				if status == 202 {
					_ = json.NewEncoder(w).Encode(api.HostingSite{ID: reviewSiteID, Revision: 8, ActiveReleaseID: reviewReleaseID})
				} else {
					_ = json.NewEncoder(w).Encode(api.NewProblem(status, "hosting_release_conflict", "Refresh required", ""))
				}
			})
			code := cmdSites([]string{"promote", "--site", reviewSiteID, "--release", reviewReleaseID, "--expected-revision", "7"})
			if calls != 1 || (code == 0) != (status == 202) {
				t.Fatalf("calls=%d exit=%d", calls, code)
			}
		})
	}
}

func TestSitesReviewDiffIsReadOnlyAndReportsBaseline(t *testing.T) {
	var output bytes.Buffer
	previousOutput := osStdout
	osStdout = &output
	defer func() { osStdout = previousOutput }()
	calls := 0
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" {
			t.Fatal("comparison mutated hosting")
		}
		if strings.HasSuffix(r.URL.Path, "/releases") {
			_ = json.NewEncoder(w).Encode([]api.HostingRelease{{ID: reviewReleaseID, SiteID: reviewSiteID, State: "ready", Manifest: api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", SHA256: strings.Repeat("a", 64), Size: 4, ContentType: "text/html"}}}}})
		} else {
			_ = json.NewEncoder(w).Encode(api.HostingSite{ID: reviewSiteID, Status: "active", Revision: 4})
		}
	})
	if code := cmdSites([]string{"diff", "--site", reviewSiteID, "--release", reviewReleaseID}); code != 0 || calls != 2 {
		t.Fatalf("calls=%d exit=%d", calls, code)
	}
	var review api.HostingReleaseReview
	if err := json.Unmarshal(output.Bytes(), &review); err != nil || review.ExpectedRevision != 4 || review.Added != 1 || review.BaseReleaseID != "" {
		t.Fatalf("review output %+v %v", review, err)
	}
}

func TestSitesReviewRejectsUnsafeOptionsBeforeRequests(t *testing.T) {
	calls := 0
	reviewCLI(t, func(http.ResponseWriter, *http.Request) { calls++ })
	for _, args := range [][]string{
		{"promote", "--site", reviewSiteID, "--release", reviewReleaseID},
		{"promote", "--site", reviewSiteID, "--release", reviewReleaseID, "--expected-revision", "7", "--wait", "--wait-timeout", "20m"},
		{"diff", "--site", reviewSiteID, "--release", reviewReleaseID, "--wait"},
		{"deploy", "--no-promote", "--wait"},
		{"deploy", "--wait", "--wait-timeout", "0s"},
	} {
		if code := cmdSites(args); code == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
	if calls != 0 {
		t.Fatal("invalid options contacted API")
	}
}

func TestSitesWaitRefusesLegacyBeforePromoting(t *testing.T) {
	calls := 0
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" {
			t.Fatal("legacy wait mutated site")
		}
		_ = json.NewEncoder(w).Encode(api.HostingSite{ID: reviewSiteID, Revision: 7})
	})
	if code := cmdSites([]string{"promote", "--site", reviewSiteID, "--release", reviewReleaseID, "--expected-revision", "7", "--wait"}); code == 0 || calls != 1 {
		t.Fatalf("calls=%d exit=%d", calls, code)
	}
}

func TestSitesWaitTimeoutLeavesPromotionCommitted(t *testing.T) {
	var output bytes.Buffer
	previousOutput := osStdout
	osStdout = &output
	defer func() { osStdout = previousOutput }()
	promotions := 0
	reads := 0
	site := api.HostingSite{ID: reviewSiteID, Status: "active", OriginID: "origin", Revision: 7, DurableRouting: true}
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST":
			promotions++
			site.Revision = 8
			site.ActiveReleaseID = reviewReleaseID
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(site)
		case strings.HasSuffix(r.URL.Path, "/status"):
			reads++
			_ = json.NewEncoder(w).Encode(api.HostingSiteStatus{Site: site, Publication: api.HostingPublicationStatus{State: "pending", Generation: 3, PublishedGeneration: 2}})
		default:
			_ = json.NewEncoder(w).Encode(site)
		}
	})
	start := time.Now()
	code := cmdSites([]string{"promote", "--site", reviewSiteID, "--release", reviewReleaseID, "--expected-revision", "7", "--wait", "--wait-timeout", "200ms"})
	if code == 0 || promotions != 1 || reads != 1 || site.ActiveReleaseID != reviewReleaseID || time.Since(start) > 2*time.Second {
		t.Fatalf("exit=%d promotions=%d reads=%d", code, promotions, reads)
	}
	var out api.HostingPublicationObservation
	if err := json.Unmarshal(output.Bytes(), &out); err != nil || out.WaitState != "timeout" || out.Site.ActiveReleaseID != reviewReleaseID {
		t.Fatalf("timeout output %+v %v", out, err)
	}
}
