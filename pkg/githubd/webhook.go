// Package githubd is the GitHub App integration daemon (spec §14 M7.5,
// ADR-012). It owns push-webhook reception, Checks-API writes, OAuth
// callback handling, and the per-installation access-token cache.
//
// Slice 1 shipped the gRPC contract; slices 7 + 8 wire the business
// logic. Today this package holds:
//
//   - webhook HMAC verify (VerifyPushSignature; pkg/stripex/webhook.go
//     owns the Stripe equivalent)
//   - the PushEvent struct (slice 7)
//   - a Service skeleton that implements the proto contract via the
//     apid gRPC client + the GitHub HTTP API
//
// Slice 8 fills in OAuth + install-token cache + Checks writer.
package githubd

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ErrBadPushSignature is returned by VerifyPushSignature when the
// header is malformed or the HMAC does not match. Surfaces to the
// gatewayd proxy as 401.
var ErrBadPushSignature = errors.New("githubd: bad X-Hub-Signature-256")

// VerifyPushSignature checks an X-Hub-Signature-256 header against
// the SHA-256 HMAC of body using secret. GitHub sends:
//
//	X-Hub-Signature-256: sha256=<hex>
//
// Header must contain the literal "sha256=" prefix (older v0 sigs
// are rejected — GitHub Apps only emit sha256 today). The HMAC
// comparison uses hmac.Equal (constant time).
//
// Empty secret → ErrBadPushSignature (refuses to verify against no
// key; gatewayd treats this as a misconfig).
func VerifyPushSignature(body []byte, header string, secret []byte) error {
	if len(secret) == 0 {
		return fmt.Errorf("%w: empty secret", ErrBadPushSignature)
	}
	if !strings.HasPrefix(header, "sha256=") {
		return fmt.Errorf("%w: missing sha256= prefix", ErrBadPushSignature)
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil {
		return fmt.Errorf("%w: bad hex: %w", ErrBadPushSignature, err)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	want := mac.Sum(nil)
	if !hmac.Equal(got, want) {
		return fmt.Errorf("%w: hmac mismatch", ErrBadPushSignature)
	}
	return nil
}

// SignForTest computes the X-Hub-Signature-256 header value that
// VerifyPushSignature would accept. Test fixtures use this to
// replay recorded GitHub pushes.
func SignForTest(body []byte, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
