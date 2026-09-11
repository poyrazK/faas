// spec: §5.7
// Same-package test bridge: exposes the unexported centsToStripeMinorUnits
// helper to refund_test.go (which runs in package stripe_test) so the
// outbound cents→Stripe-minor-unit boundary at the Refund call site is
// pinned without standing up the stripe-go SDK.
//
// The bridge is the standard Go test-only-export pattern (named with
// the ForTest suffix to make its scope obvious) — it doesn't widen
// the package's production surface.
package stripe

// CentsToStripeMinorUnitsForTest returns cents unchanged. Mirrors
// centsToStripeMinorUnits at client.go. Lives here so the
// package-stripe_test test in refund_test.go can pin the conversion.
func CentsToStripeMinorUnitsForTest(cents int64) int64 {
	return centsToStripeMinorUnits(cents)
}
