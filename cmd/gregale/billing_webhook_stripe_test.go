package main_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	main "github.com/onebox-faas/faas/cmd/gregale"
	stripe "github.com/onebox-faas/faas/pkg/billing/stripe"
)

// The CLI signs with pkg/billing/stripe/webhooksig so it does not link
// stripe-go; the server verifies with pkg/billing/stripe. Both must agree.
func TestBillingWebhookTestStripeSignsWhatTheVerifierAccepts(t *testing.T) {
	body := []byte(`{"id":"evt_cli","type":"invoice.paid"}`)
	payload := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(payload, body, 0o600); err != nil {
		t.Fatal(err)
	}
	verdict := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		switch {
		case err != nil:
			verdict <- err
		case !bytes.Equal(got, body):
			verdict <- errors.New("posted body differs from the payload file")
		default:
			verdict <- stripe.VerifySignature(got, r.Header.Get("Stripe-Signature"), "whsec_cli", time.Minute)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	code := main.CmdBilling([]string{"webhook-test", "stripe", "--url", server.URL, "--secret", "whsec_cli", "--payload", payload})
	if code != 0 {
		t.Fatalf("webhook-test stripe exit = %d, want 0", code)
	}
	select {
	case err := <-verdict:
		if err != nil {
			t.Fatalf("server-side verifier rejected the CLI's signature: %v", err)
		}
	default:
		t.Fatal("webhook-test stripe sent no request")
	}
}
