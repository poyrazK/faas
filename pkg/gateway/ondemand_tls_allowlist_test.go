// adr: 520 — amendment 2: a wildcard's opt-in host allowlist.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// With an allowlist, random server names below a wildcard never reach its
// new-host budget, and removing a name stops the certificate of a host the
// edge admitted before (bug hunt 32 M7).
func TestOnDemandTLSPolicyHonoursTheWildcardHostAllowlist(t *testing.T) {
	s, p, _ := onDemandTLSFixture(t)
	ctx := context.Background()
	if ok, decision := p.Decide(ctx, "old.tenants.example.net"); !ok {
		t.Fatalf("host before any allowlist = %q, want allow", decision)
	}
	if err := s.SetCustomDomainTLSAllowedHosts(ctx, "*.tenants.example.net", []string{"app", "eu.shop"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host     string
		allowed  bool
		decision string
	}{
		{"app.tenants.example.net", true, OnDemandTLSAllowWildcard},
		{"eu.shop.tenants.example.net", true, OnDemandTLSAllowWildcard},
		{"random.tenants.example.net", false, OnDemandTLSDenyNotAllowlisted},
		{"old.tenants.example.net", false, OnDemandTLSDenyNotAllowlisted},
		// Exact domains are unaffected.
		{"shop.example.com", true, OnDemandTLSAllowExact},
	} {
		if allowed, decision := p.Decide(ctx, tc.host); allowed != tc.allowed || decision != tc.decision {
			t.Errorf("Decide(%q) = %v, %q; want %v, %q", tc.host, allowed, decision, tc.allowed, tc.decision)
		}
	}
	probe, _ := state.WildcardProbeHost("*.tenants.example.net")
	if ok, decision := p.Decide(ctx, probe); !ok {
		t.Fatalf("the doctor's probe host %s = %q, want allow", probe, decision)
	}

	// Refused names spend no budget.
	for i := range api.OnDemandTLSWildcardNewHostsPerWeek + 5 {
		p.Decide(ctx, fmt.Sprintf("scan-%d.tenants.example.net", i))
	}
	if err := s.SetCustomDomainTLSAllowedHosts(ctx, "*.tenants.example.net", nil); err != nil {
		t.Fatal(err)
	}
	if ok, decision := p.Decide(ctx, "after-the-scan.tenants.example.net"); !ok {
		t.Fatalf("new host after a scan of unlisted names = %q, want allow (the scan spent no budget)", decision)
	}
}

// allowlistFailingTLSStore fails only the allowlist read.
type allowlistFailingTLSStore struct{ mem *state.MemStore }

func (f allowlistFailingTLSStore) DomainByName(ctx context.Context, domain string) (state.CustomDomain, error) {
	return f.mem.DomainByName(ctx, domain)
}

func (f allowlistFailingTLSStore) WildcardDomainForHost(ctx context.Context, host string) (state.CustomDomain, error) {
	return f.mem.WildcardDomainForHost(ctx, host)
}

func (f allowlistFailingTLSStore) AdmitCustomDomainTLSHost(ctx context.Context, wildcardDomain, host string, now time.Time, window time.Duration, limit int) (bool, error) {
	return f.mem.AdmitCustomDomainTLSHost(ctx, wildcardDomain, host, now, window, limit)
}

func (f allowlistFailingTLSStore) AppByID(ctx context.Context, id string) (state.App, error) {
	return f.mem.AppByID(ctx, id)
}

func (f allowlistFailingTLSStore) AccountByID(ctx context.Context, id string) (state.Account, error) {
	return f.mem.AccountByID(ctx, id)
}

func (f allowlistFailingTLSStore) OrgByID(ctx context.Context, id string) (state.Org, error) {
	return f.mem.OrgByID(ctx, id)
}

func (allowlistFailingTLSStore) CustomDomainTLSHostAllowed(context.Context, string, string) (bool, error) {
	return false, errors.New("db down")
}

func TestOnDemandTLSPolicyAllowlistLookupFailsClosed(t *testing.T) {
	s, _, _ := onDemandTLSFixture(t)
	p := NewOnDemandTLSPolicy(allowlistFailingTLSStore{s}, "gregale.dev", nil, nil)
	if ok, decision := p.Decide(context.Background(), "app.tenants.example.net"); ok || decision != OnDemandTLSDenyError {
		t.Fatalf("allowlist lookup error = %v, %q; want deny %q", ok, decision, OnDemandTLSDenyError)
	}
}
