package apidserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
)

func TestStripeWebhookSanitizesUnknownCustomerLog(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	var logs bytes.Buffer
	*(*e.S()).Log() = slog.New(slog.NewJSONHandler(&logs, nil))
	*(*e.S()).StripeWebhookSecret() = apidserver.StripeWebhookSecretForTest
	body := []byte(`{"id":"evt_log_safety","type":"invoice.payment_succeeded","data":{"object":{"customer":"cus_unknown\r\ninjected=true"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/stripe", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", stripe.SignForTest(body, apidserver.StripeWebhookSecretForTest, time.Now()))
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unknown customer status=%d, want 503", rec.Code)
	}
	decoder := json.NewDecoder(&logs)
	for {
		var entry struct {
			Message  string `json:"msg"`
			Customer string `json:"customer_id"`
		}
		if err := decoder.Decode(&entry); err == io.EOF {
			t.Fatal("missing unknown customer log")
		} else if err != nil {
			t.Fatal(err)
		}
		if entry.Message != "stripe_webhook.unknown_customer" {
			continue
		}
		if strings.ContainsAny(entry.Customer, "\r\n") || !strings.Contains(entry.Customer, "injected=true") {
			t.Fatalf("customer log field contains controls or lost context: %q", entry.Customer)
		}
		return
	}
}
