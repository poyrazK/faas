// Package stripex is the thin Stripe wrapper for the one-box FaaS
// platform (spec §4.7, ADR-010). It exposes only the surface M7 needs:
//
//   - EnsurePlanProducts: idempotent product/price creation for the four
//     plans (Free/Hobby/Pro/Scale) + the metered `gb_ram_hour` price.
//   - CreateCustomer: maps a state.Account to a Stripe `cus_…` and writes
//     the customer ID back via state.Store.UpdateAccountStripeCustomerID.
//   - PushUsageRecord: hourly metered usage record with our own
//     (account, hour) dedupe table on top of the Stripe SDK idempotency
//     key.
//   - VerifySignature: HMAC-SHA256 against the Stripe-Signature header,
//     constant-time compare, default 5 min tolerance.
//
// The package keeps the dependency on stripe-go isolated — the rest of
// the codebase talks to the local interface (pkg/meter.StripePusher).
package stripe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
)

// ErrBadSignature is returned by VerifySignature when the header is
// malformed, the timestamp is out of tolerance, or the v1 signature
// doesn't match. Wrapped with operation context by callers. Aliased
// to billing.ErrBadSignature so errors.Is(err, billing.ErrBadSignature)
// works at the apid handler boundary regardless of which Stripe code
// path emitted the wrap.
var ErrBadSignature = billing.ErrBadSignature

// VerifySignature validates a Stripe webhook payload against the
// Stripe-Signature header. The header format is:
//
//	t=<unix>,v1=<hex hmac>
//
// The signed payload is `<unix>.<body>`. The HMAC is computed with
// SHA-256 over the payload using the endpoint signing secret. The
// timestamp tolerance defaults to 5 minutes (Stripe's recommended
// default; override via the tolerance parameter).
//
// Replay protection is timestamp-based — a stale `t=` returns
// ErrBadSignature even if the signature itself is valid. This matches
// Stripe's own recommended implementation.
//
// The compare is constant-time via crypto/hmac.Equal, so timing-attack
// scrapers can't learn anything about the secret.
func VerifySignature(payload []byte, header, secret string, tolerance time.Duration) error {
	if tolerance <= 0 {
		tolerance = 5 * time.Minute
	}
	if secret == "" {
		return fmt.Errorf("%w: empty secret", ErrBadSignature)
	}
	if header == "" {
		return fmt.Errorf("%w: empty header", ErrBadSignature)
	}

	// Parse the header. Stripe accepts multiple comma-separated entries;
	// each entry is `key=value`. Only `t` and `v1` matter for the v1
	// scheme; we ignore v0 (legacy) entries.
	var ts string
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			ts = kv[1]
		case "v1":
			sigs = append(sigs, kv[1])
		}
	}
	if ts == "" || len(sigs) == 0 {
		return fmt.Errorf("%w: missing t= or v1= entries", ErrBadSignature)
	}

	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: bad t= value: %w", ErrBadSignature, err)
	}
	if age := time.Since(time.Unix(unix, 0)); age > tolerance || age < -tolerance {
		return fmt.Errorf("%w: timestamp outside tolerance (age=%s)", ErrBadSignature, age)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(payload)
	expected := mac.Sum(nil)
	expHex := hex.EncodeToString(expected)

	// Constant-time compare against every provided v1 signature (Stripe
	// rotates secrets; the header may carry more than one valid sig).
	for _, sig := range sigs {
		if hmac.Equal([]byte(expHex), []byte(sig)) {
			return nil
		}
	}
	return fmt.Errorf("%w: no v1 matched", ErrBadSignature)
}

// SignForTest computes the signature a Stripe-valid webhook would carry
// for the given payload + secret + timestamp. Tests use it to generate
// fixtures; never call from production code.
func SignForTest(payload []byte, secret string, ts time.Time) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts.Unix(), 10)))
	mac.Write([]byte("."))
	mac.Write(payload)
	return "t=" + strconv.FormatInt(ts.Unix(), 10) + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}
