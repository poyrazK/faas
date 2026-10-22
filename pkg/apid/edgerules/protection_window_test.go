package edgerules

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// H10-13: per-app edge counters are created on their first increment, which
// increase() never sees. On prod three WAF blocks summed to 0 and a throttle
// 429 to "none"; every summary query must also count series born in the window.
func TestEdgeProtectionQueriesCountSeriesBornInWindow(t *testing.T) {
	var resp api.EdgeProtectionResponse
	queries := edgeProtectionQueries(&resp, "app-1", "1h")
	if len(queries) == 0 {
		t.Fatal("no queries")
	}
	for _, q := range queries {
		if strings.Count(q.query, "increase(") != 1 || !strings.Contains(q.query, " offset 1h) or gateway_") {
			t.Errorf("query does not count new series: %s", q.query)
		}
	}
	got := windowedCount(`m{app="a"}`, "15m")
	want := `((increase(m{app="a"}[15m]) and m{app="a"} offset 15m) or m{app="a"})`
	if got != want {
		t.Fatalf("windowedCount = %s, want %s", got, want)
	}
}
