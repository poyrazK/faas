package runtimefence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

var ErrDelivery = errors.New("external fence receipt delivery unavailable")
var ErrReceiptPending = errors.New("external fence receipt pending")

const ReceiptLookupPath = "/v1/runtime-upgrade/fence-receipts:lookup"
const ReceiptMediaType = "application/vnd.gregale.external-fence.v1+json"
const IntentMediaType = "application/vnd.gregale.external-fence-intent.v1+json"

// ReceiptDelivery contains authenticated bytes, not accepted fencing proof.
// Pass Envelope to RecordRuntimeUpgradeExternalFenceReceipt, which rechecks
// locked intent/key/revocation/heads and database time. Old deliveries remain
// retrievable for exact accepted-byte retries; they cannot gain fresh admission.
type ReceiptDelivery struct{ envelope []byte }

func (d ReceiptDelivery) Envelope() []byte { return slices.Clone(d.envelope) }

// ReceiptClient only looks up existing receipts. Its protocol forbids the
// authority from initiating enforcement, refreshing issuance or signing a new
// claim in response to a lookup. There is no signing or termination method.
type ReceiptClient struct {
	endpoint string
	verifier *Verifier
	client   *http.Client
}

// Lookup sends the exact canonical intent over a fresh authenticated connection.
// There are no redirects, proxies, cookies, compression, polling or retries.
// Caller-driven retries preserve the original intent, challenge and signed bytes.
func (c *ReceiptClient) Lookup(ctx context.Context, intent Intent) (ReceiptDelivery, error) {
	if c == nil || c.client == nil || c.verifier == nil || ctx == nil {
		return ReceiptDelivery{}, deliveryError("client or context")
	}
	if err := ctx.Err(); err != nil {
		return ReceiptDelivery{}, fmt.Errorf("%w: %w", ErrDelivery, err)
	}
	if _, err := IntentDigest(intent); err != nil || intent.AuthorityID != c.verifier.authority {
		return ReceiptDelivery{}, deliveryError("reviewed intent")
	}
	ctx, cancel := context.WithTimeout(ctx, api.RuntimeUpgradeExternalFenceDeliveryTimeout)
	defer cancel()
	raw, err := json.Marshal(intent)
	if err != nil {
		return ReceiptDelivery{}, deliveryError("intent encoding")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return ReceiptDelivery{}, deliveryError("request")
	}
	request.GetBody = nil // No transport replay, even after a failed write.
	request.Header.Set("Content-Type", IntentMediaType)
	request.Header.Set("Accept", ReceiptMediaType)
	request.Header.Set("Cache-Control", "no-store")
	response, err := c.client.Do(request)
	if err != nil {
		// Do not expose endpoint, credentials, certificate subjects or peer text.
		if ctx.Err() != nil {
			return ReceiptDelivery{}, fmt.Errorf("%w: %w", ErrDelivery, ctx.Err())
		}
		return ReceiptDelivery{}, deliveryError("authenticated exchange")
	}
	defer func() { _ = response.Body.Close() }()
	return c.readDelivery(ctx, intent, response)
}

func (c *ReceiptClient) readDelivery(ctx context.Context, intent Intent, response *http.Response) (ReceiptDelivery, error) {
	if err := ctx.Err(); err != nil {
		return ReceiptDelivery{}, fmt.Errorf("%w: %w", ErrDelivery, err)
	}
	if response.StatusCode == http.StatusAccepted || response.StatusCode == http.StatusNotFound {
		return ReceiptDelivery{}, ErrReceiptPending
	}
	if response.StatusCode != http.StatusOK || response.ProtoMajor != 1 || response.Uncompressed || len(response.Header.Values("Content-Type")) != 1 || response.Header.Get("Content-Type") != ReceiptMediaType || len(response.Header.Values("Content-Encoding")) != 0 || response.ContentLength > api.RuntimeUpgradeExternalFenceEnvelopeMaxBytes {
		return ReceiptDelivery{}, deliveryError("response framing")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, api.RuntimeUpgradeExternalFenceEnvelopeMaxBytes+1))
	if ctx.Err() != nil {
		return ReceiptDelivery{}, fmt.Errorf("%w: %w", ErrDelivery, ctx.Err())
	}
	if err != nil || len(raw) == 0 || len(raw) > api.RuntimeUpgradeExternalFenceEnvelopeMaxBytes || len(response.Trailer) != 0 {
		return ReceiptDelivery{}, deliveryError("bounded complete receipt")
	}
	proof, err := c.verifier.verifyAuthenticity(intent, raw)
	if ctx.Err() != nil {
		return ReceiptDelivery{}, fmt.Errorf("%w: %w", ErrDelivery, ctx.Err())
	}
	if err != nil {
		return ReceiptDelivery{}, deliveryError("signed intent binding")
	}
	return ReceiptDelivery{envelope: proof.Envelope()}, nil
}

func deliveryError(operation string) error { return fmt.Errorf("%w: %s", ErrDelivery, operation) }
