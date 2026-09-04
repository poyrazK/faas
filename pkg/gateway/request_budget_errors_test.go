package gateway

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

func TestWriteBurstCapacityErrorMapsBudgetExpiryTo504(t *testing.T) {
	deadlineCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	ctx := reqbudget.NewContext(deadlineCtx, reqbudget.Budget{
		Total:   time.Second,
		Started: time.Now().Add(-2 * time.Second),
	})
	r := httptest.NewRequest("POST", "http://example.test", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	if !writeBurstCapacityError(rr, r, context.DeadlineExceeded) {
		t.Fatal("writeBurstCapacityError returned false for budget expiry")
	}
	if rr.Code != 504 {
		t.Fatalf("status = %d, want 504", rr.Code)
	}
	problem := rr.Body.String()
	if !strings.Contains(problem, api.CodeRequestBudgetExceeded) {
		t.Fatalf("body = %q, missing code %q", problem, api.CodeRequestBudgetExceeded)
	}
}

func TestWriteBurstCapacityErrorLeavesClientDisconnectSilent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "http://example.test", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	if writeBurstCapacityError(rr, r, context.Canceled) {
		t.Fatal("writeBurstCapacityError handled a client disconnect")
	}
	if rr.Code != 200 || rr.Body.Len() != 0 {
		t.Fatalf("client disconnect wrote status/body: status=%d body=%q", rr.Code, rr.Body.String())
	}
}
