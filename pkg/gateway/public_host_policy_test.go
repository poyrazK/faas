// adr: 375
package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type unavailableHostPolicyBackend struct {
	*fakeBackend
	wait bool
}

func (b unavailableHostPolicyBackend) LookupHostPolicy(ctx context.Context, _ string) (App, bool, error) {
	if b.wait {
		<-ctx.Done()
		return b.app, true, nil // Even a loader ignoring expiry cannot publish.
	}
	return App{}, false, errors.New("authoritative host policy unavailable")
}

func TestPublicHostPolicyUnavailableRefusesBeforeGuestOrWake(t *testing.T) {
	for _, wait := range []bool{false, true} {
		t.Run(map[bool]string{false: "outage", true: "expired"}[wait], func(t *testing.T) {
			h, backend, _ := newTestHandler(t)
			h.backend = unavailableHostPolicyBackend{fakeBackend: backend, wait: wait}
			forwarded := false
			h.WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded = true })
			})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
			if rec.Code != http.StatusServiceUnavailable || backend.admits != 0 || forwarded {
				t.Fatalf("host refusal=%d wake=%d forwarded=%v", rec.Code, backend.admits, forwarded)
			}
		})
	}
}

func TestPublicHostPolicyOwnerFailureCannotBecomeSyntheticRoute(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	backend.app.AccountID = "account"
	h.backend = unavailableHostPolicyBackend{fakeBackend: backend}
	entry := &HostEntry{Host: backend.host, Route: []EdgeRuleResolved{{ID: "route", AccountID: "account", TargetAppSlug: "target"}}}
	if err := entry.SealPolicy(); err != nil {
		t.Fatal(err)
	}
	targetLoads := 0
	h.WithEdgeRules(hostPolicyRouteMatcher{snapshotTestMatcher{entry: entry}}, func(context.Context, string) (App, bool) {
		targetLoads++
		return App{ID: "target", AccountID: "account", Plan: api.PlanPro}, true
	}, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
	if rec.Code != http.StatusServiceUnavailable || targetLoads != 0 || backend.admits != 0 {
		t.Fatalf("unverified owner accepted synthetic route: status=%d targets=%d wakes=%d", rec.Code, targetLoads, backend.admits)
	}
}

type hostPolicyRouteMatcher struct{ snapshotTestMatcher }

func (m hostPolicyRouteMatcher) MatchRoute(ctx context.Context, host, _, _ string) *EdgeRuleResolved {
	entry, ok := PinnedHostPolicy(ctx, host)
	if !ok || len(entry.Route) == 0 {
		return nil
	}
	return &entry.Route[0]
}
