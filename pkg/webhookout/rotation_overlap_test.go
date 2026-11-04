package webhookout_test

// adr: 20261101093517 — a rotated secret keeps signing for its overlap.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/webhookout"
)

// A target with a previous signer carries a second signature, made with the
// old secret over the same timestamp, delivery id and body, in
// X-Faas-Webhook-Signature-Previous on the webhook wire only.
func TestDispatchSignsWithThePreviousSecretDuringRotation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		set      webhookout.HeaderSet
		wantPrev bool
	}{
		{"webhook", webhookout.HeaderSetWebhook, true},
		{"alert", webhookout.HeaderSetAlert, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body []byte
			var header http.Header
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				header = r.Header.Clone()
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			oldSigner := webhookout.NewSigner([]byte("old-secret"))
			target := testTarget(srv.URL)
			target.PreviousSigner = oldSigner
			d := webhookout.NewDispatcher(webhookout.DispatcherOptions{HTTPClient: srv.Client(), HeaderSet: tc.set, MaxAttempts: 1})
			evt := newTestEvent()
			if res := d.Dispatch(context.Background(), target, evt); res.Err != nil {
				t.Fatalf("dispatch: %v", res.Err)
			}
			previous := header.Get(webhookout.HeaderWebhookSignaturePrevious)
			if !tc.wantPrev {
				if previous != "" {
					t.Fatalf("alert wire carried a previous signature %q", previous)
				}
				return
			}
			unix, err := strconv.ParseInt(header.Get("X-Faas-Webhook-Timestamp"), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			id := header.Get("X-Faas-Delivery-Id")
			if want := "sha256=" + oldSigner.Sign(unix, id, body); previous != want {
				t.Fatalf("previous signature = %q, want %q", previous, want)
			}
			if current := header.Get("X-Faas-Webhook-Signature"); current == previous || current == "" {
				t.Fatalf("current signature = %q must differ from the previous one", current)
			}
		})
	}
}
