package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// adr: 1315 — a consumer held for an overdue invoice gets a 402 naming the
// due date, and the request is not billed.
func TestEnforceConsumerOverdueHold(t *testing.T) {
	due := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	store := &fakeConsumerPlanStore{policy: ConsumerPlanPolicy{OverdueBlocked: true, OverdueDueAt: due}}
	h := (&Handler{}).WithConsumerPlanStore(store)
	w, r := httptest.NewRecorder(), planRequest("GET /items")
	if h.enforceConsumerPlan(w, r, &statusRecorder{}, App{ID: "app-1", AccountID: "acct-1"}, false) {
		t.Fatal("overdue consumer admitted")
	}
	if w.Code != http.StatusPaymentRequired || !strings.Contains(w.Body.String(), "consumer_invoice_overdue") ||
		!strings.Contains(w.Body.String(), "2026-09-30") || len(store.admitUnits) != 0 ||
		r.Context().Value(suppressFinancialUsageKey{}) != true {
		t.Fatalf("code=%d body=%s admissions=%d", w.Code, w.Body, len(store.admitUnits))
	}
}
