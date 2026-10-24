package consumersignup

import (
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261021143000 — an IPv6 host controls its whole /64, so the public
// sign-up limits count a /64 as one address; IPv4 addresses count alone.
func TestSignupLimiterCountsAnIPv6Slash64AsOneAddress(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	s := &Service{Now: func() time.Time { return now }}
	for i := range api.ConsumerSignupPerIPPerHour {
		if !s.allow(fmt.Sprintf("2001:db8:1:2::%x", i+1), true) {
			t.Fatalf("sign-up %d from the /64 refused before its hourly budget ran out", i+1)
		}
	}
	if s.allow("2001:db8:1:2:ffff:ffff:ffff:fffe", true) {
		t.Fatal("a new interface ID in the same /64 got a fresh sign-up budget")
	}
	if !s.allow("2001:db8:1:3::1", true) {
		t.Fatal("another /64 shares the first one's budget")
	}
	for i := range api.ConsumerSignupPerIPPerHour {
		if !s.allow("198.51.100.7", true) {
			t.Fatalf("IPv4 sign-up %d refused early", i+1)
		}
	}
	if !s.allow("198.51.100.8", true) {
		t.Fatal("a neighbouring IPv4 address shares a budget")
	}
}

// A full limiter map must not forget the limits it holds: resetting it let
// anyone showing limiterCap addresses, one IPv6 /64 before the fix, clear
// every address's budget. Limiters that have refilled hold no state and
// make room; a map still full of active ones refuses new addresses.
func TestSignupLimiterKeepsLimitsWhenFull(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	s := &Service{Now: func() time.Time { return now }}
	const exhausted = "203.0.113.7"
	for range api.ConsumerSignupPerIPPerHour {
		s.allow(exhausted, true)
	}
	if s.allow(exhausted, true) {
		t.Fatal("setup: the address still had budget")
	}
	for i := range limiterCap {
		s.allow(fmt.Sprintf("10.%d.%d.%d", i>>16&0xff, i>>8&0xff, i&0xff), true)
	}
	if s.allow(exhausted, true) {
		t.Fatal("filling the limiter map gave an exhausted address a fresh budget")
	}
	if s.allow("192.0.2.1", true) {
		t.Fatal("a new address was admitted while every tracked one is mid-window")
	}

	// An hour later every bucket has refilled; dropping them loses nothing,
	// so new addresses are admitted again.
	now = now.Add(time.Hour)
	if !s.allow("192.0.2.1", true) {
		t.Fatal("refilled limiters did not make room for a new address")
	}
	if n := len(s.creates.byKey); n > limiterCap {
		t.Fatalf("limiter map holds %d addresses, over the cap of %d", n, limiterCap)
	}
}
