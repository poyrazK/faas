// spec: §10 — retries must not create duplicate or misreported refunds.
package polar

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/billing"
)

func TestRefundLookupPaginationFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pages    int
		match    int
		wantPost int
		wantErr  bool
	}{
		{"exhausted budget", refundLookupMaxPages + 1, 0, 0, true},
		{"match at budget", refundLookupMaxPages + 1, refundLookupMaxPages, 0, false},
		{"complete search", refundLookupMaxPages, 0, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gets, posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					posts.Add(1)
					_, _ = io.WriteString(w, `{"id":"new-refund","amount":500}`)
					return
				}
				request := gets.Add(1)
				page, err := strconv.Atoi(r.URL.Query().Get("page"))
				if err != nil || page != int(request) {
					t.Errorf("page=%d, err=%v, want %d", page, err, request)
				}
				items := "[]"
				if page == tc.match {
					items = existingRefundItem
				}
				_, _ = fmt.Fprintf(w, `{"items":%s,"pagination":{"max_page":%d}}`, items, tc.pages)
			}))
			defer server.Close()
			p, err := NewProvider(testConfig(server.URL), nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := billing.ContextWithIdempotencyKey(context.Background(), "operator-refund-42")
			_, err = p.Refund(ctx, "order-1", 500)
			if (err != nil) != tc.wantErr {
				t.Errorf("Refund err=%v, wantErr=%v", err, tc.wantErr)
			}
			if posts.Load() != int32(tc.wantPost) || gets.Load() != refundLookupMaxPages {
				t.Errorf("posts/gets=%d/%d, want %d/%d", posts.Load(), gets.Load(), tc.wantPost, refundLookupMaxPages)
			}
		})
	}
}

func TestRefundRejectsInvalidMatchingRefund(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		for _, tc := range []struct {
			name string
			item string
		}{
			{"missing ID", `[{"amount":500,"metadata":{"faas_idempotency_key":"operator-refund-42"}}]`},
			{"different amount", `[{"id":"existing","amount":700,"metadata":{"faas_idempotency_key":"operator-refund-42"}}]`},
		} {
			t.Run(fmt.Sprintf("%s/recovery=%t", tc.name, recovery), func(t *testing.T) {
				f := &refundServer{listItems: tc.item}
				wantPosts := 0
				if recovery {
					f.listItems = ""
					f.postCode = http.StatusBadGateway
					f.listItemsAfterPost = tc.item
					wantPosts = 1
				}
				p, closeFn := newRefundProvider(t, f)
				defer closeFn()
				ctx := billing.ContextWithIdempotencyKey(context.Background(), "operator-refund-42")
				if result, err := p.Refund(ctx, "order-1", 500); err == nil || result != nil {
					t.Errorf("Refund = (%+v, %v), want invalid response rejected", result, err)
				}
				if posts, _ := f.counts(); posts != wantPosts {
					t.Errorf("posts=%d, want %d", posts, wantPosts)
				}
			})
		}
	}
}

func TestRefundResponseValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    refundResponse
		wantErr bool
	}{
		{"matching amount", refundResponse{ID: "refund", Amount: 500}, false},
		{"omitted amount", refundResponse{ID: "refund"}, false},
		{"negative amount", refundResponse{ID: "refund", Amount: -1}, true},
		{"mismatched amount", refundResponse{ID: "refund", Amount: 700}, true},
		{"whitespace ID", refundResponse{ID: "  ", Amount: 500}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.body.result("order", 500)
			if (err != nil) != tc.wantErr {
				t.Fatalf("result=%+v, err=%v, wantErr=%t", result, err, tc.wantErr)
			}
			if err == nil && (result.AmountCents != 500 || result.ChargeID != "order") {
				t.Fatalf("unexpected result: %+v", result)
			}
		})
	}
}

func TestRefundRecoveryLookupBudgetNeverRetriesWrite(t *testing.T) {
	var gets, posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		gets.Add(1)
		pages := 1
		if posts.Load() > 0 {
			pages = refundLookupMaxPages + 1
		}
		_, _ = fmt.Fprintf(w, `{"items":[],"pagination":{"max_page":%d}}`, pages)
	}))
	defer server.Close()
	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := billing.ContextWithIdempotencyKey(context.Background(), "operator-refund-42")
	for attempt := 0; attempt < 2; attempt++ {
		if result, err := p.Refund(ctx, "order-1", 500); err == nil || result != nil {
			t.Errorf("attempt %d = (%+v, %v), want incomplete lookup to fail", attempt, result, err)
		}
	}
	if posts.Load() != 1 || gets.Load() != 1+2*refundLookupMaxPages {
		t.Fatalf("posts/gets=%d/%d, want one POST, initial lookup, two bounded searches", posts.Load(), gets.Load())
	}
}
