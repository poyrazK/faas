// adr: 520 — exact and wildcard hosts share durable admission budgets.
package gateway_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOnDemandTLSExactDomainsShareFleetBudget(t *testing.T) {
	s := state.NewMemStore()
	ctx := t.Context()
	acct, err := s.CreateAccount(ctx, "exact-budget@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "exact-budget", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	p := gateway.NewOnDemandTLSPolicy(s, "gregale.dev", nil, nil)
	now := time.Now().UTC()
	p.SetNow(func() time.Time { return now })
	perApp, perAccount, _ := api.CustomDomainLimitsFor(api.PlanFree)
	admitted, denied := 0, 0
	var staged []string
	for i := 0; i < 2*api.OnDemandTLSFleetNewHostsPer3h+1; i++ {
		host := fmt.Sprintf("h%d.customer-zone-%d.com", i, i%7)
		d, err := s.CreateCustomDomainIfUnderQuota(ctx, host, app.ID, "proof", perApp, perAccount)
		if err != nil {
			t.Fatal(err)
		}
		if verified, err := s.MarkDomainVerifiedIfChallenge(ctx, d.Domain, "proof"); err != nil || !verified {
			t.Fatalf("verify: %v %v", verified, err)
		}
		staged = append(staged, host)
		now = now.Add(30 * time.Second)
	}
	for _, host := range staged {
		if ok, decision := p.Decide(ctx, host); ok {
			admitted++
		} else if decision == gateway.OnDemandTLSDenyFleetBudget {
			denied++
		} else {
			t.Fatalf("unexpected refusal for %s: %s", host, decision)
		}
		now = now.Add(30 * time.Millisecond)
	}
	if admitted != api.OnDemandTLSFleetNewHostsPer3h || denied != len(staged)-admitted {
		t.Fatalf("exact-host fleet admissions=%d denials=%d, want %d admissions", admitted, denied, api.OnDemandTLSFleetNewHostsPer3h)
	}
	// A gateway process restart and full ledger cannot deny already admitted
	// certificates or count their reloads as new hostnames.
	p = gateway.NewOnDemandTLSPolicy(s, "gregale.dev", nil, nil)
	p.SetNow(func() time.Time { return now })
	if ok, decision := p.Decide(ctx, staged[0]); !ok {
		t.Fatalf("admitted exact hostname refused after restart with full fleet budget: %s", decision)
	}
}
