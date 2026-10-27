// adr: 570 — the operator kill switch for the revocation fence.
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
)

func fenceFlags(t *testing.T, def platformflags.Definition) *platformflags.Set {
	t.Helper()
	return platformflags.NewSet(map[string]platformflags.Definition{platformflags.TrafficRevocationFence: def})
}

func TestTrafficFenceOffDecision(t *testing.T) {
	off, other := securityTestAccount, "30000000-0000-0000-0000-000000000003"
	value := false
	accountRule := platformflags.Definition{Enabled: true, Default: true, Rules: []platformflags.Rule{{ID: "fence-off", Accounts: []string{off}, Value: &value}}}
	for _, tc := range []struct {
		name    string
		flags   *platformflags.Set
		account string
		want    bool
	}{
		{"no flag set", nil, off, false},
		{"catalog default", platformflags.NewSet(nil), off, false},
		{"catalog default, no account", platformflags.NewSet(nil), "", false},
		{"platform-wide off", fenceFlags(t, platformflags.Definition{}), off, true},
		{"platform-wide off, no account", fenceFlags(t, platformflags.Definition{}), "", true},
		{"account rule", fenceFlags(t, accountRule), off, true},
		{"account rule, other account", fenceFlags(t, accountRule), other, false},
		// The evaluator answers "not enabled" without an account; that must
		// keep the fence rather than switch it off.
		{"account rule, no account", fenceFlags(t, accountRule), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := trafficFenceOff(tc.flags, tc.account); got != tc.want {
				t.Fatalf("trafficFenceOff = %v, want %v", got, tc.want)
			}
		})
	}
}

// With the switch off for its account, a request is served through a
// revocation store outage, and the answer carries the fence-off marker.
func TestTrafficFenceOffServesThroughRevocationOutage(t *testing.T) {
	value := false
	for _, tc := range []struct {
		name   string
		flags  *platformflags.Set
		served bool
	}{
		{"fence on", platformflags.NewSet(nil), false},
		{"fence off for the account", fenceFlags(t, platformflags.Definition{Enabled: true, Default: true,
			Rules: []platformflags.Rule{{ID: "fence-off", Accounts: []string{securityTestAccount}, Value: &value}}}), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := trafficrevocation.New(&gatewaySecurityStore{err: errors.New("store offline")})
			defer registry.Close()
			h, backend := securityTestHandler(t, registry)
			h.WithTrafficRevocationFenceFlags(tc.flags)
			var stamped http.Header
			h.WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					stamped = http.Header{}
					stampTrafficSecurity(r.Context(), stamped)
					w.WriteHeader(http.StatusOK)
				})
			})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
			if served := rec.Code == http.StatusOK && stamped != nil; served != tc.served {
				t.Fatalf("status=%d served=%v body=%s", rec.Code, served, rec.Body)
			}
			if tc.served && stamped.Get(trafficSecurityHeader) != trafficSecurityFenceOff {
				t.Fatalf("answer stamp = %q, want the fence-off marker", stamped.Get(trafficSecurityHeader))
			}
			if exchanges, scopes := registry.Tracked(); exchanges != 0 || scopes != 0 {
				t.Fatalf("registrations leaked: %d/%d", exchanges, scopes)
			}
			want := 0.0
			if tc.served {
				want = 1
			}
			if got := testutil.ToFloat64(h.metrics.trafficFenceOff.WithLabelValues("public_http")); got != want {
				t.Fatalf("fence-off counter = %v, want %v", got, want)
			}
		})
	}
}

type countingPeers struct{ unverified int }

func (p *countingPeers) Required() bool { return true }
func (p *countingPeers) Unverified()    { p.unverified++ }

// The public edge passes a fence-off answer from its compute gateway and
// counts it as unverified, even while the snapshot is otherwise required.
func TestPublicEdgePassesFenceOffAnswer(t *testing.T) {
	registry := trafficrevocation.New(&gatewaySecurityStore{})
	defer registry.Close()
	peers := &countingPeers{}
	proxy := (&InternalReverseProxy{}).WithTrafficRevocations(registry).WithTrafficSecurityPeers(peers)
	ctx, done := proxy.publicTrafficContext(context.Background())
	defer done()
	r := httptest.NewRequest(http.MethodGet, "http://app.test/", nil).WithContext(ctx)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{trafficSecurityHeader: {trafficSecurityFenceOff}}}
	if err := bindPublicTrafficSecurity(r, resp); err != nil {
		t.Fatalf("fence-off answer refused: %v", err)
	}
	if resp.Header.Get(trafficSecurityHeader) != "" || peers.unverified != 1 {
		t.Fatalf("header=%q unverified=%d", resp.Header.Get(trafficSecurityHeader), peers.unverified)
	}
	if exchanges, scopes := registry.Tracked(); exchanges != 0 || scopes != 0 {
		t.Fatalf("fence-off answer enrolled scopes: %d/%d", exchanges, scopes)
	}
}
