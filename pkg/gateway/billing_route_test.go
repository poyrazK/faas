package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/usageoutbox"
)

// adr: 936 — consumer usage carries its bounded billing route; anonymous
// traffic and the overflow label never do.
func TestHandlerObserveJournalsBillingRouteForConsumers(t *testing.T) {
	cases := []struct {
		name     string
		consumer bool
		route    string
		want     string
	}{
		{"consumer route", true, "POST /generate", "POST /generate"},
		{"overflow label", true, otherRouteLabel, ""},
		{"anonymous discovered", false, "POST /generate", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := usageoutbox.Open(t.TempDir(), 4096)
			if err != nil {
				t.Fatal(err)
			}
			h := &Handler{usageOutbox: q, apiDiscoveryEnabled: true}
			r := consumerUsageTestRequest(true)
			if !tc.consumer {
				// Discovery keeps anonymous traffic journaled; it must still
				// not carry a billing route.
				r = withAppAndAccount(httptest.NewRequest(http.MethodPost, "/generate", nil), uuid.New(), uuid.New())
				r = withAuditRoute(r, tc.route)
			}
			r = withBillingRoute(r, tc.route)
			h.observe(r, http.StatusOK, appIDFromContext(r.Context()).String(), string(api.PlanPro), false, Target{})
			item, ok, err := q.Next()
			if err != nil || !ok {
				t.Fatalf("usage item ok=%t err=%v", ok, err)
			}
			if item.Event.BillingRoute != tc.want {
				t.Fatalf("billing route = %q, want %q", item.Event.BillingRoute, tc.want)
			}
		})
	}
}

func TestBillingRouteSetIsBoundedPerApp(t *testing.T) {
	h := &Handler{}
	set := h.billingRouteSetFor("app-a")
	if h.billingRouteSetFor("app-a") != set || h.billingRouteSetFor("app-b") == set {
		t.Fatal("billing route sets must be stable per app and separate across apps")
	}
	if got := set.admit("GET /items/{id}"); got != "GET /items/{id}" {
		t.Fatalf("first label admitted as %q", got)
	}
}
