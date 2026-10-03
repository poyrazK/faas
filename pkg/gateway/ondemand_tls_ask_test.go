// adr: 520 — on-demand custom-domain TLS permission check.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func onDemandTLSFixture(t *testing.T) (*state.MemStore, *OnDemandTLSPolicy, *prometheus.Registry) {
	t.Helper()
	ctx := context.Background()
	s := state.NewMemStore()
	for domain, verified := range map[string]bool{
		"shop.example.com":          true,
		"pending.example.com":       false,
		"*.tenants.example.net":     true,
		"*.unverified.example.org":  false,
		"exact.tenants.example.net": false,
	} {
		if _, err := s.CreateCustomDomain(ctx, domain, "app-1", "tok"); err != nil {
			t.Fatal(err)
		}
		if verified {
			if ok, err := s.MarkDomainVerifiedIfChallenge(ctx, domain, "tok"); err != nil || !ok {
				t.Fatalf("verify %s = %v, %v", domain, ok, err)
			}
		}
	}
	reg := prometheus.NewRegistry()
	p := NewOnDemandTLSPolicy(s, "gregale.dev", reg, nil)
	p.SetNow(func() time.Time { return time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC) })
	return s, p, reg
}

func TestOnDemandTLSPolicyDecide(t *testing.T) {
	_, p, _ := onDemandTLSFixture(t)
	for _, tc := range []struct {
		host     string
		allowed  bool
		decision string
	}{
		{"shop.example.com", true, OnDemandTLSAllowExact},
		{"SHOP.example.com.", true, OnDemandTLSAllowExact},
		{"pending.example.com", false, OnDemandTLSDenyUnverified},
		{"acme.tenants.example.net", true, OnDemandTLSAllowWildcard},
		{"deep.acme.tenants.example.net", true, OnDemandTLSAllowWildcard},
		// An unverified exact row shadows the verified wildcard above it.
		{"exact.tenants.example.net", false, OnDemandTLSDenyUnverified},
		{"tenants.example.net", false, OnDemandTLSDenyUnknown},
		{"a.unverified.example.org", false, OnDemandTLSDenyUnverified},
		{"unrelated.example.com", false, OnDemandTLSDenyUnknown},
		{"gregale.dev", false, OnDemandTLSDenyPlatform},
		{"a.b.gregale.dev", false, OnDemandTLSDenyPlatform},
		{"*.tenants.example.net", false, OnDemandTLSDenyInvalid},
		{"", false, OnDemandTLSDenyInvalid},
		{"bad_host.example.com", false, OnDemandTLSDenyInvalid},
		{"localhost", false, OnDemandTLSDenyInvalid},
	} {
		t.Run(tc.host, func(t *testing.T) {
			allowed, decision := p.Decide(context.Background(), tc.host)
			if allowed != tc.allowed || decision != tc.decision {
				t.Fatalf("Decide(%q) = %v, %q; want %v, %q", tc.host, allowed, decision, tc.allowed, tc.decision)
			}
		})
	}
}

func TestOnDemandTLSPolicyWildcardBudgetSparesKnownHosts(t *testing.T) {
	_, p, _ := onDemandTLSFixture(t)
	ctx := context.Background()
	limit := api.OnDemandTLSWildcardNewHostsPerWeek
	for i := 0; i < limit; i++ {
		if ok, decision := p.Decide(ctx, fmt.Sprintf("t%d.tenants.example.net", i)); !ok {
			t.Fatalf("host %d within budget refused: %s", i, decision)
		}
	}
	if ok, decision := p.Decide(ctx, "one-too-many.tenants.example.net"); ok || decision != OnDemandTLSDenyBudget {
		t.Fatalf("host past budget = %v, %q; want deny %q", ok, decision, OnDemandTLSDenyBudget)
	}
	// The edge asks again on reload and renewal; those must keep working.
	if ok, decision := p.Decide(ctx, "t0.tenants.example.net"); !ok {
		t.Fatalf("known host refused after budget exhausted: %s", decision)
	}
}

type failingTLSStore struct{ *state.MemStore }

func (failingTLSStore) DomainByName(context.Context, string) (state.CustomDomain, error) {
	return state.CustomDomain{}, errors.New("db down")
}

func TestOnDemandTLSPolicyFailsClosedOnStoreError(t *testing.T) {
	p := NewOnDemandTLSPolicy(failingTLSStore{state.NewMemStore()}, "gregale.dev", nil, nil)
	if ok, decision := p.Decide(context.Background(), "shop.example.com"); ok || decision != OnDemandTLSDenyError {
		t.Fatalf("store error = %v, %q; want deny %q", ok, decision, OnDemandTLSDenyError)
	}
	nilStore := NewOnDemandTLSPolicy(nil, "", nil, nil)
	if ok, decision := nilStore.Decide(context.Background(), "shop.example.com"); ok || decision != OnDemandTLSDenyError {
		t.Fatalf("nil store = %v, %q; want deny %q", ok, decision, OnDemandTLSDenyError)
	}
}

func TestOnDemandTLSAskHandler(t *testing.T) {
	_, p, reg := onDemandTLSFixture(t)
	h := p.Handler()
	for _, tc := range []struct {
		name   string
		method string
		remote string
		domain string
		want   int
	}{
		{"allowed", http.MethodGet, "127.0.0.1:41000", "shop.example.com", http.StatusOK},
		{"allowed ipv6 loopback", http.MethodGet, "[::1]:41000", "shop.example.com", http.StatusOK},
		{"denied", http.MethodGet, "127.0.0.1:41000", "unrelated.example.com", http.StatusForbidden},
		{"non-loopback peer", http.MethodGet, "203.0.113.7:41000", "shop.example.com", http.StatusForbidden},
		{"wrong method", http.MethodPost, "127.0.0.1:41000", "shop.example.com", http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, OnDemandTLSAskPath+"?domain="+url.QueryEscape(tc.domain), nil)
			req.RemoteAddr = tc.remote
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	if got := testutil.ToFloat64(p.decisions.WithLabelValues(OnDemandTLSAllowExact)); got != 2 {
		t.Fatalf("allow_exact counter = %v, want 2", got)
	}
	if n, err := testutil.GatherAndCount(reg, "gateway_tls_on_demand_ask_total"); err != nil || n == 0 {
		t.Fatalf("gateway_tls_on_demand_ask_total not registered: n=%d err=%v", n, err)
	}
}

// Random server names on an internet-facing 443 must not become unbounded
// store lookups: the lookup budget refuses past its burst and refills.
func TestOnDemandTLSPolicyLookupBudget(t *testing.T) {
	_, p, _ := onDemandTLSFixture(t)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	p.SetNow(func() time.Time { return now })
	ctx := context.Background()
	for i := 0; i < api.OnDemandTLSAskLookupBurst; i++ {
		if _, decision := p.Decide(ctx, fmt.Sprintf("scan-%d.example.org", i)); decision != OnDemandTLSDenyUnknown {
			t.Fatalf("lookup %d within burst = %q, want %q", i, decision, OnDemandTLSDenyUnknown)
		}
	}
	if ok, decision := p.Decide(ctx, "shop.example.com"); ok || decision != OnDemandTLSDenyOverload {
		t.Fatalf("lookup past burst = %v, %q; want deny %q", ok, decision, OnDemandTLSDenyOverload)
	}
	now = now.Add(time.Second)
	if ok, decision := p.Decide(ctx, "shop.example.com"); !ok {
		t.Fatalf("lookup after refill = %q, want allow", decision)
	}
}

func TestOnDemandTLSPolicyNegativeCache(t *testing.T) {
	s, p, _ := onDemandTLSFixture(t)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	p.SetNow(func() time.Time { return now })
	ctx := context.Background()
	if _, decision := p.Decide(ctx, "late.example.com"); decision != OnDemandTLSDenyUnknown {
		t.Fatalf("unknown host = %q", decision)
	}
	if _, err := s.CreateCustomDomain(ctx, "late.example.com", "app-1", "tok"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.MarkDomainVerifiedIfChallenge(ctx, "late.example.com", "tok"); err != nil || !ok {
		t.Fatalf("verify = %v, %v", ok, err)
	}
	if ok, _ := p.Decide(ctx, "late.example.com"); ok {
		t.Fatal("negative cache did not hold within its TTL")
	}
	now = now.Add(time.Duration(api.OnDemandTLSAskNegativeCacheSeconds)*time.Second + time.Second)
	if ok, decision := p.Decide(ctx, "late.example.com"); !ok {
		t.Fatalf("host after negative-cache TTL = %q, want allow", decision)
	}
}
