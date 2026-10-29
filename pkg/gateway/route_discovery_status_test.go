package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/usageoutbox"
)

// TestHandlerObserveDiscoversOnlyRoutesTheAppAnswered: a scanner probe the
// app answers 404, or an edge rule refuses, carries a route label but proves
// nothing about the app's API, and it reached the discovered OpenAPI paths
// (Hunt 12 H12-17).
func TestHandlerObserveDiscoversOnlyRoutesTheAppAnswered(t *testing.T) {
	for status, want := range map[int]string{200: "GET /wp-login.php", 404: "", 403: ""} {
		q, err := usageoutbox.Open(t.TempDir(), 4096)
		if err != nil {
			t.Fatal(err)
		}
		h := &Handler{usageOutbox: q, apiDiscoveryEnabled: true}
		acct, app := uuid.New(), uuid.New()
		r := withAppAndAccount(httptest.NewRequest(http.MethodGet, "/wp-login.php", nil), acct, app)
		r = withAuditRoute(r, "GET /wp-login.php")
		h.observe(r, status, app.String(), string(api.PlanPro), false, Target{})
		item, ok, err := q.Next()
		if err != nil {
			t.Fatalf("status %d: %v", status, err)
		}
		// An event with nothing to bill or discover is not enqueued at all.
		got := ""
		if ok {
			got = item.Event.DiscoveredRoute
		}
		if got != want || (want != "" && !ok) {
			t.Errorf("status %d: DiscoveredRoute = %q (enqueued=%t), want %q", status, got, ok, want)
		}
	}
}
