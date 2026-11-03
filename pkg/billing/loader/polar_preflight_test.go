package loader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/state"
)

// flakyPolar answers 503 until healthy is set, then proxies to the catalog
// fake.
func flakyPolar(t *testing.T, healthy *atomic.Bool) string {
	t.Helper()
	target, err := url.Parse(polarCatalogServer(t, true))
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func polarEnv(baseURL string) func(string) string {
	return mapEnv(map[string]string{
		"FAAS_BILLING_PROVIDER":       "polar",
		"FAAS_POLAR_ACCESS_TOKEN":     "polar_test_token",
		"FAAS_POLAR_HOBBY_PRODUCT_ID": "hobby-product",
		"FAAS_POLAR_PRO_PRODUCT_ID":   "pro-product",
		"FAAS_POLAR_SCALE_PRODUCT_ID": "scale-product",
		"FAAS_POLAR_USAGE_EVENT_NAME": "ram_usage",
		"FAAS_POLAR_METER_ID":         "meter-1",
		"FAAS_POLAR_BASE_URL":         baseURL,
	})
}

// A Polar outage at boot starts apid and meterd; the preflight passes in the
// background once Polar answers. A catalog that cannot work still refuses.
// adr: 20261031100000
func TestPolarBootPreflightSurvivesAnOutage(t *testing.T) {
	minDelay, maxDelay := polarPreflightRetryMin, polarPreflightRetryMax
	polarPreflightRetryMin, polarPreflightRetryMax = 10*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { polarPreflightRetryMin, polarPreflightRetryMax = minDelay, maxDelay })

	for _, daemon := range []string{"apid", "meterd"} {
		t.Run(daemon, func(t *testing.T) {
			var healthy atomic.Bool
			env := polarEnv(flakyPolar(t, &healthy))
			cfg := resolveCfg(t, env)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			var p any
			var err error
			if daemon == "apid" {
				p, _, err = LoadProviderForAPID(ctx, cfg, env, discardLog())
			} else {
				p, _, err = LoadProviderForMeterd(cfg, env, state.NewMemStore(), discardLog())
			}
			if err != nil {
				t.Fatalf("%s refused to start while Polar answered 503: %v", daemon, err)
			}
			provider := p.(*polar.Provider)
			healthy.Store(true)
			deadline := time.Now().Add(30 * time.Second)
			for {
				entries := provider.ListBillingCatalog(ctx)
				if len(entries) > 0 && !entries[0].SyncedAt.IsZero() {
					return
				}
				if time.Now().After(deadline) {
					t.Fatal("the background preflight never passed after Polar recovered")
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}

	// A Scale product priced like Hobby is configuration: the daemon refuses.
	env := mapEnv(map[string]string{
		"FAAS_BILLING_PROVIDER":       "polar",
		"FAAS_POLAR_ACCESS_TOKEN":     "polar_test_token",
		"FAAS_POLAR_HOBBY_PRODUCT_ID": "hobby-product",
		"FAAS_POLAR_PRO_PRODUCT_ID":   "pro-product",
		"FAAS_POLAR_SCALE_PRODUCT_ID": "no-such-product",
		"FAAS_POLAR_USAGE_EVENT_NAME": "ram_usage",
		"FAAS_POLAR_METER_ID":         "meter-1",
		"FAAS_POLAR_BASE_URL":         polarCatalogServer(t, true),
	})
	if _, _, err := LoadProviderForMeterd(resolveCfg(t, env), env, state.NewMemStore(), discardLog()); err == nil || !strings.Contains(err.Error(), "preflight failed") {
		t.Fatalf("LoadProviderForMeterd with an unknown product = %v, want a refusal", err)
	}
}
