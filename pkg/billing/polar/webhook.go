package polar

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
)

// VerifyWebhook verifies Polar's Standard Webhooks headers and normalizes the
// supported subscription/order events. The signature covers the exact body
// bytes, so callers must not re-marshal the payload before invoking it.
func (p *Provider) VerifyWebhook(payload []byte, headers map[string]string, tolerance time.Duration) (billing.Event, error) {
	if p == nil || strings.TrimSpace(p.webhookSecret) == "" {
		return billing.Event{}, fmt.Errorf("polar: %w: webhook secret is not configured", billing.ErrBadSignature)
	}
	id := headerValue(headers, "webhook-id")
	timestamp := headerValue(headers, "webhook-timestamp")
	signature := headerValue(headers, "webhook-signature")
	if id == "" || timestamp == "" || signature == "" {
		return billing.Event{}, fmt.Errorf("polar: %w: missing Standard Webhooks header", billing.ErrBadSignature)
	}
	if err := verifyStandardWebhookSignature(payload, id, timestamp, signature, p.webhookSecret, tolerance); err != nil {
		return billing.Event{}, err
	}
	event, err := parsePolarEvent(payload, id, p)
	if err != nil {
		return billing.Event{}, err
	}
	return event, nil
}

func headerValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func verifyStandardWebhookSignature(payload []byte, id, timestamp, signature, secret string, tolerance time.Duration) error {
	if tolerance <= 0 {
		tolerance = 5 * time.Minute
	}
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("polar: %w: malformed webhook timestamp", billing.ErrBadSignature)
	}
	age := time.Since(time.Unix(unix, 0))
	if age > tolerance || age < -tolerance {
		return fmt.Errorf("polar: %w: webhook timestamp outside tolerance", billing.ErrBadSignature)
	}
	key, err := decodeSecret(secret)
	if err != nil {
		return fmt.Errorf("polar: %w: invalid webhook secret encoding", billing.ErrBadSignature)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(id))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(payload)
	expected := mac.Sum(nil)
	for _, part := range strings.Fields(signature) {
		version, encoded, ok := strings.Cut(part, ",")
		if !ok || version != "v1" {
			continue
		}
		got, decodeErr := decodeBase64(encoded)
		if decodeErr == nil && hmac.Equal(expected, got) {
			return nil
		}
	}
	return fmt.Errorf("polar: %w: signature mismatch", billing.ErrBadSignature)
}

func decodeSecret(secret string) ([]byte, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, errors.New("empty secret")
	}
	// Polar's dashboard exposes the raw `polar_whs_...` secret. Standard
	// Webhooks libraries ask for base64 of the entire raw secret; our verifier
	// works on the decoded HMAC key, so raw Polar input is already the key.
	if strings.HasPrefix(secret, "polar_whs_") {
		return []byte(secret), nil
	}
	secret = strings.TrimPrefix(secret, "whsec_")
	return decodeBase64(secret)
}

func decodeBase64(value string) ([]byte, error) {
	decoders := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, decoder := range decoders {
		if decoded, err := decoder.DecodeString(value); err == nil {
			return decoded, nil
		}
	}
	return nil, errors.New("invalid base64")
}

// SignForTest creates a Standard Webhooks v1 signature. rawSecret is the
// decoded HMAC key; VerifyWebhook expects the base64 encoding of that key.
func SignForTest(payload []byte, rawSecret, id string, when time.Time) string {
	timestamp := strconv.FormatInt(when.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(rawSecret))
	_, _ = mac.Write([]byte(id + "." + timestamp + "."))
	_, _ = mac.Write(payload)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

type polarWebhook struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func parsePolarEvent(payload []byte, eventID string, p *Provider) (billing.Event, error) {
	var raw polarWebhook
	if err := json.Unmarshal(payload, &raw); err != nil {
		return billing.Event{}, fmt.Errorf("polar: parse webhook body: %w", err)
	}
	var data map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw.Data)))
	decoder.UseNumber()
	if err := decoder.Decode(&data); err != nil {
		return billing.Event{}, fmt.Errorf("polar: parse webhook data: %w", err)
	}

	subscriptionID := stringValue(data["subscription_id"])
	if subscriptionID == "" && strings.HasPrefix(raw.Type, "subscription.") {
		subscriptionID = stringValue(data["id"])
	}
	event := billing.Event{
		EventID:        eventID,
		Type:           mapPolarEventType(raw.Type, data),
		CustomerID:     firstString(data, "customer_id", nestedString(data, "customer", "id")),
		SubscriptionID: subscriptionID,
		PlanID:         p.planForProduct(firstString(data, "product_id", nestedString(data, "product", "id"))),
		Raw:            clone(payload),
		Currency:       strings.ToUpper(firstString(data, "currency", "price_currency")),
		Invoice:        polarInvoice(raw.Type, data),
	}
	if event.Type == billing.EventRefundProcessed {
		if strings.HasPrefix(raw.Type, "refund.") {
			event.ProviderRefundID = firstString(data, "id", "refund_id")
		} else {
			event.ProviderRefundID = firstString(data, "refund_id")
		}
		event.ChargeID = firstString(data, "order_id", nestedString(data, "order", "id"))
		event.AmountCents = firstNumber(data, "amount", "refunded_amount", "amount_refunded")
	} else if strings.HasPrefix(raw.Type, "order.") {
		event.ChargeID = stringValue(data["id"])
		event.AmountCents = firstNumber(data, "total_amount", "net_amount", "amount")
	}
	if event.SubscriptionID == event.CustomerID {
		event.SubscriptionID = stringValue(data["subscription_id"])
	}
	return event, nil
}

func mapPolarEventType(eventType string, data map[string]any) billing.EventType {
	switch eventType {
	case "subscription.created":
		// Polar can create the subscription before the first payment has
		// completed. Only an active subscription is allowed to grant the
		// local paid entitlement; an incomplete event is intentionally a
		// no-op until order.paid or subscription.active arrives.
		if stringValue(data["status"]) == "active" {
			return billing.EventPaymentSucceeded
		}
		return billing.EventUnknown
	case "subscription.updated", "subscription.uncanceled":
		// Do not let an incomplete subscription.updated event set a paid
		// plan before the first successful payment either.
		if stringValue(data["status"]) != "active" {
			return billing.EventUnknown
		}
		return billing.EventSubscriptionUpdated
	case "subscription.active":
		return billing.EventPaymentSucceeded
	case "subscription.cycled":
		return billing.EventPaymentSucceeded
	case "subscription.past_due":
		return billing.EventSubscriptionPastDue
	case "subscription.revoked":
		return billing.EventSubscriptionCanceled
	case "subscription.canceled":
		// Polar sends this immediately for a scheduled cancellation while
		// the subscription is still active. Do not suspend the account until
		// the eventual revoked event.
		if stringValue(data["status"]) == "active" && boolValue(data["cancel_at_period_end"]) {
			return billing.EventSubscriptionUpdated
		}
		return billing.EventSubscriptionCanceled
	case "order.paid":
		return billing.EventPaymentSucceeded
	case "order.refunded", "refund.created", "refund.updated":
		return billing.EventRefundProcessed
	default:
		return billing.EventUnknown
	}
}

func (p *Provider) planForProduct(productID string) string {
	if p == nil || productID == "" {
		return ""
	}
	for plan, configured := range p.products {
		if configured == productID {
			return string(plan)
		}
	}
	return ""
}

func firstString(data map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(data[key]); value != "" {
			return value
		}
	}
	return ""
}

func nestedString(data map[string]any, objectKey, valueKey string) string {
	nested, ok := data[objectKey].(map[string]any)
	if !ok {
		return ""
	}
	return stringValue(nested[valueKey])
}

func stringValue(value any) string {
	switch value := value.(type) {
	case string:
		return value
	default:
		return ""
	}
}

func boolValue(value any) bool {
	b, _ := value.(bool)
	return b
}

func numberValue(value any) int64 {
	switch value := value.(type) {
	case json.Number:
		n, _ := value.Int64()
		return n
	case float64:
		return int64(value)
	case int64:
		return value
	case string:
		n, _ := strconv.ParseInt(value, 10, 64)
		return n
	default:
		return 0
	}
}

func firstNumber(data map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if value := numberValue(data[key]); value != 0 {
			return value
		}
	}
	return 0
}

func polarInvoice(eventType string, data map[string]any) *billing.InvoiceData {
	if !strings.HasPrefix(eventType, "order.") {
		return nil
	}
	id := firstString(data, "id", "order_id")
	if id == "" {
		return nil
	}
	status := invoiceStatus(eventType, firstString(data, "status"))
	total := firstNumber(data, "total_amount", "amount", "net_amount")
	periodStart := firstTime(data, "period_start", "billing_period_start", "current_period_start")
	periodEnd := firstTime(data, "period_end", "billing_period_end", "current_period_end")
	if periodStart.IsZero() {
		periodStart = nestedTime(data, "billing_period", "starts_at")
	}
	if periodEnd.IsZero() {
		periodEnd = nestedTime(data, "billing_period", "ends_at")
	}
	if periodStart.IsZero() {
		periodStart = firstTime(data, "created_at")
	}
	if periodStart.IsZero() {
		periodStart = time.Now().UTC()
	}
	if periodEnd.IsZero() {
		periodEnd = periodStart
	}
	paid := eventType == "order.paid" || boolValue(data["paid"]) || status == "paid"
	amountPaid := int64(0)
	if paid {
		amountPaid = firstNumber(data, "amount_paid", "paid_amount", "total_amount")
		if amountPaid == 0 {
			amountPaid = total
		}
	}
	return &billing.InvoiceData{
		ProviderInvoiceID: id,
		Number:            firstString(data, "invoice_number", "number"),
		Status:            status,
		PeriodStart:       periodStart,
		PeriodEnd:         periodEnd,
		SubtotalCents:     firstNumber(data, "subtotal_amount", "subtotal"),
		TaxCents:          firstNumber(data, "tax_amount", "tax"),
		TotalCents:        total,
		AmountPaidCents:   amountPaid,
		Currency:          strings.ToLower(firstString(data, "currency", "price_currency")),
		PDFAvailable:      boolValue(data["is_invoice_generated"]) || firstString(data, "invoice_pdf", "invoice_url", "pdf_url") != "",
	}
}

func invoiceStatus(eventType, providerStatus string) string {
	if eventType == "order.paid" {
		return "paid"
	}
	switch strings.ToLower(providerStatus) {
	case "paid":
		return "paid"
	case "void", "cancelled", "canceled":
		return "void"
	case "refunded":
		return "void"
	case "partially_refunded":
		return "paid"
	case "pending":
		return "open"
	case "uncollectible", "failed":
		return "uncollectible"
	case "draft":
		return "draft"
	default:
		return "open"
	}
}

func firstTime(data map[string]any, keys ...string) time.Time {
	for _, key := range keys {
		if value := timeValue(data[key]); !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func nestedTime(data map[string]any, objectKey, valueKey string) time.Time {
	nested, ok := data[objectKey].(map[string]any)
	if !ok {
		return time.Time{}
	}
	return timeValue(nested[valueKey])
}

func timeValue(value any) time.Time {
	switch value := value.(type) {
	case string:
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return parsed.UTC()
		}
	case json.Number:
		if unix, err := value.Int64(); err == nil {
			return time.Unix(unix, 0).UTC()
		}
	case float64:
		return time.Unix(int64(value), 0).UTC()
	}
	return time.Time{}
}

func clone(payload []byte) []byte {
	return append([]byte(nil), payload...)
}
