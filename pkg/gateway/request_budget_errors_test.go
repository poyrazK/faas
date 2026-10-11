// spec: §4.1
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	r.Header.Set("x-faas-request-id", "budget-req-1")
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
	if got := rr.Header().Get(api.ErrorCodeHeader); got != api.CodeRequestBudgetExceeded {
		t.Fatalf("error code header = %q, want %q", got, api.CodeRequestBudgetExceeded)
	}
	if got := rr.Header().Get(api.RequestIDHeader); got != "budget-req-1" {
		t.Fatalf("request id header = %q, want budget-req-1", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q, want no-store", got)
	}
}

func TestWriteWakeErrorPreservesBudgetMarkerForCanonicalProblem(t *testing.T) {
	rr := httptest.NewRecorder()
	writeWakeError(rr, api.NewProblem(
		http.StatusGatewayTimeout,
		api.CodeRequestBudgetExceeded,
		"Request budget exceeded",
		"the request exceeded its wall-clock budget while capacity was becoming ready",
	))

	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", rr.Code)
	}
	if got := rr.Header().Get(api.ErrorCodeHeader); got != api.CodeRequestBudgetExceeded {
		t.Fatalf("error code header = %q, want %q", got, api.CodeRequestBudgetExceeded)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("cache-control = %q, want no-store", got)
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

func TestRequestBudgetExpiredUsesBudgetClockWhenContextErrorLags(t *testing.T) {
	ctx := reqbudget.NewContext(context.Background(), reqbudget.Budget{
		Total:   time.Second,
		Started: time.Now().Add(-2 * time.Second),
	})
	if !requestBudgetExpired(ctx) {
		t.Fatal("requestBudgetExpired = false, want true for an elapsed budget")
	}
}

func TestHandleForwardRequestCancellationUsesBudgetClockWhenContextErrorLags(t *testing.T) {
	ctx := reqbudget.NewContext(context.Background(), reqbudget.Budget{
		Total:   time.Second,
		Started: time.Now().Add(-2 * time.Second),
	})
	r := httptest.NewRequest("GET", "http://example.test", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	if !handleForwardRequestCancellation(rr, r, true) {
		t.Fatal("handleForwardRequestCancellation returned false for an elapsed budget")
	}
	if rr.Code != 504 {
		t.Fatalf("status = %d, want 504", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), api.CodeRequestBudgetExceeded) {
		t.Fatalf("body = %q, missing code %q", rr.Body.String(), api.CodeRequestBudgetExceeded)
	}
}

// A budget 504 names the limit, the observed wall clock and the errors page,
// so a customer can tell a 30 s budget from an outage.
func TestRequestBudgetExceededCarriesLimitObservedAndDocs(t *testing.T) {
	ctx := reqbudget.NewContext(context.Background(), reqbudget.Budget{
		Total:   30 * time.Second,
		Started: time.Now().Add(-31 * time.Second),
	})
	r := httptest.NewRequest("GET", "http://example.test", nil).WithContext(ctx)
	rr := httptest.NewRecorder()
	writeRequestBudgetExceededForRequest(rr, r)
	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{`"limit":30000`, `"observed":31`, `"code":"request_budget_exceeded"`, `"docs_url":"` + requestBudgetDocsURL + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("budget problem missing %s: %s", want, body)
		}
	}
}

// The public→internal HTTP/1.1 hop must not time out before the internal
// gateway can answer the budget 504: a fixed 30 s header timeout raced the
// 30 s app budget and turned every slow request into 502 "App connection
// failed" on production-us.
func TestInternalProxyHTTP1HopWaitsForTheCustomerEnvelope(t *testing.T) {
	proxy := NewInternalReverseProxy(&stubDialer{}, &url.URL{Scheme: "http", Host: "internal"}, nil, false)
	transport, ok := proxy.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("HTTP/1.1 hop transport = %T, want *http.Transport", proxy.Transport)
	}
	if transport.ResponseHeaderTimeout < api.CustomerRequestEnvelopeTimeout {
		t.Fatalf("ResponseHeaderTimeout = %s, want >= CustomerRequestEnvelopeTimeout %s", transport.ResponseHeaderTimeout, api.CustomerRequestEnvelopeTimeout)
	}
	if api.CustomerRequestEnvelopeTimeout <= api.RequestBudgetMax {
		t.Fatalf("envelope %s must exceed the request budget %s", api.CustomerRequestEnvelopeTimeout, api.RequestBudgetMax)
	}
}

// TestRequestBudgetDetailNamesThePhase pins production-us hunt #4 (H4-66): a
// warm app that took too long was told the budget ran out "while capacity was
// becoming ready".
func TestRequestBudgetDetailNamesThePhase(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example.test", nil)
	forward := httptest.NewRecorder()
	writeRequestBudgetExceededForRequest(forward, r)
	if !strings.Contains(forward.Body.String(), "while the app was handling it") {
		t.Fatalf("forward detail = %s", forward.Body.String())
	}
	capacity := httptest.NewRecorder()
	writeRequestBudgetExceeded(capacity, r, requestBudgetDetailCapacity)
	if !strings.Contains(capacity.Body.String(), "while capacity was becoming ready") {
		t.Fatalf("capacity detail = %s", capacity.Body.String())
	}
}
