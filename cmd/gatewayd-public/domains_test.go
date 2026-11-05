// adr: 20261026100000
// domains_test.go pins the split layout at the public edge: the platform
// domain keeps the dashboard and API, apps move to their own domain, and the
// old apps domain stays an alias that serves or redirects.

package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// testDomains is the layout with platform and apps domains; an empty
// platform domain shares the apps domain, as production does today.
func testDomains(t *testing.T, platform, apps string, aliases ...string) hostidentity.Domains {
	t.Helper()
	d, err := hostidentity.NewDomains(platform, apps, apps, aliases, "")
	if err != nil {
		t.Fatalf("NewDomains(%q, %q, %v): %v", platform, apps, aliases, err)
	}
	return d
}

// splitDomains is the layout after the move ADR-20261026100000 describes.
func splitDomains(t *testing.T, mode string) hostidentity.Domains {
	t.Helper()
	d, err := hostidentity.NewDomains("gregale.dev", "gregale.app", "gregale.app", []string{"gregale.dev"}, mode)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestIsAppHostInTheSplitLayout(t *testing.T) {
	d := splitDomains(t, "")
	for host, want := range map[string]bool{
		"shop.gregale.app":            true,
		"shop.gregale.dev":            true,
		"deploy-7-shop.gregale.app":   true,
		"gregale.app":                 false,
		"gregale.dev":                 false,
		"api.gregale.dev":             false,
		"operations.gregale.dev":      false,
		"s3.gregale.dev":              false,
		"shop.example.com":            false,
		"evilgregale.app":             false,
		"deploy-7-shop.other.example": false,
	} {
		if got := isAppHost(host, d); got != want {
			t.Errorf("isAppHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestPlatformRoutesFollowThePlatformDomain(t *testing.T) {
	d := splitDomains(t, "")
	app := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	mux := http.NewServeMux()
	installPublicStaticRoutes(mux, d.Platform, func() http.Handler { return app })
	for host, want := range map[string]int{
		"gregale.dev":      http.StatusOK,
		"api.gregale.dev":  http.StatusOK,
		"shop.gregale.app": http.StatusTeapot,
		"shop.gregale.dev": http.StatusTeapot,
		"gregale.app":      http.StatusTeapot,
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+host+"/.well-known/security.txt", nil))
		if rec.Code != want {
			t.Errorf("security.txt on %s = %d, want %d", host, rec.Code, want)
		}
	}
}

func TestClassifyHostInTheSplitLayout(t *testing.T) {
	d := splitDomains(t, "")
	for host, want := range map[string]httpsec.Surface{
		"api.gregale.dev":  httpsec.SurfacePlatform,
		"shop.gregale.app": httpsec.SurfaceAppHost,
		"shop.gregale.dev": httpsec.SurfaceAppHost,
		"shop.example.com": httpsec.SurfaceCustomDomain,
	} {
		if got := httpsec.ClassifyHostIn(host, d); got != want {
			t.Errorf("ClassifyHostIn(%q) = %v, want %v", host, got, want)
		}
	}
}

// In serve mode an alias request continues as its canonical host, so every
// host-keyed rule and policy downstream applies to it; the client's host
// travels in X-Forwarded-Host.
func TestAliasHostsServeAsTheCanonicalHost(t *testing.T) {
	var gotHost, gotForwarded string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotForwarded = r.Host, r.Header.Get("X-Forwarded-Host")
		w.WriteHeader(http.StatusTeapot)
	})
	serve := aliasHosts(next, splitDomains(t, "serve"))
	for _, tc := range []struct{ target, host, forwarded string }{
		{"http://shop.gregale.dev/a", "shop.gregale.app", "shop.gregale.dev"},
		{"http://Shop.Gregale.Dev:8090/a", "shop.gregale.app:8090", "Shop.Gregale.Dev:8090"},
		{"http://pr-3-shop.gregale.dev/", "pr-3-shop.gregale.app", "pr-3-shop.gregale.dev"},
		// Canonical, platform, nested and customer hosts pass untouched,
		// including a client-sent X-Forwarded-Host.
		{"http://shop.gregale.app/", "shop.gregale.app", "client.example"},
		{"http://api.gregale.dev/v1/whoami", "api.gregale.dev", "client.example"},
		{"http://gregale.dev/", "gregale.dev", "client.example"},
		{"http://a.b.gregale.dev/", "a.b.gregale.dev", "client.example"},
		{"http://shop.example.com/", "shop.example.com", "client.example"},
		// Platform-zone hosts on the old apps domain are not app aliases.
		{"http://s3.gregale.dev/bucket/key", "s3.gregale.dev", "client.example"},
		{"http://registry.gregale.dev/v2/", "registry.gregale.dev", "client.example"},
		{"http://www.gregale.dev/", "www.gregale.dev", "client.example"},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.target, nil)
		req.Header.Set("X-Forwarded-Host", "client.example")
		rec := httptest.NewRecorder()
		serve.ServeHTTP(rec, req)
		if rec.Code != http.StatusTeapot || gotHost != tc.host || gotForwarded != tc.forwarded {
			t.Errorf("%s: status %d host %q forwarded %q, want host %q forwarded %q", tc.target, rec.Code, gotHost, gotForwarded, tc.host, tc.forwarded)
		}
	}
}

func TestAliasHostsRedirectMode(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	redirect := aliasHosts(next, splitDomains(t, "redirect"))
	for _, tc := range []struct {
		method, target, location string
		status                   int
	}{
		{http.MethodGet, "http://shop.gregale.dev/a/b?x=1", "https://shop.gregale.app/a/b?x=1", http.StatusPermanentRedirect},
		{http.MethodPost, "http://pr-3-shop.gregale.dev:8090/hook", "https://pr-3-shop.gregale.app/hook", http.StatusPermanentRedirect},
		// The platform keeps its hosts on the old apps domain.
		{http.MethodGet, "http://api.gregale.dev/v1/whoami", "", http.StatusTeapot},
		{http.MethodGet, "http://gregale.dev/", "", http.StatusTeapot},
		{http.MethodGet, "http://shop.gregale.app/", "", http.StatusTeapot},
		{http.MethodGet, "http://shop.example.com/", "", http.StatusTeapot},
		{http.MethodPut, "http://s3.gregale.dev/bucket/key", "", http.StatusTeapot},
	} {
		rec := httptest.NewRecorder()
		redirect.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.target, nil))
		if rec.Code != tc.status || rec.Header().Get("Location") != tc.location {
			t.Errorf("%s %s = %d %q, want %d %q", tc.method, tc.target, rec.Code, rec.Header().Get("Location"), tc.status, tc.location)
		}
	}
}

// The on-demand TLS ask never issues for a host under a domain Gregale
// owns: the wildcard certificates cover them, and a custom-domain row there
// would be a host takeover.
func TestOnDemandTLSAskRefusesEveryOwnedDomain(t *testing.T) {
	t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", "on_demand")
	ctx := context.Background()
	store := state.NewMemStore()
	// Verified rows under owned domains prove the refusal is the ownership
	// check, not a missing row.
	for _, domain := range []string{"shop.example.com", "x.gregale.app", "x.gregale.dev"} {
		if _, err := store.CreateCustomDomain(ctx, domain, "app-1", "tok"); err != nil {
			t.Fatal(err)
		}
		if ok, err := store.MarkDomainVerifiedIfChallenge(ctx, domain, "tok"); err != nil || !ok {
			t.Fatalf("verify %s = %v, %v", domain, ok, err)
		}
	}
	mux := http.NewServeMux()
	if !installOnDemandTLSAsk(mux, store, splitDomains(t, ""), prometheus.NewRegistry(), slog.New(slog.DiscardHandler)) {
		t.Fatal("ask not installed")
	}
	for domain, want := range map[string]int{
		"shop.example.com": http.StatusOK,
		"x.gregale.app":    http.StatusForbidden,
		"x.gregale.dev":    http.StatusForbidden,
	} {
		req := httptest.NewRequest(http.MethodGet, gateway.OnDemandTLSAskPath+"?domain="+domain, nil)
		req.RemoteAddr = "127.0.0.1:50000"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("ask %s = %d, want %d", domain, rec.Code, want)
		}
	}
}
