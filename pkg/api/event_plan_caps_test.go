package api

// adr: 20261023215834

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestEventSettledRetentionByPlan(t *testing.T) {
	day := 24 * time.Hour
	for plan, want := range map[Plan]time.Duration{PlanFree: day, PlanHobby: 3 * day, PlanPro: 7 * day, PlanScale: 14 * day} {
		l := MustLimitsFor(plan).EventStorage
		if got := l.SettledRetention(); got != want {
			t.Errorf("%s window = %s, want %s", plan, got, want)
		}
		// Each budget covers the window at a sustained rate within the
		// provisional V12 bound of 10 events/s per account.
		if rate := float64(l.RetainedEvents) / want.Seconds(); rate > 10 {
			t.Errorf("%s sustains %.1f events/s", plan, rate)
		}
	}
	if got := EventSettledRetentionMax(); got != 14*day {
		t.Fatalf("longest window = %s", got)
	}
}

func TestErrEventReplayBackfillRangeNamesTheWindow(t *testing.T) {
	p := ErrEventReplayBackfillRange(259200, 345601)
	if p.Status != http.StatusBadRequest || p.Code != CodeValidation || p.Limit == nil || *p.Limit != 259200 ||
		p.Observed == nil || *p.Observed != 345601 || !strings.HasSuffix(p.DocsURL, "#settled-receipt-retention") ||
		!strings.Contains(p.Detail, "72h0m0s") {
		t.Fatalf("problem = %+v", p)
	}
}
