package objectstorage

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"golang.org/x/time/rate"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/knownhosts"
)

// testHostLookupBudget allows burst new-host reads and refills none.
func testHostLookupBudget(burst int) *HostLookupBudget {
	b := NewHostLookupBudget("test_public_object")
	b.budget = rate.NewLimiter(0, burst)
	return b
}

// After a restart the edge had seen no host name an app, so random names
// that spent the budget shed a real app's first public read. A preloaded app
// host skips the spent budget; random names are still shed.
func TestPublicReadPreloadedHostsSkipASpentBudget(t *testing.T) {
	store := &countingPublicReadStore{Store: seedForwardBucket(t)}
	budget := testHostLookupBudget(3)
	known := knownhosts.New("gregale.dev")
	known.Replace([]string{"assets-app"}, nil)
	budget.Preload(known)
	h, err := NewPublicReadForwarder(PublicReadForwardConfig{
		Store: store, HostLookups: budget, AppsDomain: "gregale.dev", Forward: http.NotFoundHandler(), Next: http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	shed := 0
	for i := range 10 {
		req := httptest.NewRequest(http.MethodGet, "http://x/assets/logo.png", nil)
		req.Host = fmt.Sprintf("random-%d.gregale.dev", i)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusServiceUnavailable {
			shed++
		}
	}
	if shed != 7 {
		t.Fatalf("shed %d of 10 random names, want 7 (budget 3)", shed)
	}
	if got := publicReadRoute(t, h, "assets-app.gregale.dev", "/assets/logo.png"); got != "forward" {
		t.Fatalf("preloaded app's first public read after the flood went to %q, want forward", got)
	}
}

// A flood of random names under the apps wildcard must spend the edge's
// new-host budget, not control-plane reads, and must be answered 503, never
// passed on as a miss; a host already seen naming an app keeps working
// (ADR-20261103163954 §4).
func TestPublicReadHostLookupBudgetShedsUnknownHosts(t *testing.T) {
	store := &countingPublicReadStore{Store: seedForwardBucket(t)}
	budget := testHostLookupBudget(3)
	h, err := NewPublicReadForwarder(PublicReadForwardConfig{
		Store: store, HostLookups: budget, AppsDomain: "gregale.dev", Forward: http.NotFoundHandler(), Next: http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := publicReadRoute(t, h, "assets-app.gregale.dev", "/assets/logo.png"); got != "forward" {
		t.Fatalf("known app's public read went to %q, want forward", got)
	}

	shed := 0
	for i := range 10 {
		req := httptest.NewRequest(http.MethodGet, "http://x/assets/logo.png", nil)
		req.Host = fmt.Sprintf("random-%d.gregale.dev", i)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusServiceUnavailable {
			shed++
			if rec.Header().Get("Retry-After") == "" {
				t.Fatal("shed lookup has no Retry-After")
			}
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.Code != api.CodeTrafficPolicyUnavailable {
				t.Fatalf("shed lookup body = %s, want code %q", rec.Body.String(), api.CodeTrafficPolicyUnavailable)
			}
		}
	}
	// One token went to assets-app's first lookup, two to random names.
	if shed != 8 {
		t.Fatalf("shed %d of 10 random names, want 8", shed)
	}
	if store.appBySlug != 3 {
		t.Fatalf("store read AppBySlug %d times, want 3 (the budget)", store.appBySlug)
	}
	if got := testutil.ToFloat64(budget.shed); got != 8 {
		t.Fatalf("shed counter = %v, want 8", got)
	}

	// The known host skips the spent budget once its cached mounts expire.
	budget.mu.Lock()
	_, known := budget.known["assets-app.gregale.dev"]
	budget.mu.Unlock()
	if !known {
		t.Fatal("a host that named an app was not remembered")
	}
	h.(*publicReadForwarder).match.mounts = newPublicMountCache()
	if got := publicReadRoute(t, h, "assets-app.gregale.dev", "/assets/logo.png"); got != "forward" {
		t.Fatalf("known app's read after the budget was spent went to %q, want forward", got)
	}
}

// Names that cannot be an app (empty or dotted slugs) are refused without a
// store read, so they must not spend the budget either.
func TestPublicReadHostLookupBudgetSkipsNamesThatNeedNoRead(t *testing.T) {
	store := &countingPublicReadStore{Store: seedForwardBucket(t)}
	budget := testHostLookupBudget(1)
	h, err := NewPublicReadForwarder(PublicReadForwardConfig{
		Store: store, HostLookups: budget, AppsDomain: "gregale.dev", Forward: http.NotFoundHandler(), Next: http.NotFoundHandler(),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"a.b.gregale.dev", "c.d.gregale.dev"} {
		if got := publicReadRoute(t, h, host, "/assets/logo.png"); got != "next" {
			t.Fatalf("%s went to %q, want next", host, got)
		}
	}
	if got := publicReadRoute(t, h, "assets-app.gregale.dev", "/assets/logo.png"); got != "forward" {
		t.Fatalf("first real lookup went to %q, want forward: dotted names spent the budget", got)
	}
}

// A full known set is dropped whole, so the set stays bounded.
func TestHostLookupBudgetKnownSetIsBounded(t *testing.T) {
	b := testHostLookupBudget(0)
	b.maxKnown = 2
	for _, host := range []string{"a.gregale.dev", "b.gregale.dev", "c.gregale.dev"} {
		b.found(host)
	}
	if len(b.known) != 1 {
		t.Fatalf("known set holds %d hosts, want 1 after the drop", len(b.known))
	}
	if !b.admit("c.gregale.dev") || b.admit("a.gregale.dev") {
		t.Fatal("after the drop only the newest host should skip an empty budget")
	}
}

// A nil budget admits every lookup, as before the budget existed.
func TestHostLookupBudgetNilAdmits(t *testing.T) {
	var b *HostLookupBudget
	b.found("a.gregale.dev")
	if !b.admit("a.gregale.dev") {
		t.Fatal("nil budget refused a lookup")
	}
}
