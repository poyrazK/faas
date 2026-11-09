package polar

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// adr: 20261104212028 — credit reaches the next order as a one-time fixed,
// code-less discount set on the subscription; the order webhook reports the
// discount Polar applied.

type polarCall struct {
	method, path string
	body         map[string]any
}

func creditDiscountServer(t *testing.T, subscription string) (*Provider, *[]polarCall) {
	t.Helper()
	var calls []polarCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := polarCall{method: r.Method, path: r.URL.Path}
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&call.body)
		}
		calls = append(calls, call)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/subscriptions/sub-1":
			_, _ = io.WriteString(w, subscription)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/discounts":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"disc-1"}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/subscriptions/sub-1":
			_, _ = io.WriteString(w, `{"id":"sub-1","current_period_end":"2026-12-01T00:00:00Z"}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/discounts/disc-1":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/discounts/gone":
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected Polar call %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	p, err := NewProvider(testConfig(server.URL), nil)
	if err != nil {
		t.Fatal(err)
	}
	return p, &calls
}

func TestCreditDiscountTarget(t *testing.T) {
	renews := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name          string
		subscription  string
		wantAccepting bool
		wantDiscount  string
	}{
		{"active without a discount", `{"status":"active","currency":"USD","current_period_end":"2026-12-01T00:00:00Z"}`, true, ""},
		{"discount id", `{"status":"active","currency":"usd","discount_id":"disc-9","current_period_end":"2026-12-01T00:00:00Z"}`, true, "disc-9"},
		{"nested discount", `{"status":"active","currency":"usd","discount":{"id":"disc-8"},"current_period_end":"2026-12-01T00:00:00Z"}`, true, "disc-8"},
		{"ending at the period end", `{"status":"active","currency":"usd","cancel_at_period_end":true,"current_period_end":"2026-12-01T00:00:00Z"}`, false, ""},
		{"plan change pending", `{"status":"active","currency":"usd","pending_update":{"product_id":"hobby-product"},"current_period_end":"2026-12-01T00:00:00Z"}`, false, ""},
		{"past due", `{"status":"past_due","currency":"usd","current_period_end":"2026-12-01T00:00:00Z"}`, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := creditDiscountServer(t, tc.subscription)
			got, err := p.CreditDiscountTarget(context.Background(), "sub-1")
			if err != nil {
				t.Fatal(err)
			}
			if got.Accepting != tc.wantAccepting || got.DiscountID != tc.wantDiscount || got.Currency != "usd" || !got.RenewsAt.Equal(renews) {
				t.Fatalf("target = %+v, want accepting %v discount %q usd renewing %s", got, tc.wantAccepting, tc.wantDiscount, renews)
			}
		})
	}
}

func TestCreditDiscountRequests(t *testing.T) {
	p, calls := creditDiscountServer(t, `{"status":"active"}`)
	ctx := context.Background()
	id, err := p.CreateCreditDiscount(ctx, "subject-1", 1250, "USD")
	if err != nil || id != "disc-1" {
		t.Fatalf("create = %q, %v", id, err)
	}
	if err := p.SetSubscriptionDiscount(ctx, "sub-1", id); err != nil {
		t.Fatal(err)
	}
	if err := p.SetSubscriptionDiscount(ctx, "sub-1", ""); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteDiscount(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteDiscount(ctx, "gone"); err != nil {
		t.Fatalf("deleting a discount already gone: %v", err)
	}
	if _, err := p.CreateCreditDiscount(ctx, "subject-1", 0, "usd"); err == nil {
		t.Fatal("a zero discount was created")
	}

	if len(*calls) != 5 {
		t.Fatalf("calls = %+v, want 5", *calls)
	}
	create := (*calls)[0].body
	amounts, _ := create["amounts"].(map[string]any)
	if create["type"] != "fixed" || create["duration"] != "once" || create["max_redemptions"] != float64(1) ||
		amounts["usd"] != float64(1250) || create["code"] != nil || create["name"] != CreditDiscountName {
		t.Fatalf("create body = %v, want a code-less once fixed discount of 1250 usd", create)
	}
	if got := (*calls)[1].body; got["discount_id"] != "disc-1" {
		t.Fatalf("attach body = %v", got)
	}
	if got, ok := (*calls)[2].body["discount_id"]; !ok || got != nil {
		t.Fatalf("remove body = %v, want discount_id null", (*calls)[2].body)
	}
}

func TestOrderWebhookCarriesTheDiscount(t *testing.T) {
	for _, tc := range []struct {
		name         string
		data         map[string]any
		wantID       string
		wantDiscount int64
	}{
		{"discount id", map[string]any{"id": "ord-1", "discount_id": "disc-1", "discount_amount": float64(600)}, "disc-1", 600},
		{"nested discount", map[string]any{"id": "ord-1", "discount": map[string]any{"id": "disc-2"}, "discount_amount": float64(10)}, "disc-2", 10},
		{"no discount", map[string]any{"id": "ord-1"}, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := polarInvoice("order.created", tc.data)
			if inv == nil || inv.ProviderDiscountID != tc.wantID || inv.DiscountCents != tc.wantDiscount {
				t.Fatalf("invoice = %+v, want discount %q %d", inv, tc.wantID, tc.wantDiscount)
			}
		})
	}
}
