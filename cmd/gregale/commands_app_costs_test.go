package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

func TestAppCostsCLIUsesStableAppIdentityAndFiltersAccountReport(t *testing.T) {
	month := time.Now().UTC().Format("2006-01")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fp_live_costs" {
			t.Errorf("authorization header missing")
		}
		switch r.URL.Path {
		case "/v1/apps/my-api":
			if r.Method != http.MethodGet {
				t.Errorf("app request method = %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "my-api"})
		case "/v1/billing/costs":
			if r.Method != http.MethodGet || r.URL.Query().Get("month") != month {
				t.Errorf("cost request = %s %s", r.Method, r.URL)
			}
			_ = json.NewEncoder(w).Encode(api.FinancialCostsResponse{
				Currency: "EUR", PeriodStart: time.Date(time.Now().UTC().Year(), time.Now().UTC().Month(), 1, 0, 0, 0, 0, time.UTC),
				PeriodEnd: time.Now().UTC().AddDate(0, 1, 0), AsOf: time.Now().UTC(), KnownUsageMillicents: 1100,
				Meters: []api.FinancialMeterCosts{{Meter: "compute", Coverage: api.FinancialMeterCoverage{Complete: true, Fresh: true, ExpectedMinutes: 1, CompleteMinutes: 1}, Accrued: financial.ContractCosts{Contracts: []financial.MeterCost{{Price: financial.Price{Unit: "GB-hour"}, Allocations: []financial.Allocation{
					{Attribution: financial.Attribution{AppID: "app-1", Name: "my-api"}, Quantity: 2, NetMillicents: 1000},
					{Attribution: financial.Attribution{AppID: "other-app", Name: "another-api"}, Quantity: 8, NetMillicents: 100},
				}}}}}},
			})
		default:
			t.Errorf("unexpected request path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_costs")
	stdout, restore := captureStdout(t)
	defer restore()
	stderr, restoreErr := captureStderr(t)
	defer restoreErr()

	if code := run([]string{"app", "my-api", "costs", "--month", month, "--json"}); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
	var response api.FinancialAppCostsResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("invalid JSON response %q: %v", stdout, err)
	}
	if response.AppID != "app-1" || response.AppSlug != "my-api" || response.KnownUsageMillicents != 1000 {
		t.Fatalf("unexpected app cost response: %+v", response)
	}
	if len(response.Meters) != 1 || len(response.Meters[0].Allocations) != 1 || response.Meters[0].Allocations[0].Attribution.AppID != "app-1" {
		t.Fatalf("other app allocations leaked into report: %+v", response.Meters)
	}
	if strings.Contains(stdout.String(), "another-api") {
		t.Fatal("other app name leaked into JSON output")
	}
}
