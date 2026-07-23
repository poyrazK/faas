package paddle

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
)

// webhookDefaultTolerance is the replay-protection window. Paddle's
// document doesn't pin a number; 5 minutes is Stripe's default and
// matches the operator's existing webhook budget. Lives next to the
// verifier rather than in a pkg/billing constant because there is no
// third provider using it yet — when one lands, hoist.
const webhookDefaultTolerance = 5 * time.Minute

// parsePaddleEvent decodes a Paddle webhook body into the
// normalized billing.Event. Provider-shaped JSON stays here —
// apid sees only the Event envelope.
//
// Shape (verified against paddle-go-sdk/v5@v5.2.0 event_types.go):
//
//	{
//	  "event_id":   "evt_01HV...",
//	  "event_type": "subscription.created",
//	  "data": { ... provider-specific payload ... }
//	}
//
// Customer / subscription / plan ids are best-effort pulled from
// the inner data block; apid resolves customer → account via the
// state store in PR #3.
func parsePaddleEvent(payload []byte) (billing.Event, error) {
	var raw struct {
		EventID   string          `json:"event_id"`
		EventType string          `json:"event_type"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return billing.Event{}, fmt.Errorf("paddle: parse webhook body: %w", err)
	}

	custID, subID, planID := extractIDs(raw.Data)

	return billing.Event{
		Type:           mapPaddleEventType(raw.EventType),
		CustomerID:     custID,
		SubscriptionID: subID,
		PlanID:         planID,
		Raw:            cloneBytes(payload),
	}, nil
}

// extractIDs pulls customer / subscription / plan ids off the
// provider-specific data block. Paddle nests these under
// data.{customer_id,subscription_id,items[0].price.id} — best-
// effort because every event type carries a different shape, and
// the apid PR #3 dispatcher can re-fetch the latest values from
// Paddle if any of these come back empty.
func extractIDs(data json.RawMessage) (customer, subscription, plan string) {
	if len(data) == 0 {
		return
	}
	// Subscription events: data has { customer_id, subscription_id, items: [...] }.
	var sub struct {
		CustomerID     string `json:"customer_id"`
		SubscriptionID string `json:"subscription_id"`
		Items          []struct {
			Price struct {
				ID string `json:"id"`
			} `json:"price"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &sub); err == nil {
		customer = sub.CustomerID
		subscription = sub.SubscriptionID
		if len(sub.Items) > 0 {
			plan = sub.Items[0].Price.ID
		}
		return
	}

	// Transaction events: data has { customer_id, subscription_id? }.
	var txn struct {
		CustomerID     string `json:"customer_id"`
		SubscriptionID string `json:"subscription_id"`
	}
	if err := json.Unmarshal(data, &txn); err == nil {
		customer = txn.CustomerID
		subscription = txn.SubscriptionID
	}
	return
}

// mapPaddleEventType translates Paddle's event_type strings into
// the normalized billing.EventType. Unknown types return
// EventUnknown so apid's switch falls through to a 200 no-op
// (Paddle retries on 5xx; we 200 unknown types so it doesn't retry
// forever — same contract as pkg/billing/stripe's mapStripeEventType).
//
// Note on naming: Paddle's "subscription.canceled" is the past
// tense of cancel — same word apid already normalizes to
// EventSubscriptionCanceled for Stripe's `customer.subscription.deleted`.
func mapPaddleEventType(t string) billing.EventType {
	switch t {
	case "subscription.created":
		return billing.EventSubscriptionCreated
	case "subscription.updated":
		return billing.EventSubscriptionUpdated
	case "subscription.canceled":
		return billing.EventSubscriptionCanceled
	case "subscription.past_due":
		return billing.EventSubscriptionPastDue
	case "transaction.paid":
		return billing.EventPaymentSucceeded
	case "transaction.payment_failed":
		return billing.EventPaymentFailed
	default:
		return billing.EventUnknown
	}
}

// paddleSigRegexp captures `ts=N;h1=H` from Paddle-Signature.
//   - ts: decimal unix-seconds; the same field Stripe calls `t`.
//   - h1: 64 hex chars (sha256 output).
//
// The expression is anchored (^…$) so a malformed header never
// partial-matches. Extra fields, missing fields, or wrong casing
// all reject — coverage in TestVerifyPaddleSignature_RejectsMalformedHeader.
var paddleSigRegexp = regexp.MustCompile(`^ts=(\d+);h1=([a-f0-9]{64})$`)

// verifyPaddleSignature is the standalone HMAC verifier. The
// public-callable path is Provider.VerifyWebhook (provider.go) so
// outside callers don't need to know the canonical-string format.
//
// Canonical-string format: <unix>:<body>. Paddle's separator is
// ":" (Stripe's is "."). Source: paddle-go-sdk/v5@v5.2.0
// webhook_verifier.go.
//
// tolerance ≤ 0 falls back to webhookDefaultTolerance (matches
// Stripe's behaviour — pkg/billing/stripe/webhook.go:55).
//
// Returns billing.ErrBadSignature wrapped with operation context
// for any failure (header missing/malformed, ts out of window,
// h1 mismatch). errors.Is(err, billing.ErrBadSignature) is the
// caller contract.
func verifyPaddleSignature(payload []byte, header, secret string, tolerance time.Duration) error {
	if tolerance <= 0 {
		tolerance = webhookDefaultTolerance
	}
	matches := paddleSigRegexp.FindStringSubmatch(strings.TrimSpace(header))
	if len(matches) != 3 {
		return fmt.Errorf("paddle: %w: malformed Paddle-Signature (want ts=N;h1=H)", billing.ErrBadSignature)
	}
	tsStr := matches[1]
	gotHex := matches[2]

	unix, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return fmt.Errorf("paddle: %w: bad ts value: %q", billing.ErrBadSignature, err.Error())
	}
	if age := time.Since(time.Unix(unix, 0)); age > tolerance || age < -tolerance {
		return fmt.Errorf("paddle: %w: timestamp outside tolerance (age=%s)", billing.ErrBadSignature, age)
	}

	// Recompute HMAC-SHA256 over "ts:body" using the shared secret.
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write([]byte(tsStr)); err != nil {
		return fmt.Errorf("paddle: hmac write: %w", err)
	}
	if _, err := mac.Write([]byte(":")); err != nil {
		return fmt.Errorf("paddle: hmac write: %w", err)
	}
	if _, err := mac.Write(payload); err != nil {
		return fmt.Errorf("paddle: hmac write: %w", err)
	}
	expectedHex := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expectedHex), []byte(gotHex)) {
		return fmt.Errorf("paddle: %w: h1 mismatch", billing.ErrBadSignature)
	}
	return nil
}

// SignForTestForTest computes the signature a Paddle-valid webhook
// would carry for the given body + secret + timestamp. Mirrors
// pkg/billing/stripe/webhook.go::SignForTest so the two providers
// share the same test fixture shape. Tests use it to generate
// fixtures; never call from production code.
//
// The "_ForTest" suffix is the project's test-only-export convention
// to silence the linter rule against `_test.go` files reaching into
// lowercase identifiers; see pkg/billing/stripe/webhook.go:114 for
// the older plain SignForTest name (PR #158 review nit — both names
// appear in the codebase until the next touch of either one).
func SignForTestForTest(payload []byte, secret string, when time.Time) string {
	ts := strconv.FormatInt(when.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(ts))
	_, _ = mac.Write([]byte(":"))
	_, _ = mac.Write(payload)
	return "ts=" + ts + ";h1=" + hex.EncodeToString(mac.Sum(nil))
}
