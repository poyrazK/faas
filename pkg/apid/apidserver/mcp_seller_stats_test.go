package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 20261126082046 — GET /v1/apps/{slug}/mcp-stats reads the app's
// customers over calendar months, pricing this month through the real
// consumer quote, and refuses a window outside 1–12 months.
func TestMCPSellerStats(t *testing.T) {
	withBillingKeys(t)
	e := apidserver.Setup(t, api.PlanHobby)
	apidserver.MustSeedApp(t, e, "search")
	if res := e.Do(t, http.MethodPost, "/v1/apps/search/consumer-plans", api.CreateAPIConsumerPlanRequest{Name: "free",
		MaxRequestsPerMinute: 60, MaxUnitsPerMonth: 1000}, nil); res.Code != http.StatusCreated {
		t.Fatalf("plan: %d %s", res.Code, res.Body)
	}
	if res := e.Do(t, http.MethodPost, "/v1/apps/search/consumers", api.CreateAPIConsumerRequest{ExternalRef: "crm-1", Name: "Ada"}, nil); res.Code != http.StatusCreated {
		t.Fatalf("consumer: %d %s", res.Code, res.Body)
	}

	res := e.Do(t, http.MethodGet, "/v1/apps/search/mcp-stats?months=3", nil, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", res.Code, res.Body)
	}
	var got api.MCPSellerStatsResponse
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	last := got.Months[len(got.Months)-1]
	if got.App != "search" || len(got.Months) != 3 || !last.Current || last.SignedUp != 1 ||
		last.SignedUpBySource[api.MCPSellerSourceOwner] != 1 || got.Customers.Open != 1 || !got.EstimateComplete {
		t.Fatalf("stats = %+v", got)
	}

	for _, months := range []string{"0", "13", "x"} {
		if res := e.Do(t, http.MethodGet, "/v1/apps/search/mcp-stats?months="+months, nil, nil); res.Code != http.StatusUnprocessableEntity {
			t.Errorf("months=%s: %d %s", months, res.Code, res.Body)
		}
	}
	if res := e.Do(t, http.MethodGet, "/v1/apps/missing/mcp-stats", nil, nil); res.Code != http.StatusNotFound {
		t.Errorf("unknown app: %d", res.Code)
	}
}
