// adr: 520 — amendment 1: shared new-host budgets and the per-host retry backoff.
package gateway

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Caddy asks once to load, obtain or renew a certificate and then holds it,
// so a host allowed again and again within minutes is one whose ACME order
// keeps failing. Each failure costs the shared ACME account an
// authorization, and an admitted host costs no budget, so the policy backs
// it off (bug hunt 32 M7).
func TestOnDemandTLSPolicyBacksOffAHostItKeepsAllowing(t *testing.T) {
	_, p, _ := onDemandTLSFixture(t)
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	now := start
	p.SetNow(func() time.Time { return now })
	ctx := context.Background()
	bucket := time.Duration(api.OnDemandTLSHostRetryBucketSeconds) * time.Second
	backoff := time.Duration(api.OnDemandTLSHostRetryBackoffSeconds) * time.Second

	// Handshakes in one slice are one attempt: Caddy can ask several times
	// while an order is in flight.
	for range 10 {
		if ok, decision := p.Decide(ctx, "shop.example.com"); !ok {
			t.Fatalf("repeated ask within one slice = %q, want allow", decision)
		}
	}
	allowInSlices := func(host string) {
		t.Helper()
		for i := 1; i < api.OnDemandTLSHostRetryAllows; i++ {
			now = now.Add(bucket)
			if ok, decision := p.Decide(ctx, host); !ok {
				t.Fatalf("%s attempt %d = %q, want allow", host, i+1, decision)
			}
		}
	}
	allowInSlices("shop.example.com")
	triggered := now
	now = now.Add(bucket)
	if ok, decision := p.Decide(ctx, "shop.example.com"); ok || decision != OnDemandTLSDenyBackoff {
		t.Fatalf("host after %d failing attempts = %v, %q; want deny %q", api.OnDemandTLSHostRetryAllows, ok, decision, OnDemandTLSDenyBackoff)
	}
	if ok, decision := p.Decide(ctx, "a.tenants.example.net"); !ok {
		t.Fatalf("another host during the backoff = %q, want allow", decision)
	}

	// The backoff ends, and doubles when the host fails again.
	now = triggered.Add(backoff + time.Second)
	if ok, decision := p.Decide(ctx, "shop.example.com"); !ok {
		t.Fatalf("host after its backoff = %q, want allow", decision)
	}
	allowInSlices("shop.example.com")
	triggered = now
	now = triggered.Add(backoff + time.Second)
	if _, decision := p.Decide(ctx, "shop.example.com"); decision != OnDemandTLSDenyBackoff {
		t.Fatalf("second backoff after one base period = %q, want %q", decision, OnDemandTLSDenyBackoff)
	}
	now = triggered.Add(2*backoff + time.Second)
	if ok, decision := p.Decide(ctx, "shop.example.com"); !ok {
		t.Fatalf("host after its doubled backoff = %q, want allow", decision)
	}

	// A host that stayed quiet for the reset period starts from the base.
	now = now.Add(time.Duration(api.OnDemandTLSHostRetryResetSeconds) * time.Second)
	if ok, decision := p.Decide(ctx, "shop.example.com"); !ok {
		t.Fatalf("host after the reset period = %q, want allow", decision)
	}
	allowInSlices("shop.example.com")
	triggered = now
	now = triggered.Add(backoff + time.Second)
	if ok, decision := p.Decide(ctx, "shop.example.com"); !ok {
		t.Fatalf("backoff after the reset period = %q, want the base period", decision)
	}
}

// Renewal retries and reloads ask minutes apart; they never back off.
func TestOnDemandTLSPolicySpacedAsksDoNotBackOff(t *testing.T) {
	_, p, _ := onDemandTLSFixture(t)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	p.SetNow(func() time.Time { return now })
	ctx := context.Background()
	spacing := time.Duration(api.OnDemandTLSHostRetryWindowSeconds)*time.Second/time.Duration(api.OnDemandTLSHostRetryAllows-1) + time.Minute
	for i := range 2 * api.OnDemandTLSHostRetryAllows {
		if ok, decision := p.Decide(ctx, "a.tenants.example.net"); !ok {
			t.Fatalf("ask %d, %v apart = %q, want allow", i, spacing, decision)
		}
		now = now.Add(spacing)
	}
}

// The retry tracker holds allowed hosts only and is bounded.
func TestOnDemandTLSPolicyRetryTrackerIsBounded(t *testing.T) {
	_, p, _ := onDemandTLSFixture(t)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	p.mu.Lock()
	for i := range api.OnDemandTLSHostRetryEntries + 5 {
		p.noteAllowLocked(fmt.Sprintf("h%d.tenants.example.net", i), now)
	}
	n := len(p.retries)
	p.mu.Unlock()
	if n > api.OnDemandTLSHostRetryEntries {
		t.Fatalf("retry tracker holds %d hosts, cap %d", n, api.OnDemandTLSHostRetryEntries)
	}
	_, p, _ = onDemandTLSFixture(t)
	for i := range 50 {
		p.Decide(context.Background(), fmt.Sprintf("scan-%d.example.org", i))
	}
	if len(p.retries) != 0 {
		t.Fatalf("denied hosts entered the retry tracker: %d", len(p.retries))
	}
}

func TestOnDemandTLSVerdictDecisions(t *testing.T) {
	for v, want := range map[state.CustomDomainTLSVerdict]string{
		state.TLSHostWildcardUnverified:   OnDemandTLSDenyUnverified,
		state.TLSHostRegisteredDomainFull: OnDemandTLSDenyBudget,
		state.TLSHostOwnerFull:            OnDemandTLSDenyOwnerBudget,
		state.TLSHostFleetFull:            OnDemandTLSDenyFleetBudget,
		state.CustomDomainTLSVerdict(99):  OnDemandTLSDenyError,
	} {
		if got := onDemandTLSVerdictDecision(v); got != want {
			t.Errorf("verdict %d = %q, want %q", v, got, want)
		}
	}
}

// Sibling wildcards share their registered domain's budget at the edge too,
// so a second wildcard under the same domain cannot double the certificates
// Let's Encrypt allows that domain per week.
func TestOnDemandTLSPolicySiblingWildcardsShareTheBudget(t *testing.T) {
	s, p, _ := onDemandTLSFixture(t)
	ctx := context.Background()
	shop, err := s.DomainByName(ctx, "shop.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCustomDomain(ctx, "*.more.example.net", shop.AppID, "tok"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.MarkDomainVerifiedIfChallenge(ctx, "*.more.example.net", "tok"); err != nil || !ok {
		t.Fatalf("verify sibling = %v, %v", ok, err)
	}
	limit := api.OnDemandTLSRegisteredDomainNewHostsPerWeek
	for i := range limit {
		wildcard := "tenants"
		if i%2 == 1 {
			wildcard = "more"
		}
		if ok, decision := p.Decide(ctx, fmt.Sprintf("s%d.%s.example.net", i, wildcard)); !ok {
			t.Fatalf("host %d within the shared budget refused: %s", i, decision)
		}
	}
	if ok, decision := p.Decide(ctx, "one-too-many.more.example.net"); ok || decision != OnDemandTLSDenyBudget {
		t.Fatalf("sibling past the shared budget = %v, %q; want deny %q", ok, decision, OnDemandTLSDenyBudget)
	}
}
