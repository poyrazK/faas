package faas

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"
)

const (
	webhookTestSecret    = "whsec_test_123"
	webhookTestTimestamp = int64(1712345678)
	webhookTestDelivery  = "delivery-123"
	webhookTestBody      = `{"type":"invoice.paid","amount":42}`
	webhookTestSignature = "sha256=9733751b9a5946bb55cb0f75a16ae54fa21f3d6e827284736a9a5cdf4b07e6d8"
)

func TestVerifyWebhookGoldenVector(t *testing.T) {
	headers := webhookTestHeaders()
	verified, err := VerifyWebhookWithOptions([]byte(webhookTestSecret), headers, []byte(webhookTestBody), WebhookVerificationOptions{
		Now: func() time.Time { return time.Unix(webhookTestTimestamp, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if verified.DeliveryID != webhookTestDelivery {
		t.Fatalf("delivery id = %q, want %q", verified.DeliveryID, webhookTestDelivery)
	}
	if !verified.Timestamp.Equal(time.Unix(webhookTestTimestamp, 0).UTC()) {
		t.Fatalf("timestamp = %s, want %s", verified.Timestamp, time.Unix(webhookTestTimestamp, 0).UTC())
	}
}

func TestVerifyWebhookUsesDefaultClockAndTolerance(t *testing.T) {
	timestamp := time.Now().Unix()
	headers := webhookTestHeaders()
	headers.Set(WebhookTimestampHeader, strconv.FormatInt(timestamp, 10))
	headers.Set(WebhookSignatureHeader, webhookTestSignatureFor(timestamp, webhookTestDelivery, []byte(webhookTestBody)))
	verified, err := VerifyWebhook([]byte(webhookTestSecret), headers, []byte(webhookTestBody))
	if err != nil {
		t.Fatal(err)
	}
	if verified.DeliveryID != webhookTestDelivery {
		t.Fatalf("delivery id = %q, want %q", verified.DeliveryID, webhookTestDelivery)
	}
}

func TestVerifyWebhookRejectsChangedBodyAndSecret(t *testing.T) {
	now := func() time.Time { return time.Unix(webhookTestTimestamp, 0) }
	options := WebhookVerificationOptions{Now: now}
	if _, err := VerifyWebhookWithOptions([]byte(webhookTestSecret), webhookTestHeaders(), []byte(webhookTestBody+" "), options); webhookVerificationCode(err) != WebhookVerificationBadSignature {
		t.Fatalf("tampered body error = %v, want bad_signature", err)
	}
	if _, err := VerifyWebhookWithOptions([]byte("wrong-secret"), webhookTestHeaders(), []byte(webhookTestBody), options); webhookVerificationCode(err) != WebhookVerificationBadSignature {
		t.Fatalf("wrong secret error = %v, want bad_signature", err)
	}
}

func TestVerifyWebhookRejectsStaleAndFutureTimestamps(t *testing.T) {
	base := time.Unix(webhookTestTimestamp, 0)
	for _, tc := range []struct {
		name string
		ts   int64
		now  time.Time
	}{
		{name: "stale", ts: webhookTestTimestamp, now: base.Add(DefaultWebhookTimestampTolerance + time.Second)},
		{name: "future", ts: webhookTestTimestamp + int64(DefaultWebhookTimestampTolerance/time.Second) + 1, now: base},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := webhookTestHeaders()
			headers.Set(WebhookTimestampHeader, strconv.FormatInt(tc.ts, 10))
			headers.Set(WebhookSignatureHeader, webhookTestSignatureFor(tc.ts, webhookTestDelivery, []byte(webhookTestBody)))
			_, err := VerifyWebhookWithOptions([]byte(webhookTestSecret), headers, []byte(webhookTestBody), WebhookVerificationOptions{
				Now: func() time.Time { return tc.now },
			})
			if got := webhookVerificationCode(err); got != WebhookVerificationStaleTimestamp {
				t.Fatalf("error = %v, code %q; want stale_timestamp", err, got)
			}
		})
	}
}

func TestVerifyWebhookRejectsAmbiguousAndMalformedHeaders(t *testing.T) {
	t.Run("duplicate signature", func(t *testing.T) {
		headers := webhookTestHeaders()
		headers.Add(WebhookSignatureHeader, webhookTestSignature)
		_, err := VerifyWebhookWithOptions([]byte(webhookTestSecret), headers, []byte(webhookTestBody), webhookTestOptions())
		if got := webhookVerificationCode(err); got != WebhookVerificationMalformedHeader {
			t.Fatalf("error = %v, code %q; want malformed_header", err, got)
		}
	})
	t.Run("bad signature encoding", func(t *testing.T) {
		headers := webhookTestHeaders()
		headers.Set(WebhookSignatureHeader, "sha256=not-hex")
		_, err := VerifyWebhookWithOptions([]byte(webhookTestSecret), headers, []byte(webhookTestBody), webhookTestOptions())
		if got := webhookVerificationCode(err); got != WebhookVerificationMalformedSig {
			t.Fatalf("error = %v, code %q; want malformed_signature", err, got)
		}
	})
	t.Run("duplicate id with alternate casing", func(t *testing.T) {
		headers := webhookTestHeaders()
		headers["x-faas-delivery-id"] = []string{webhookTestDelivery}
		_, err := VerifyWebhookWithOptions([]byte(webhookTestSecret), headers, []byte(webhookTestBody), webhookTestOptions())
		if got := webhookVerificationCode(err); got != WebhookVerificationMalformedHeader {
			t.Fatalf("error = %v, code %q; want malformed_header", err, got)
		}
	})
	t.Run("missing delivery id", func(t *testing.T) {
		headers := webhookTestHeaders()
		headers.Del(WebhookDeliveryIDHeader)
		_, err := VerifyWebhookWithOptions([]byte(webhookTestSecret), headers, []byte(webhookTestBody), webhookTestOptions())
		if got := webhookVerificationCode(err); got != WebhookVerificationMissingHeader {
			t.Fatalf("error = %v, code %q; want missing_header", err, got)
		}
	})
}

func TestVerifyWebhookRejectsInvalidSecretAndOptions(t *testing.T) {
	if _, err := VerifyWebhook(nil, webhookTestHeaders(), []byte(webhookTestBody)); webhookVerificationCode(err) != WebhookVerificationInvalidSecret {
		t.Fatalf("empty secret error = %v, want invalid_secret", err)
	}
	if _, err := VerifyWebhookWithOptions([]byte(webhookTestSecret), webhookTestHeaders(), []byte(webhookTestBody), WebhookVerificationOptions{TimestampTolerance: -time.Second}); webhookVerificationCode(err) != WebhookVerificationInvalidOptions {
		t.Fatalf("negative tolerance error = %v, want invalid_options", err)
	}
}

func webhookTestHeaders() http.Header {
	headers := make(http.Header)
	headers.Set(WebhookSignatureHeader, webhookTestSignature)
	headers.Set(WebhookTimestampHeader, strconv.FormatInt(webhookTestTimestamp, 10))
	headers.Set(WebhookDeliveryIDHeader, webhookTestDelivery)
	return headers
}

func webhookTestOptions() WebhookVerificationOptions {
	return WebhookVerificationOptions{Now: func() time.Time { return time.Unix(webhookTestTimestamp, 0) }}
}

func webhookTestSignatureFor(timestamp int64, deliveryID string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(webhookTestSecret))
	_, _ = fmt.Fprintf(mac, "%d.%s.", timestamp, deliveryID)
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func webhookVerificationCode(err error) WebhookVerificationErrorCode {
	var verificationErr *WebhookVerificationError
	if !errors.As(err, &verificationErr) {
		return ""
	}
	return verificationErr.Code
}
