package api

import (
	"net/http"
	"strings"
	"testing"
)

// hasLowerKey answers exactly as a strings.ToLower lookup, for canonical,
// upper, mixed, non-ASCII and over-long names, without allocating for an
// ordinary header name.
func TestHasLowerKeyMatchesToLower(t *testing.T) {
	names := []string{"", "x", "X-", "Accept", "X-Forwarded-For", strings.Repeat("x-faas-", 12) + "id"}
	for _, set := range []map[string]struct{}{guestIdentityHeaderNames, outboundWebhookHeaders} {
		for name := range set {
			names = append(names, name, strings.ToUpper(name), http.CanonicalHeaderKey(name), name+"x", name[:len(name)-1])
		}
	}
	// U+212A KELVIN SIGN lowers to "k" under strings.ToLower.
	names = append(names, "x-faas-webhooK-signature", "X-Faas-Request-İd")
	for _, set := range []map[string]struct{}{guestIdentityHeaderNames, outboundWebhookHeaders} {
		for _, name := range names {
			_, want := set[strings.ToLower(name)]
			if got := hasLowerKey(set, name); got != want {
				t.Errorf("hasLowerKey(%q) = %t, strings.ToLower lookup says %t", name, got, want)
			}
		}
	}
	if !IsOutboundWebhookHeader("x-faas-webhooK-signature") {
		t.Error("a Kelvin-sign spelling stopped matching as strings.ToLower does")
	}
	for _, name := range []string{RequestIDHeader, "X-Gregale-Operation-Attempt", "X-Faas-Webhook-Signature", "X-Forwarded-For"} {
		if allocs := testing.AllocsPerRun(100, func() { _ = IsGuestIdentityHeader(name); _ = IsOutboundWebhookHeader(name) }); allocs != 0 {
			t.Errorf("classifying %s allocated %.0f times, want 0", name, allocs)
		}
	}
}

// The guest boundary classifies each header of a request: a typical request's
// names, looked up as strings.ToLower did and as hasLowerKey does.
func BenchmarkGuestHeaderClassification(b *testing.B) {
	names := []string{"Accept", "Accept-Encoding", "User-Agent", "X-Forwarded-For", "X-Forwarded-Proto",
		RequestIDHeader, AppIDHeader, DeploymentIDHeader, InstanceIDHeader, NodeIDHeader, "X-Faas-App", "Traceparent"}
	b.Run("tolower", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, name := range names {
				_, _ = guestIdentityHeaderNames[strings.ToLower(name)]
			}
		}
	})
	b.Run("haslowerkey", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, name := range names {
				_ = hasLowerKey(guestIdentityHeaderNames, name)
			}
		}
	})
}
