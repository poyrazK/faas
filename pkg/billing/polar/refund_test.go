package polar

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/billing"
)

// refundServer is a fake Polar with a scripted POST /v1/refunds outcome
// and a GET /v1/refunds listing that can be switched to "the refund
// exists" mid-test. Counts every call so the tests can pin the
// exactly-one-POST contract.
type refundServer struct {
	mu        sync.Mutex
	postCode  int    // 0 → 200 with a refund body
	postBody  string // response body for a 2xx POST
	hangup    bool   // close the connection on POST without a response
	listItems string // JSON array returned by GET /v1/refunds
	posts     int
	gets      int
	lastPost  map[string]any
	lastQuery string
}

func (f *refundServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path != "/v1/refunds" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			f.gets++
			f.lastQuery = r.URL.RawQuery
			items := f.listItems
			if items == "" {
				items = "[]"
			}
			_, _ = io.WriteString(w, `{"items":`+items+`,"pagination":{"total_count":1,"max_page":1}}`)
		case http.MethodPost:
			f.posts++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode refund body: %v", err)
			}
			f.lastPost = body
			if f.hangup {
				hj, ok := w.(http.Hijacker)
				if !ok {
					t.Fatal("recorder does not support hijack")
				}
				conn, _, err := hj.Hijack()
				if err != nil {
					t.Fatalf("hijack: %v", err)
				}
				_ = conn.Close()
				return
			}
			if f.postCode != 0 {
				w.WriteHeader(f.postCode)
				_, _ = io.WriteString(w, `{"error":"upstream","detail":"simulated"}`)
				return
			}
			body2 := f.postBody
			if body2 == "" {
				body2 = `{"id":"refund-new","amount":500,"currency":"eur","status":"succeeded"}`
			}
			_, _ = io.WriteString(w, body2)
		default:
			http.Error(w, "method", http.StatusMethodNotAllowed)
		}
	}
}

func (f *refundServer) counts() (posts, gets int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.posts, f.gets
}

const existingRefundItem = `[{"id":"refund-existing","amount":500,"currency":"eur","status":"succeeded","order_id":"order-1","metadata":{"faas_idempotency_key":"operator-refund-42"}}]`

func newRefundProvider(t *testing.T, f *refundServer) (*Provider, func()) {
	t.Helper()
	server := httptest.NewServer(f.handler(t))
	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	return p, server.Close
}

// TestRefund_PreflightReturnsExistingRefund: an operator retry with the
// same key (or a previous attempt whose response was lost) must not
// create a second refund.
func TestRefund_PreflightReturnsExistingRefund(t *testing.T) {
	f := &refundServer{listItems: existingRefundItem}
	p, closeFn := newRefundProvider(t, f)
	defer closeFn()
	ctx := billing.ContextWithIdempotencyKey(context.Background(), "operator-refund-42")
	res, err := p.Refund(ctx, "order-1", 500)
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if res.ProviderRefundID != "refund-existing" || res.AmountCents != 500 || res.ChargeID != "order-1" {
		t.Fatalf("result = %+v, want the existing refund", res)
	}
	if posts, gets := f.counts(); posts != 0 || gets != 1 {
		t.Fatalf("posts/gets = %d/%d, want 0/1", posts, gets)
	}
	if f.lastQuery == "" || !containsAll(f.lastQuery, "order_id=order-1", "limit=100") {
		t.Fatalf("lookup query = %q, want order_id + limit", f.lastQuery)
	}
}

// TestRefund_HappyPathStampsMetadataMarker: the POST carries the key in
// metadata so a later lookup can find it; exactly one POST is sent.
func TestRefund_HappyPathStampsMetadataMarker(t *testing.T) {
	f := &refundServer{}
	p, closeFn := newRefundProvider(t, f)
	defer closeFn()
	ctx := billing.ContextWithIdempotencyKey(context.Background(), "operator-refund-42")
	res, err := p.Refund(ctx, "order-1", 500)
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if res.ProviderRefundID != "refund-new" {
		t.Fatalf("result = %+v", res)
	}
	meta, _ := f.lastPost["metadata"].(map[string]any)
	if meta[refundMetadataKey] != "operator-refund-42" {
		t.Fatalf("metadata = %v, want %s=operator-refund-42", f.lastPost["metadata"], refundMetadataKey)
	}
	if posts, gets := f.counts(); posts != 1 || gets != 1 {
		t.Fatalf("posts/gets = %d/%d, want 1/1", posts, gets)
	}
}

// TestRefund_AmbiguousFailureRecoversByLookup: the POST reached Polar
// but the response was a 502; the refund exists on re-read, so the
// call succeeds without a second POST.
func TestRefund_AmbiguousFailureRecoversByLookup(t *testing.T) {
	f := &refundServer{postCode: http.StatusBadGateway}
	p, closeFn := newRefundProvider(t, f)
	defer closeFn()
	// The listing is empty before the POST and shows the refund after it.
	f.mu.Lock()
	f.listItems = ""
	f.mu.Unlock()
	ctx := billing.ContextWithIdempotencyKey(context.Background(), "operator-refund-42")
	// Flip the listing once the POST has been seen: wrap the handler via
	// a goroutine-safe hook — simplest is to pre-arm after first GET.
	go func() {
		for {
			f.mu.Lock()
			if f.posts >= 1 {
				f.listItems = existingRefundItem
				f.mu.Unlock()
				return
			}
			f.mu.Unlock()
		}
	}()
	res, err := p.Refund(ctx, "order-1", 500)
	if err != nil {
		t.Fatalf("Refund: %v (want recovery via lookup)", err)
	}
	if res.ProviderRefundID != "refund-existing" {
		t.Fatalf("result = %+v, want the refund Polar committed", res)
	}
	if posts, gets := f.counts(); posts != 1 || gets != 2 {
		t.Fatalf("posts/gets = %d/%d, want 1/2 (no blind POST retry)", posts, gets)
	}
}

// TestRefund_AmbiguousFailureWithoutRefundDoesNotRetry pins the fix for
// the double-refund risk: a 5xx with no refund on re-read surfaces the
// error after exactly one POST (pre-fix doJSON retried it three times).
func TestRefund_AmbiguousFailureWithoutRefundDoesNotRetry(t *testing.T) {
	f := &refundServer{postCode: http.StatusBadGateway}
	p, closeFn := newRefundProvider(t, f)
	defer closeFn()
	ctx := billing.ContextWithIdempotencyKey(context.Background(), "operator-refund-42")
	_, err := p.Refund(ctx, "order-1", 500)
	var ae *APIError
	if err == nil || !errors.As(err, &ae) || ae.Status != http.StatusBadGateway {
		t.Fatalf("Refund err = %v, want the 502 APIError", err)
	}
	if posts, gets := f.counts(); posts != 1 || gets != 2 {
		t.Fatalf("posts/gets = %d/%d, want 1/2", posts, gets)
	}
}

// TestRefund_HangupDoesNotRetry: a transport failure after the request
// was sent is ambiguous too — one POST, then a lookup, then the error.
func TestRefund_HangupDoesNotRetry(t *testing.T) {
	f := &refundServer{hangup: true}
	p, closeFn := newRefundProvider(t, f)
	defer closeFn()
	ctx := billing.ContextWithIdempotencyKey(context.Background(), "operator-refund-42")
	if _, err := p.Refund(ctx, "order-1", 500); err == nil {
		t.Fatal("Refund succeeded on a hung-up connection")
	}
	if posts, gets := f.counts(); posts != 1 || gets != 2 {
		t.Fatalf("posts/gets = %d/%d, want 1/2", posts, gets)
	}
}

// TestRefund_ClientRejectionSkipsRecoveryLookup: a definite 4xx (Polar's
// 403 RefundedAlready, 422 validation) is not ambiguous — no second
// listing, the error surfaces as-is.
func TestRefund_ClientRejectionSkipsRecoveryLookup(t *testing.T) {
	f := &refundServer{postCode: http.StatusForbidden}
	p, closeFn := newRefundProvider(t, f)
	defer closeFn()
	ctx := billing.ContextWithIdempotencyKey(context.Background(), "operator-refund-42")
	_, err := p.Refund(ctx, "order-1", 500)
	var ae *APIError
	if err == nil || !errors.As(err, &ae) || ae.Status != http.StatusForbidden {
		t.Fatalf("Refund err = %v, want the 403 APIError", err)
	}
	if posts, gets := f.counts(); posts != 1 || gets != 1 {
		t.Fatalf("posts/gets = %d/%d, want 1/1 (no recovery lookup on a definite rejection)", posts, gets)
	}
}

// TestRefund_DefaultKeyIsOrderAndAmount: without a context key the
// marker is derived from (order, amount) so a bare retry still dedupes.
func TestRefund_DefaultKeyIsOrderAndAmount(t *testing.T) {
	f := &refundServer{}
	p, closeFn := newRefundProvider(t, f)
	defer closeFn()
	if _, err := p.Refund(context.Background(), "order-1", 500); err != nil {
		t.Fatalf("Refund: %v", err)
	}
	meta, _ := f.lastPost["metadata"].(map[string]any)
	if meta[refundMetadataKey] != "faas-refund-order-1-500" {
		t.Fatalf("metadata marker = %v, want faas-refund-order-1-500", meta[refundMetadataKey])
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
