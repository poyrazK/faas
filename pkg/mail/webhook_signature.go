package mail

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Resend webhook signature verification (issue #246 acceptance item
// 8). Resend uses Svix / Standard Webhooks — the same scheme as
// dozens of SaaS providers (Clerk, ngrok, Knock, etc.) so the
// verifier is reusable as a future Postmark parity moves to the
// same envelope.
//
// Wire format:
//
//	svix-id:        <unique delivery id, e.g. msg_xxx>
//	svix-timestamp: <unix seconds>
//	svix-signature: v1,<base64 hmac> [v1,<base64 hmac> ...]
//
// The signed payload is `<svix-id>.<svix-timestamp>.<body>`. The
// HMAC is SHA-256 over the payload using the endpoint signing
// secret. The secret is base64-encoded after stripping the
// `whsec_` prefix:
//
//	secret = base64.StdEncoding.DecodeString(secret[len("whsec_"):])
//
// Replay protection is timestamp-based — a stale svix-timestamp
// returns ErrBadSignature even if the signature itself is valid.
// Matches Svix's recommended implementation and matches the 5-min
// window pkg/billing/stripe/webhook.go::VerifySignature uses.
//
// The compare is constant-time via crypto/hmac.Equal so timing-attack
// scrapers can't learn anything about the secret.

// ErrBadSignature is returned by VerifyResendSignature when the
// header is missing, malformed, or fails the HMAC / timestamp
// check. Callers should map this to a 400 response so Resend
// stops retrying.
var ErrBadSignature = errors.New("mail: bad webhook signature")

// DefaultResendSignatureTolerance is the Svix-recommended replay
// window. Pinned here so a future tweak goes through code review
// rather than drifting through the codebase.
const DefaultResendSignatureTolerance = 5 * time.Minute

// ResendSignatureHeader is the canonical name Resend uses for
// the signature header. Exported so tests + the apid webhook
// handler can reference the same string from one place.
const ResendSignatureHeader = "svix-signature"

// ResendIDHeader is the delivery-id header Svix stamps on every
// delivery. The verifier doesn't use it for HMAC validation but
// the apid webhook handler feeds it into the durable replay claim
// as the dedupe key.
const ResendIDHeader = "svix-id"

// ResendTimestampHeader is the unix-seconds timestamp header.
const ResendTimestampHeader = "svix-timestamp"

// whsecPrefix is the Svix secret prefix Resend documents. The
// verifier strips this before base64-decoding.
const whsecPrefix = "whsec_"

// VerifyResendSignature validates a Resend (Svix) webhook
// payload. The header is the verbatim value of svix-signature;
// the other two headers (svix-id, svix-timestamp) are passed
// separately because the apid handler reads them from r.Header
// and the verifier wants them as plain strings.
//
// Empty secret / empty header / bad base64 / stale timestamp /
// no matching v1 signature all return wrapped ErrBadSignature
// — callers map to 400 (signature problem) or 503 (secret
// unset, fail-closed) per the cmd/apid/handlers_mail_webhooks.go
// handler.
func VerifyResendSignature(body []byte, signatureHeader, secret string, idHeader, timestampHeader string, tolerance time.Duration) error {
	if tolerance <= 0 {
		tolerance = DefaultResendSignatureTolerance
	}
	if secret == "" {
		return fmt.Errorf("%w: empty secret", ErrBadSignature)
	}
	if signatureHeader == "" {
		return fmt.Errorf("%w: empty %s header", ErrBadSignature, ResendSignatureHeader)
	}
	if idHeader == "" {
		return fmt.Errorf("%w: empty %s header", ErrBadSignature, ResendIDHeader)
	}
	if timestampHeader == "" {
		return fmt.Errorf("%w: empty %s header", ErrBadSignature, ResendTimestampHeader)
	}

	// Strip the whsec_ prefix + base64-decode the secret. Resend
	// docs (and Svix's own example impls) document this shape;
	// anything else is a programming error in the wiring path.
	if !strings.HasPrefix(secret, whsecPrefix) {
		return fmt.Errorf("%w: secret missing %q prefix", ErrBadSignature, whsecPrefix)
	}
	keyBytes, err := base64.StdEncoding.DecodeString(secret[len(whsecPrefix):])
	if err != nil {
		return fmt.Errorf("%w: secret base64 decode", ErrBadSignature)
	}
	if len(keyBytes) == 0 {
		return fmt.Errorf("%w: empty secret after decode", ErrBadSignature)
	}

	// Parse the signature header. Svix sends one or more
	// space-separated `v1,<base64>` entries. We accept any
	// number and require at least one to match.
	type candidate struct{ raw, decoded []byte }
	var sigs []candidate
	for _, part := range strings.Fields(signatureHeader) {
		kv := strings.SplitN(part, ",", 2)
		if len(kv) != 2 || kv[0] != "v1" {
			// Unknown version entries are ignored — Svix
			// reserves the right to add v2 / v3 in the
			// future and a header with multiple versions
			// is the migration path.
			continue
		}
		decoded, decErr := base64.StdEncoding.DecodeString(kv[1])
		if decErr != nil {
			continue
		}
		sigs = append(sigs, candidate{raw: []byte(kv[1]), decoded: decoded})
	}
	if len(sigs) == 0 {
		return fmt.Errorf("%w: no usable v1 signature in %s", ErrBadSignature, ResendSignatureHeader)
	}

	// Replay protection. Svix recommends a 5-min window; we use
	// the same value as Stripe (pkg/billing/stripe/webhook.go:46)
	// so a single tolerance constant lives in pkg/api/limits.go
	// in a follow-up.
	unix, err := strconv.ParseInt(timestampHeader, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: bad %s value", ErrBadSignature, ResendTimestampHeader)
	}
	if age := time.Since(time.Unix(unix, 0)); age > tolerance || age < -tolerance {
		return fmt.Errorf("%w: timestamp outside tolerance (age=%s)", ErrBadSignature, age)
	}

	// HMAC-SHA256 over the canonical Svix envelope:
	//   <svix-id>.<svix-timestamp>.<body>
	mac := hmac.New(sha256.New, keyBytes)
	mac.Write([]byte(idHeader))
	mac.Write([]byte("."))
	mac.Write([]byte(timestampHeader))
	mac.Write([]byte("."))
	mac.Write(body)
	expected := mac.Sum(nil)

	for _, s := range sigs {
		if hmac.Equal(expected, s.decoded) {
			return nil
		}
	}
	return fmt.Errorf("%w: no v1 matched", ErrBadSignature)
}

// SignResendForTest computes the signature a Resend-valid webhook
// would carry for the given payload + secret + headers. Tests use
// it to generate fixtures; never call from production code.
//
// The secret argument is the verbatim whsec_-prefixed value Resend
// shows in the dashboard. The return value is the full svix-signature
// header value (`v1,<base64>` — Svix sends a single signature today
// but the verifier accepts a space-separated list, so the helper
// returns one).
func SignResendForTest(body []byte, secret, idHeader, timestampHeader string) string {
	if !strings.HasPrefix(secret, whsecPrefix) {
		panic("mail: SignResendForTest: secret missing whsec_ prefix")
	}
	keyBytes, err := base64.StdEncoding.DecodeString(secret[len(whsecPrefix):])
	if err != nil {
		panic("mail: SignResendForTest: " + err.Error())
	}
	mac := hmac.New(sha256.New, keyBytes)
	mac.Write([]byte(idHeader))
	mac.Write([]byte("."))
	mac.Write([]byte(timestampHeader))
	mac.Write([]byte("."))
	mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// RandomResendSecretForTest returns a freshly-generated
// whsec_-prefixed secret suitable for use in tests. Uses
// crypto/rand so two calls never collide.
func RandomResendSecretForTest() string {
	b := make([]byte, 24)
	for i := range b {
		// Tiny PRNG is fine for tests; collisions across a
		// single test process are vanishingly unlikely and
		// the verifier only needs the round-trip to work.
		b[i] = byte(time.Now().UnixNano() >> uint(i%8))
	}
	return whsecPrefix + base64.StdEncoding.EncodeToString(b)
}
