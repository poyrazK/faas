// Package webhooksig computes Stripe webhook signatures. It imports only
// the standard library, so the CLI's `billing webhook-test stripe` can sign
// a test event without linking stripe-go, whose package init runs
// `uname -a` on every start. pkg/billing/stripe verifies with the same
// function.
package webhooksig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// V1 returns the v1 signature for the header timestamp t: the hex
// HMAC-SHA256 of `<t>.<payload>` keyed by the endpoint's signing secret.
func V1(payload []byte, secret, t string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(t))
	mac.Write([]byte("."))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// Header returns the Stripe-Signature value `t=<unix>,v1=<signature>` that
// a webhook sent at ts carries.
func Header(payload []byte, secret string, ts time.Time) string {
	t := strconv.FormatInt(ts.Unix(), 10)
	return "t=" + t + ",v1=" + V1(payload, secret, t)
}
