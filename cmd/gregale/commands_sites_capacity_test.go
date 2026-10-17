package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSitesCapacityIndependentOptInAndResponseValidation(t *testing.T) {
	// adr: 202610171216 — operator snapshots grant no mutation or CI inventory access.
	calls := 0
	bad := false
	reviewCLI(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/internal/hosting/sites/"+reviewSiteID+"/preview-capacity" || r.Header.Get("Idempotency-Key") != "" {
			t.Error("unexpected request", r.Method, r.URL.Path)
		}
		out := api.HostingPreviewCapacity{SiteID: reviewSiteID, Limit: api.HostingPreviewsPerSite, Previews: 2, Reservations: 6, ObservedAt: time.Now().UTC()}
		if bad {
			out.SiteID = reviewReleaseID
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	var out bytes.Buffer
	old := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = old })
	args := []string{"capacity", "--site", reviewSiteID}
	t.Setenv("FAAS_FRONTEND_HOSTING_PREVIEW_RESERVATIONS_ENABLED", "")
	if cmdSites(args) == 0 || calls != 0 {
		t.Fatal("default off")
	}
	t.Setenv("FAAS_FRONTEND_HOSTING_PREVIEW_RESERVATIONS_ENABLED", "1")
	if cmdSites([]string{"capacity", "--site", "bad"}) == 0 || calls != 0 {
		t.Fatal("invalid input reached network")
	}
	if cmdSites(args) != 0 || calls != 1 {
		t.Fatal(out.String())
	}
	out.Reset()
	bad = true
	if cmdSites(args) == 0 || out.Len() != 0 {
		t.Fatal("foreign evidence returned", out.String())
	}
}
