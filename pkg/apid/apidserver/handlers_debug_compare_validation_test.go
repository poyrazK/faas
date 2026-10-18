// adr: 1623
package apidserver_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDebugDeploymentCompareValidationAndPlan(t *testing.T) {
	for _, mode := range []string{"invalid_since", "invalid_until", "expired_until", "plan_gated"} {
		t.Run(mode, func(t *testing.T) {
			plan := api.PlanPro
			if mode == "plan_gated" {
				plan = api.PlanFree
			}
			e := apidserver.Setup(t, plan)
			if _, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "compare-validation", Status: state.AppActive}); err != nil {
				t.Fatal(err)
			}
			req := api.DebugCompareRequest{Source: "11111111-1111-4111-8111-111111111111", Mirror: "22222222-2222-4222-8222-222222222222"}
			switch mode {
			case "invalid_since":
				req.Since = "0h"
			case "invalid_until":
				req.Until = "invalid"
			case "expired_until":
				req.Until = time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339Nano)
			}
			rec := e.Do(t, http.MethodPost, "/v1/apps/compare-validation/debug/compare", req, nil)
			status := http.StatusBadRequest
			if mode == "plan_gated" {
				status = http.StatusPaymentRequired
			}
			if rec.Code != status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}
