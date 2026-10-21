package operation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261018212918 — a held customer's start is a 402 naming the due date
// and, when it is a plain https URL, the provider's hosted invoice page.
func TestProductInvoiceOverdueProblem(t *testing.T) {
	due := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, url, want string
	}{
		{"hosted page", "https://invoice.stripe.com/i/in_1", "an invoice due 2026-10-15 is unpaid; new work resumes once it is paid: https://invoice.stripe.com/i/in_1"},
		{"script link dropped", "javascript:alert(1)", "an invoice due 2026-10-15 is unpaid; new work resumes once it is paid"},
		{"no link", "", "an invoice due 2026-10-15 is unpaid; new work resumes once it is paid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteOperationError(w, fmt.Errorf("admit: %w", &state.ProductInvoiceOverdueError{DueAt: due, HostedInvoiceURL: tc.url}))
			var problem api.Problem
			if err := json.Unmarshal(w.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusPaymentRequired || problem.Code != api.CodeProductInvoiceOverdue || problem.Detail != tc.want || strings.Contains(w.Body.String(), "javascript") {
				t.Fatalf("problem = %d %+v", w.Code, problem)
			}
		})
	}
}
