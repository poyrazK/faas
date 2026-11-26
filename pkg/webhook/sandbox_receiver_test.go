package webhook

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

func TestSandboxReceiverRejectsUnverifiedOrDifferentDelivery(t *testing.T) {
	const target = api.APISandboxWebhookReceiverPrefix + "abc"
	const expected = `{"id":"delivery","rule_name":"job.finished","payload":{"delivery_id":"delivery","event":"job.finished","attempt":0,"data":{"job_id":"job","result":{"export_id":"expected"}}}}`
	signer := webhookout.NewSigner([]byte("sandbox fixture"))
	receiver := &sandboxReceiver{
		target: target, signer: signer, retryOnce: true,
		delivery: state.AppWebhookDelivery{
			ID: "delivery", Event: state.AppWebhookEventJobFinished,
			Payload: []byte(`{"job_id":"job","result":{"export_id":"expected"}}`),
		},
	}
	for _, tc := range []struct {
		name string
		body string
		sign bool
		want int
	}{
		{"expected signed attempt", expected, true, http.StatusServiceUnavailable},
		{"missing signature", expected, false, http.StatusUnauthorized},
		{"different signed result", strings.Replace(expected, "expected", "different", 1), true, http.StatusUnprocessableEntity},
		{"different signed delivery", strings.Replace(expected, `"delivery_id":"delivery"`, `"delivery_id":"another"`, 1), true, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("X-Faas-Webhook-Timestamp", "1")
			req.Header.Set("X-Faas-Delivery-Id", "delivery")
			req.Header.Set("X-Faas-Webhook-Attempt", "1")
			if tc.sign {
				req.Header.Set("X-Faas-Webhook-Signature", "sha256="+signer.Sign(1, "delivery", []byte(tc.body)))
			}
			resp, err := receiver.RoundTrip(req)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.want {
				t.Fatalf("receiver status = %d, want %d", resp.StatusCode, tc.want)
			}
			_ = resp.Body.Close()
		})
	}
}
