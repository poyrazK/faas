package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestFinancialBudgetAdmissionBlocksWarmTraffic(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.setLegacyHot()
	h.WithFinancialBudgetAdmission(func(context.Context, App) error {
		return api.NewProblem(http.StatusPaymentRequired, "financial_budget_held", "Preview budget reached", "Preview is held.")
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil))
	if rec.Code != http.StatusPaymentRequired || !strings.Contains(rec.Body.String(), "financial_budget_held") || atomic.LoadInt32(&backend.admits) != 0 {
		t.Fatalf("warm budget bypass: %d %s", rec.Code, rec.Body)
	}
}

func TestFinancialBudgetAdmissionFailureDoesNotServeWarmCache(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.setLegacyHot()
	h.WithFinancialBudgetAdmission(func(context.Context, App) error { return context.DeadlineExceeded })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("failed budget read served warm traffic: %d", rec.Code)
	}
}
