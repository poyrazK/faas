package capgate

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/productcap"
)

// adr: 20261123074615 — core realtime is beta and its children stay preview.
// The gate picks the longest catalog route, so core routes nested under a
// preview child's prefix must be claimed by `realtime` or every account
// outside the preview would meet them as unregistered.
func TestRealtimeRouteOwnershipFollowsBetaSplit(t *testing.T) {
	catalog, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	g := New(catalog, nil)
	cases := map[string]string{
		"/v1/apps/shop/realtime/endpoints":                                     "realtime",
		"/v1/apps/shop/realtime/endpoints/ep":                                  "realtime",
		"/v1/apps/shop/realtime/endpoints/ep/channels/lobby/publish":           "realtime",
		"/v1/apps/shop/realtime/endpoints/ep/principals:send":                  "realtime",
		"/v1/apps/shop/realtime/endpoints/ep/principals/messages/m1/receipt":   "realtime",
		"/v1/apps/shop/realtime/endpoints/ep/connections":                      "realtime-connections",
		"/v1/apps/shop/realtime/endpoints/ep/connections/c1/send":              "realtime-connections",
		"/v1/apps/shop/realtime/endpoints/ep/channels/lobby/publish-batch":     "realtime-channels",
		"/v1/apps/shop/realtime/endpoints/ep/channels/lobby/groups":            "realtime-channels",
		"/v1/apps/shop/realtime/endpoints/ep/channels/lobby/retained-messages": "realtime-channels",
		"/v1/apps/shop/realtime/endpoints/ep/channels/lobby/signals":           "realtime-channels",
		"/v1/apps/shop/realtime/endpoints/ep/principals/user-1/access":         "realtime-access",
		"/v1/apps/shop/realtime/endpoints/ep/occupancy":                        "realtime-occupancy",
		"/v1/apps/shop/realtime/endpoints/ep/tickets":                          "realtime-tickets",
		"/v1/apps/shop/realtime/endpoints/ep/push/preferences":                 "realtime-push",
		"/v1/apps/shop/realtime/endpoints/ep/publish-credentials":              "realtime-publishers",
		"/v1/realtime/workers/groups/g1/claims":                                "realtime-workers",
	}
	for path, want := range cases {
		if got := g.owner(path); got != want {
			t.Errorf("owner(%s) = %q, want %q", path, got, want)
		}
	}
	byID := map[string]productcap.Maturity{}
	for _, c := range catalog.Capabilities {
		byID[c.ID] = c.Maturity
	}
	for _, id := range []string{"realtime", "realtime-connections"} {
		if byID[id] != productcap.MaturityBeta {
			t.Errorf("%s maturity = %q, want beta", id, byID[id])
		}
	}
	for _, id := range []string{"realtime-access", "realtime-channels", "realtime-occupancy", "realtime-publishers",
		"realtime-push", "realtime-tickets", "realtime-workers"} {
		if byID[id] != productcap.MaturityPreview {
			t.Errorf("%s maturity = %q, want preview", id, byID[id])
		}
	}
}
