package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// `app h12-py costs` printed "h12-py" on all four rows; each row is a
// deployment of that app (Hunt 12 H12-43).
func TestAppCostsLabelsRowsByDeployment(t *testing.T) {
	main.ResetJSONOut(t)
	now := time.Now().UTC()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/apps/my-api":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-1", Slug: "my-api"})
		case "/v1/billing/costs":
			_ = json.NewEncoder(w).Encode(api.FinancialCostsResponse{
				Currency: "USD", PeriodStart: now.AddDate(0, 0, -1), PeriodEnd: now.AddDate(0, 1, 0), AsOf: now,
				Meters: []api.FinancialMeterCosts{{Meter: "compute", Coverage: api.FinancialMeterCoverage{Complete: true, Fresh: true}, Accrued: financial.ContractCosts{Contracts: []financial.MeterCost{{Price: financial.Price{Unit: "mb_seconds"}, Allocations: []financial.Allocation{
					{Attribution: financial.Attribution{AppID: "app-1", DeploymentID: "dep-one", Name: "my-api"}, Quantity: 3600000},
					{Attribution: financial.Attribution{AppID: "app-1", DeploymentID: "dep-two", Name: "my-api"}, Quantity: 7200000},
				}}}}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	out, restore := main.CaptureStdout(t)
	code := main.Run([]string{"app", "my-api", "costs"})
	restore()
	got := out.String()
	if code != 0 || !strings.Contains(got, "deployment dep-one") || !strings.Contains(got, "deployment dep-two") {
		t.Fatalf("exit %d, want rows labelled by deployment:\n%s", code, got)
	}
}
