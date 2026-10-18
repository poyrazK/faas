package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestProductPilotCLIHTTPReadsAndEvidence(t *testing.T) {
	f, m, c := newProductPilotFixture(t)
	_ = addCompleteProductPilotJourneys(t, f, &m, c)
	saveProductPilot(t, c.file, m)
	c.output = filepath.Join(filepath.Dir(c.file), "release-pilot-report.json")
	base := "/v1/apps/" + m.App + "/product"
	var mu sync.Mutex
	var reads []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer owner" || r.ContentLength != 0 {
			t.Errorf("pilot sent a mutation or changed authority: %s %s", r.Method, r.URL)
		}
		mu.Lock()
		reads = append(reads, r.URL.Path)
		mu.Unlock()
		var value any
		switch {
		case r.URL.Path == base+"/doctor":
			value = f.doctor
		case r.URL.Path == base:
			if r.URL.Query().Get("revision") != f.catalog.Revision {
				t.Error("catalog revision changed")
			}
			value = f.catalog
		case strings.HasPrefix(r.URL.Path, base+"/operations/") && strings.HasSuffix(r.URL.Path, "/explain"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, base+"/operations/"), "/explain")
			value = f.operations[id]
		case r.URL.Path == base+"/billing/review":
			review := f.reviews[r.URL.Query().Get("tenant_id")]
			if r.URL.Query().Get("period") != review.Period {
				t.Error("billing period changed")
			}
			value = review
		case strings.HasPrefix(r.URL.Path, base+"/help/"):
			value = f.help[strings.TrimPrefix(r.URL.Path, base+"/help/")]
		default:
			t.Errorf("unexpected pilot route %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(value); err != nil {
			t.Error(err)
		}
	}))
	defer srv.Close()
	client := api.NewClient(srv.URL, "owner")
	value, err := runProductCommand(t.Context(), client, c)
	if err != nil {
		t.Fatal(err)
	}
	report, ok := value.(api.ProductPilotReport)
	if !ok || report.HelpState != "observed" || report.JourneyState != "recorded" || productReportExit(report) != 0 || report.QualificationState != "unverified" {
		t.Fatalf("wired pilot report: %+v", value)
	}
	c.verb, c.tenant, c.helpID = "pilot help", m.HelpCases[0].TenantID, m.HelpCases[0].RequestID
	value, err = runProductCommand(t.Context(), client, c)
	if err != nil {
		t.Fatal(err)
	}
	inspected, ok := value.(api.ProductPilotHelpResult)
	if !ok || inspected.Result != "inspection_only" || inspected.RequestID != c.helpID {
		t.Fatalf("wired help inspection: %+v", value)
	}
	mu.Lock()
	count := len(reads)
	mu.Unlock()
	if count != 15 {
		t.Fatalf("unexpected reads: %d", count)
	}
}
