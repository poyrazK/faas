// adr: 520 — amendment 4: shared new-host budgets and certificate reloads.
package gateway

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Permission asks include successful storage loads. Frequent reloads and
// renewals must remain allowed; only a new hostname consumes durable budget.
func TestOnDemandTLSPolicyRepeatedLoadsRemainAllowed(t *testing.T) {
	_, p, _ := onDemandTLSFixture(t)
	var now atomic.Int64
	now.Store(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC).UnixNano())
	p.SetNow(func() time.Time { return time.Unix(0, now.Load()).UTC() })
	for i := range 12 {
		for _, host := range []string{"shop.example.com", "a.tenants.example.net"} {
			if ok, decision := p.Decide(t.Context(), host); !ok {
				t.Fatalf("load %d for %s refused: %s", i, host, decision)
			}
		}
		now.Add(int64(time.Minute))
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
