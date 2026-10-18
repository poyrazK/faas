package outbound

// adr: 1438 — a tag's daily cost limit alerts at 80% and 100% once per tag
// and day, and overflow rows never alert.

import (
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestTagLimitsAlertOncePerThreshold(t *testing.T) {
	// Each call settles 2,000 millicents: the first reaches 80% of 2,500,
	// the second 100%, and a third is refused without another alert.
	f := newTagFixture(t, map[string]int64{"feature=search": 2_500})
	for index, want := range []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests} {
		if got := f.call("feature=search,tier=free"); got.Code != want {
			t.Fatalf("call %d = %d %s", index, got.Code, got.Body.String())
		}
	}
	alerts := f.backend.AIBudgetAlerts("llm")
	var tagAlerts []AIBudgetAlert
	for _, alert := range alerts {
		if alert.Budget == api.OutboundAIBudgetTagDailyCost {
			tagAlerts = append(tagAlerts, alert)
		}
	}
	if len(tagAlerts) != 2 || tagAlerts[0].Tag != "feature=search" || tagAlerts[0].ThresholdPercent != 80 ||
		tagAlerts[1].ThresholdPercent != 100 || tagAlerts[1].Used != 4_000 || tagAlerts[1].Limit != 2_500 {
		t.Fatalf("tag alerts = %+v", tagAlerts)
	}
	for _, alert := range alerts {
		if alert.Tag == "tier=free" {
			t.Fatal("a tag without a limit alerted")
		}
	}
	day := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	limit := int64(10)
	if crossings := tagBudgetCrossings([]aiTagDay{{tag: "feature=~other", cost: 50, limit: &limit}, {tag: "team=core", cost: 50}}, day, 50); len(crossings) != 0 {
		t.Fatalf("overflow or unlimited rows crossed: %+v", crossings)
	}
}
