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

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
)

// webhookDefaultTolerance is the replay-protection window. Paddle's
// document doesn't pin a number; 5 minutes is Stripe's default and
// matches the operator's existing webhook budget. Lives next to the
// verifier rather than in a pkg/billing constant because there is no
// third provider using it yet — when one lands, hoist.
const webhookDefaultTolerance = 5 * time.Minute

// WebhookDefaultToleranceSeconds is the integer form of
// webhookDefaultTolerance, exposed for the loader so the env overlay
// (FAAS_PADDLE_WEBHOOK_TOLERANCE_SECONDS) has a stable default value
// to reference without importing the time package from the loader.
// Kept in sync with webhookDefaultTolerance; a change here MUST
// update the comment above and vice-versa.
const WebhookDefaultToleranceSeconds = 300

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
func parsePaddleEvent(payload []byte, provider *Provider) (billing.Event, error) {
	var raw struct {
		EventID   string          `json:"event_id"`
		EventType string          `json:"event_type"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return billing.Event{}, fmt.Errorf("paddle: parse webhook body: %w", err)
	}

	custID, subID, planID := extractIDs(raw.Data)
	var data map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw.Data)))
	decoder.UseNumber()
	if len(raw.Data) > 0 {
		if err := decoder.Decode(&data); err != nil {
			return billing.Event{}, fmt.Errorf("paddle: parse webhook data: %w", err)
		}
	}
	if subID == "" && strings.HasPrefix(raw.EventType, "subscription.") {
		subID = paddleString(data, "id")
	}
	planID = canonicalPaddlePlan(provider, planID, data)
	eventType := mapPaddleEventType(raw.EventType, data)

	event := billing.Event{
		EventID:        raw.EventID,
		Type:           eventType,
		CustomerID:     custID,
		SubscriptionID: subID,
		PlanID:         planID,
		Raw:            cloneBytes(payload),
		Invoice:        paddleInvoice(raw.EventType, data),
	}
	if eventType == billing.EventRefundProcessed {
		event.ProviderRefundID = paddleString(data, "id", "adjustment_id")
		event.ChargeID = paddleString(data, "transaction_id", "charge_id")
		event.AmountCents = paddleAmountAt(data, []string{"totals", "total"}, []string{"amount"})
		event.Currency = strings.ToUpper(paddleString(data, "currency_code", "currency"))
	}
	return event, nil
}

// canonicalPaddlePlan converts provider price/product handles into the
// platform plan enum before the event reaches the shared state machine. The
// checkout custom_data is the first choice because it remains stable even if
// Paddle catalog handles are rotated; the hydrated catalog handles are the
// fallback for provider-generated renewals.
func canonicalPaddlePlan(provider *Provider, providerPlanID string, data map[string]any) string {
	for _, candidate := range []string{
		paddleString(data, "plan", "target_plan"),
		paddleNestedString(data, "custom_data", "plan", "target_plan"),
	} {
		if plan := api.Plan(strings.ToLower(candidate)); plan.Valid() {
			return string(plan)
		}
	}
	if provider != nil && provider.catalog != nil {
		provider.catalog.mu.RLock()
		defer provider.catalog.mu.RUnlock()
		for plan, handle := range provider.catalog.planMonthly {
			if handle == providerPlanID {
				return string(plan)
			}
		}
		for plan, handle := range provider.catalog.planOverage {
			if handle == providerPlanID {
				return string(plan)
			}
		}
		for plan, handle := range provider.catalog.planCustomers {
			if handle == providerPlanID {
				return string(plan)
			}
		}
	}
	return providerPlanID
}

func paddleNestedString(data map[string]any, objectKey string, keys ...string) string {
	nested, ok := data[objectKey].(map[string]any)
	if !ok {
		return ""
	}
	return paddleString(nested, keys...)
}

// extractIDs pulls customer / subscription / plan ids off the
// provider-specific data block. Paddle uses customer_id and
// subscription_id on transaction-shaped payloads, while subscription
// payloads commonly use the top-level id (handled by parsePaddleEvent)
// plus items[0].price.id. Extraction is best-effort because every
// event type carries a different shape.
func extractIDs(data json.RawMessage) (customer, subscription, plan string) {
	if len(data) == 0 {
		return
	}
	// Subscription events: data has { customer_id, subscription_id?, items: [...] }.
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
func mapPaddleEventType(t string, data map[string]any) billing.EventType {
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
	case "transaction.completed":
		return billing.EventPaymentSucceeded
	case "transaction.payment_failed":
		return billing.EventPaymentFailed
	case "transaction.past_due":
		return billing.EventSubscriptionPastDue
	case "adjustment.created", "adjustment.updated":
		if strings.EqualFold(paddleString(data, "action"), "refund") {
			return billing.EventRefundProcessed
		}
		return billing.EventUnknown
	default:
		return billing.EventUnknown
	}
}

func paddleInvoice(eventType string, data map[string]any) *billing.InvoiceData {
	if !strings.HasPrefix(eventType, "transaction.") || data == nil {
		return nil
	}
	// Paddle's transaction payload carries both a transaction ID (the
	// refundable charge identity) and, when an invoice exists, an invoice ID.
	// Invoice history must use the latter so it can be joined to Paddle's
	// invoice/credit-note surfaces; older/test payloads may only have id.
	id := paddleString(data, "invoice_id", "id", "transaction_id")
	if id == "" {
		return nil
	}
	total := paddleAmountAt(data,
		[]string{"details", "adjusted_totals", "total"},
		[]string{"details", "totals", "total"},
		[]string{"total"})
	status := paddleInvoiceStatus(eventType, paddleString(data, "status"))
	paid := eventType == "transaction.paid" || eventType == "transaction.completed" || status == "paid"
	amountPaid := int64(0)
	if paid {
		amountPaid = total
	}
	periodStart := paddleTimeAt(data, "billing_period", "starts_at")
	periodEnd := paddleTimeAt(data, "billing_period", "ends_at")
	if periodStart.IsZero() {
		periodStart = paddleTimeAt(data, "created_at")
	}
	if periodEnd.IsZero() {
		periodEnd = periodStart
	}
	invoiceNumber := paddleString(data, "invoice_number", "number")
	return &billing.InvoiceData{
		ProviderInvoiceID: id,
		Number:            invoiceNumber,
		Status:            status,
		PeriodStart:       periodStart,
		PeriodEnd:         periodEnd,
		SubtotalCents: paddleAmountAt(data,
			[]string{"details", "adjusted_totals", "subtotal"},
			[]string{"details", "totals", "subtotal"},
			[]string{"subtotal"}),
		TaxCents: paddleAmountAt(data,
			[]string{"details", "adjusted_totals", "tax"},
			[]string{"details", "totals", "tax"},
			[]string{"tax"}),
		TotalCents:      total,
		AmountPaidCents: amountPaid,
		Currency:        strings.ToLower(paddleString(data, "currency_code", "currency")),
		PDFAvailable:    eventType == "transaction.completed" && invoiceNumber != "",
	}
}

func paddleInvoiceStatus(eventType, providerStatus string) string {
	switch eventType {
	case "transaction.paid", "transaction.completed":
		return "paid"
	case "transaction.canceled":
		return "void"
	}
	switch strings.ToLower(providerStatus) {
	case "completed", "paid":
		return "paid"
	case "canceled", "cancelled":
		return "void"
	case "past_due", "failed":
		return "uncollectible"
	case "draft":
		return "draft"
	default:
		return "open"
	}
}

func paddleString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := data[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func paddleValueAt(data map[string]any, path ...string) any {
	var value any = data
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	return value
}

func paddleAmountAt(data map[string]any, paths ...[]string) int64 {
	for _, path := range paths {
		value := paddleValueAt(data, path...)
		switch value := value.(type) {
		case string:
			n, err := strconv.ParseInt(value, 10, 64)
			if err == nil && n >= 0 {
				return n
			}
		case json.Number:
			n, err := value.Int64()
			if err == nil && n >= 0 {
				return n
			}
		case float64:
			if value >= 0 && value <= float64(^uint64(0)>>1) {
				return int64(value)
			}
		}
	}
	return 0
}

func paddleTimeAt(data map[string]any, path ...string) time.Time {
	value := paddleValueAt(data, path...)
	if text, ok := value.(string); ok {
		if parsed, err := time.Parse(time.RFC3339, text); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
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
