package faas

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// WebhookSignatureHeader contains the SHA-256 HMAC for a delivery.
	WebhookSignatureHeader = "X-Faas-Webhook-Signature"
	// WebhookPreviousSignatureHeader carries a second HMAC, made with the
	// secret a rotation replaced, for 24 hours after the rotation. A
	// receiver still holding the old secret verifies against it.
	WebhookPreviousSignatureHeader = "X-Faas-Webhook-Signature-Previous"
	// WebhookTimestampHeader contains the Unix timestamp used in the signature.
	WebhookTimestampHeader = "X-Faas-Webhook-Timestamp"
	// WebhookDeliveryIDHeader is stable across automatic retries of a delivery.
	WebhookDeliveryIDHeader = "X-Faas-Delivery-Id"

	// DefaultWebhookTimestampTolerance is the default accepted clock skew.
	DefaultWebhookTimestampTolerance = 5 * time.Minute
)

// VerifiedWebhook contains the authenticated identity and timestamp of one
// delivery. DeliveryID is stable across retries and is the value receivers
// should use as their idempotency key. Persist it with the business change;
// signature verification alone does not prevent a replay within the time
// tolerance.
type VerifiedWebhook struct {
	DeliveryID string
	Timestamp  time.Time
}

// WebhookVerificationOptions configures the freshness check. A zero tolerance
// uses DefaultWebhookTimestampTolerance. Now is primarily useful for tests;
// applications normally leave it nil.
type WebhookVerificationOptions struct {
	TimestampTolerance time.Duration
	Now                func() time.Time
}

// WebhookVerificationErrorCode identifies a safe-to-log verification failure.
type WebhookVerificationErrorCode string

const (
	WebhookVerificationMissingHeader   WebhookVerificationErrorCode = "missing_header"
	WebhookVerificationMalformedHeader WebhookVerificationErrorCode = "malformed_header"
	WebhookVerificationMalformedSig    WebhookVerificationErrorCode = "malformed_signature"
	WebhookVerificationStaleTimestamp  WebhookVerificationErrorCode = "stale_timestamp"
	WebhookVerificationBadSignature    WebhookVerificationErrorCode = "bad_signature"
	WebhookVerificationInvalidSecret   WebhookVerificationErrorCode = "invalid_secret"
	WebhookVerificationInvalidOptions  WebhookVerificationErrorCode = "invalid_options"
)

// WebhookVerificationError is returned for an invalid signature, malformed
// delivery metadata, or a timestamp outside the accepted window. Its message
// never includes the secret, signature, or body.
type WebhookVerificationError struct {
	Code WebhookVerificationErrorCode
}

func (e *WebhookVerificationError) Error() string {
	return fmt.Sprintf("webhook verification: %s", e.Code)
}

// VerifyWebhook authenticates body using Gregale's outbound webhook headers.
// Pass the exact raw request bytes before parsing or re-serializing JSON.
// Verification uses HMAC-SHA256 over
// <unix_timestamp>.<delivery_id>.<raw_body>, and rejects timestamps more than
// five minutes away from the local clock. Persist the returned DeliveryID
// transactionally with the handler's side effects to deduplicate retries.
func VerifyWebhook(secret []byte, headers http.Header, body []byte) (VerifiedWebhook, error) {
	return VerifyWebhookWithOptions(secret, headers, body, WebhookVerificationOptions{})
}

// VerifyWebhookWithOptions is VerifyWebhook with a configurable timestamp
// tolerance and clock. A zero tolerance selects the five-minute default; a
// negative tolerance is rejected.
func VerifyWebhookWithOptions(secret []byte, headers http.Header, body []byte, options WebhookVerificationOptions) (VerifiedWebhook, error) {
	if len(secret) == 0 {
		return VerifiedWebhook{}, webhookVerificationError(WebhookVerificationInvalidSecret)
	}
	if options.TimestampTolerance < 0 {
		return VerifiedWebhook{}, webhookVerificationError(WebhookVerificationInvalidOptions)
	}
	tolerance := options.TimestampTolerance
	if tolerance == 0 {
		tolerance = DefaultWebhookTimestampTolerance
	}
	now := time.Now
	if options.Now != nil {
		now = options.Now
	}

	signatureHeader, err := oneWebhookHeader(headers, WebhookSignatureHeader)
	if err != nil {
		return VerifiedWebhook{}, err
	}
	timestampHeader, err := oneWebhookHeader(headers, WebhookTimestampHeader)
	if err != nil {
		return VerifiedWebhook{}, err
	}
	deliveryID, err := oneWebhookHeader(headers, WebhookDeliveryIDHeader)
	if err != nil {
		return VerifiedWebhook{}, err
	}
	if strings.TrimSpace(deliveryID) != deliveryID || strings.ContainsAny(deliveryID, ",\r\n") || len(deliveryID) > 256 {
		return VerifiedWebhook{}, webhookVerificationError(WebhookVerificationMalformedHeader)
	}

	timestamp, err := strconv.ParseInt(timestampHeader, 10, 64)
	if err != nil || timestamp < 0 || strconv.FormatInt(timestamp, 10) != timestampHeader {
		return VerifiedWebhook{}, webhookVerificationError(WebhookVerificationMalformedHeader)
	}
	if !timestampWithinTolerance(timestamp, now(), tolerance) {
		return VerifiedWebhook{}, webhookVerificationError(WebhookVerificationStaleTimestamp)
	}

	got, ok := parseWebhookSignature(signatureHeader)
	if !ok {
		return VerifiedWebhook{}, webhookVerificationError(WebhookVerificationMalformedSig)
	}

	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "%d.%s.", timestamp, deliveryID)
	_, _ = mac.Write(body)
	want := mac.Sum(nil)
	if !hmac.Equal(got, want) && !previousWebhookSignatureMatches(headers, want) {
		return VerifiedWebhook{}, webhookVerificationError(WebhookVerificationBadSignature)
	}
	return VerifiedWebhook{DeliveryID: deliveryID, Timestamp: time.Unix(timestamp, 0).UTC()}, nil
}

func oneWebhookHeader(headers http.Header, name string) (string, error) {
	var values []string
	for key, entries := range headers {
		if strings.EqualFold(key, name) {
			values = append(values, entries...)
		}
	}
	if len(values) == 0 {
		return "", webhookVerificationError(WebhookVerificationMissingHeader)
	}
	if len(values) != 1 || values[0] == "" {
		return "", webhookVerificationError(WebhookVerificationMalformedHeader)
	}
	return values[0], nil
}

func timestampWithinTolerance(timestamp int64, now time.Time, tolerance time.Duration) bool {
	issuedAt := time.Unix(timestamp, 0)
	return !issuedAt.Before(now.Add(-tolerance)) && !issuedAt.After(now.Add(tolerance))
}

func webhookVerificationError(code WebhookVerificationErrorCode) error {
	return &WebhookVerificationError{Code: code}
}

// parseWebhookSignature decodes a "sha256=<hex>" signature header.
func parseWebhookSignature(header string) ([]byte, bool) {
	if !strings.HasPrefix(header, "sha256=") {
		return nil, false
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil || len(sig) != sha256.Size {
		return nil, false
	}
	return sig, true
}

// previousWebhookSignatureMatches reports whether the delivery's
// previous-secret signature, sent for 24 hours after a secret rotation,
// equals want. A receiver that has not installed the new secret yet keeps
// verifying deliveries during that window.
func previousWebhookSignatureMatches(headers http.Header, want []byte) bool {
	header, err := oneWebhookHeader(headers, WebhookPreviousSignatureHeader)
	if err != nil {
		return false
	}
	got, ok := parseWebhookSignature(header)
	return ok && hmac.Equal(got, want)
}
