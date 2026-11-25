package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

type edgeFlagBackend struct {
	Backend
	bundle flags.Bundle
	// environments records the environment of each lookup, when set.
	environments *[]string
}

func (b edgeFlagBackend) EdgeFlagBundle(_ context.Context, _, environment string) flags.Bundle {
	if b.environments != nil {
		*b.environments = append(*b.environments, environment)
	}
	return b.bundle
}

type declaredOnly map[string]bool

func (d declaredOnly) MatchDeclaredRoute(_ context.Context, _ App, path, _ string) (bool, error) {
	return d[path], nil
}

// pricingBundle routes /pricing: variant "b" rewrites to /pricing-b, "a"
// serves the page unchanged.
func pricingBundle() flags.Bundle {
	rollout := 10000
	return flags.Bundle{EnvironmentID: "env-1", Version: 4, Config: flags.Config{Flags: []flags.Flag{{
		Key: "pricing", Type: "variant", Enabled: true, Default: "a", Seed: "seed-1",
		Rules:    []flags.Rule{{ID: "everyone", Rollout: &rollout}},
		Variants: []flags.FlagVariant{{Key: "a", Weight: 5000}, {Key: "b", Weight: 5000}},
		Edge: &flags.Edge{Routes: []flags.EdgeRoute{{Path: "/pricing", Variants: map[string]flags.EdgeAction{
			"b": {Rewrite: "/pricing-b"},
		}}}},
	}}}}
}

func edgeHandler(released bool) *Handler {
	h := &Handler{backend: edgeFlagBackend{bundle: pricingBundle()}}
	if released {
		h.edgeFlagsRelease = edgeFeatureSet("acct-1", platformflags.EdgeFlags)
	}
	return h
}

var edgeApp = App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro}

// wantPath is the path the edge must route customer to.
func wantPath(t *testing.T, customer, subject string) string {
	t.Helper()
	d := flags.EvaluateVariantWithContext(pricingBundle(), "pricing", flags.EvaluationContext{Customer: customer, Subject: subject, Attributes: map[string]string{}}, "a")
	if d.Value == "b" {
		return "/pricing-b"
	}
	return "/pricing"
}

// ADR-20261124212701: a verified customer is routed as the SDK decides,
// and the decision joins the request's flag evidence.
func TestEdgeFlagsRouteVerifiedCustomer(t *testing.T) {
	served := map[string]bool{}
	for i := 0; i < 32; i++ {
		customer := uuid.NewString()
		r := httptest.NewRequest(http.MethodGet, "/pricing", nil)
		r = r.WithContext(withAuthenticated(r.Context(), Authenticated{PlatformTenantID: customer}))
		w := httptest.NewRecorder()
		r, _ = edgeHandler(true).applyEdgeFlags(w, r, edgeApp)
		if want := wantPath(t, customer, ""); r.URL.Path != want {
			t.Fatalf("customer %s routed to %s; want %s", customer, r.URL.Path, want)
		}
		served[r.URL.Path] = true
		if len(w.Result().Cookies()) != 0 {
			t.Fatal("a verified customer got a visitor cookie")
		}
		evidence := edgeFlagEvidence(r.Context())
		if len(evidence) != 1 || evidence[0].Flag != "pricing" || !evidence[0].Used || evidence[0].Reason != "rule_match" || evidence[0].ConfigVersion != 4 {
			t.Fatalf("evidence = %+v", evidence)
		}
		merged := flagEvidenceWithEdge(r.Context(), "")
		if !strings.Contains(merged, `"flag":"pricing"`) {
			t.Fatalf("telemetry evidence = %s", merged)
		}
	}
	if !served["/pricing"] || !served["/pricing-b"] {
		t.Fatalf("32 customers were all served %v; want both variants", served)
	}
}

// An anonymous visitor gets a first-party cookie and keeps its variant.
func TestEdgeFlagsAnonymousVisitorIsSticky(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/pricing", nil)
	w := httptest.NewRecorder()
	r, _ = edgeHandler(true).applyEdgeFlags(w, r, edgeApp)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != EdgeVisitorCookie || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].Path != "/" ||
		cookies[0].MaxAge != int(api.FlagsEdgeVisitorCookieMaxAge/time.Second) {
		t.Fatalf("visitor cookie = %+v", cookies)
	}
	visitor := cookies[0].Value
	first := r.URL.Path
	if want := wantPath(t, visitor, visitor); first != want {
		t.Fatalf("visitor routed to %s; want %s", first, want)
	}
	for i := 0; i < 3; i++ {
		again := httptest.NewRequest(http.MethodGet, "/pricing", nil)
		again.AddCookie(&http.Cookie{Name: EdgeVisitorCookie, Value: visitor})
		w := httptest.NewRecorder()
		again, _ = edgeHandler(true).applyEdgeFlags(w, again, edgeApp)
		if again.URL.Path != first || len(w.Result().Cookies()) != 0 {
			t.Fatalf("returning visitor routed to %s (first %s), cookies %v", again.URL.Path, first, w.Result().Cookies())
		}
	}
	forged := httptest.NewRequest(http.MethodGet, "/pricing", nil)
	forged.AddCookie(&http.Cookie{Name: EdgeVisitorCookie, Value: "not-a-uuid"})
	w = httptest.NewRecorder()
	_, _ = edgeHandler(true).applyEdgeFlags(w, forged, edgeApp)
	if c := w.Result().Cookies(); len(c) != 1 || c[0].Value == "not-a-uuid" {
		t.Fatalf("an invalid visitor cookie was kept: %v", c)
	}
}

// Requests the edge does not route are served unchanged.
func TestEdgeFlagsLeaveOtherRequestsAlone(t *testing.T) {
	for name, tc := range map[string]struct {
		released bool
		method   string
		path     string
	}{
		"flag off":        {false, http.MethodGet, "/pricing"},
		"post":            {true, http.MethodPost, "/pricing"},
		"unrouted path":   {true, http.MethodGet, "/about"},
		"prefix of route": {true, http.MethodGet, "/pricing/x"},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			w := httptest.NewRecorder()
			r, _ = edgeHandler(tc.released).applyEdgeFlags(w, r, edgeApp)
			if r.URL.Path != tc.path || len(edgeFlagEvidence(r.Context())) != 0 || len(w.Result().Cookies()) != 0 {
				t.Fatalf("routed to %s, evidence %v, cookies %v", r.URL.Path, edgeFlagEvidence(r.Context()), w.Result().Cookies())
			}
		})
	}
	other := App{ID: "app-1", AccountID: "acct-2", Plan: api.PlanPro}
	r, _ := edgeHandler(true).applyEdgeFlags(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/pricing", nil), other)
	if len(edgeFlagEvidence(r.Context())) != 0 {
		t.Fatal("an account outside edge_flags was routed")
	}
}

// A killed flag serves the unrouted page and records the kill, even when its
// default variant has a rewrite of its own.
func TestEdgeFlagsKilledServesFallback(t *testing.T) {
	b := pricingBundle()
	b.Flags[0].Killed = true
	b.Flags[0].Edge.Routes[0].Variants["a"] = flags.EdgeAction{Rewrite: "/pricing-a"}
	h := &Handler{backend: edgeFlagBackend{bundle: b}, edgeFlagsRelease: edgeFeatureSet("acct-1", platformflags.EdgeFlags)}
	for i := 0; i < 8; i++ {
		r := httptest.NewRequest(http.MethodGet, "/pricing", nil)
		r = r.WithContext(withAuthenticated(r.Context(), Authenticated{PlatformTenantID: uuid.NewString()}))
		r, _ = h.applyEdgeFlags(httptest.NewRecorder(), r, edgeApp)
		if ev := edgeFlagEvidence(r.Context()); r.URL.Path != "/pricing" || len(ev) != 1 || ev[0].Reason != flags.ReasonKilled {
			t.Fatalf("killed flag routed to %s, evidence %+v", r.URL.Path, ev)
		}
	}
}

func TestEdgeFlagCacheCachesInvalidatesAndRetries(t *testing.T) {
	calls := 0
	fail := false
	c := NewEdgeFlagCache(func(_ context.Context, appID, environment string) (flags.Bundle, error) {
		calls++
		if fail {
			return flags.Bundle{}, errors.New("db down")
		}
		return flags.Bundle{Version: int64(calls), EnvironmentID: appID + "/" + environment}, nil
	})
	now := time.Unix(1_700_000_000, 0)
	c.now = func() time.Time { return now }
	if b := c.Bundle(context.Background(), "app", "production"); b.Version != 1 || b.EnvironmentID != "app/production" {
		t.Fatalf("first lookup = %+v", b)
	}
	if b := c.Bundle(context.Background(), "app", "production"); b.Version != 1 || calls != 1 {
		t.Fatalf("cached lookup = %+v after %d calls", b, calls)
	}
	c.InvalidateAll()
	fail = true
	if b := c.Bundle(context.Background(), "app", "production"); b.Version != 0 || calls != 2 {
		t.Fatalf("failed lookup = %+v after %d calls; want unrouted", b, calls)
	}
	fail = false
	if c.Bundle(context.Background(), "app", "production"); calls != 2 {
		t.Fatal("a failed lookup was retried before flagKillsRetryAfter")
	}
	now = now.Add(flagKillsRetryAfter)
	if b := c.Bundle(context.Background(), "app", "production"); b.Version != 3 {
		t.Fatalf("retried lookup = %+v", b)
	}
	var nilCache *EdgeFlagCache
	if b := nilCache.Bundle(context.Background(), "app", ""); len(b.Flags) != 0 {
		t.Fatal("a nil cache routed")
	}
}

// bVisitor returns a visitor id the pricing flag buckets into variant b.
func bVisitor(t *testing.T) string {
	t.Helper()
	for i := 0; i < 64; i++ {
		id := uuid.NewString()
		if wantPath(t, id, id) == "/pricing-b" {
			return id
		}
	}
	t.Fatal("no visitor in variant b")
	return ""
}

// The visitor cookie never reaches the app or the response cache's
// signed-in check, on routed and unrouted paths alike; other cookies stay.
// A newly issued cookie is returned so a cached response leaves it out.
func TestEdgeFlagsVisitorCookieIsThePlatforms(t *testing.T) {
	for _, path := range []string{"/pricing", "/about"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Cookie", "theme=dark; "+EdgeVisitorCookie+"="+uuid.NewString()+"; lang=en")
		r, set := edgeHandler(true).applyEdgeFlags(httptest.NewRecorder(), r, edgeApp)
		if got := r.Header.Get("Cookie"); got != "theme=dark; lang=en" || set != "" {
			t.Fatalf("%s: forwarded Cookie %q, issued %q", path, got, set)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "/pricing", nil)
	r.Header.Set("Cookie", EdgeVisitorCookie+"="+uuid.NewString())
	r, _ = edgeHandler(true).applyEdgeFlags(httptest.NewRecorder(), r, edgeApp)
	if _, ok := r.Header["Cookie"]; ok {
		t.Fatalf("a visitor-only request still carries Cookie %q", r.Header.Get("Cookie"))
	}
	w := httptest.NewRecorder()
	_, set := edgeHandler(true).applyEdgeFlags(w, httptest.NewRequest(http.MethodGet, "/pricing", nil), edgeApp)
	if set == "" || w.Header().Get("Set-Cookie") != set {
		t.Fatalf("issued %q, response Set-Cookie %q", set, w.Header().Get("Set-Cookie"))
	}
}

// Named environments and deployment previews read their own environment's
// routes; a PR preview token is not an environment and is not routed.
func TestEdgeFlagsEnvironment(t *testing.T) {
	for name, tc := range map[string]struct {
		app  App
		want string
		asks bool
	}{
		"production":        {edgeApp, "", true},
		"named environment": {App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro, PinnedDeploymentID: "d", PinnedDeploymentScope: "staging"}, "staging", true},
		"pr preview":        {App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro, Scope: "pr-42"}, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			var seen []string
			h := &Handler{backend: edgeFlagBackend{bundle: pricingBundle(), environments: &seen}, edgeFlagsRelease: edgeFeatureSet("acct-1", platformflags.EdgeFlags)}
			_, _ = h.applyEdgeFlags(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/pricing", nil), tc.app)
			if !tc.asks {
				if len(seen) != 0 {
					t.Fatalf("looked up environments %q; want none", seen)
				}
				return
			}
			if len(seen) != 1 || seen[0] != tc.want {
				t.Fatalf("looked up environments %q; want [%q]", seen, tc.want)
			}
		})
	}
}

// A rewrite to a path the declared-route gate refuses is not served: the
// request keeps the client's path and records no exposure. The rewrite
// never changes the URL of the request it was given.
func TestEdgeFlagsRewriteKeepsGates(t *testing.T) {
	visitor := bVisitor(t)
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/pricing", nil)
		r.AddCookie(&http.Cookie{Name: EdgeVisitorCookie, Value: visitor})
		return r
	}
	gated := edgeApp
	gated.OnlyAllowDeclaredRoutes = true

	h := edgeHandler(true)
	h.declaredRoutes = declaredOnly{"/pricing": true}
	r, _ := h.applyEdgeFlags(httptest.NewRecorder(), request(), gated)
	if r.URL.Path != "/pricing" || len(edgeFlagEvidence(r.Context())) != 0 {
		t.Fatalf("an undeclared target was served: %s, evidence %v", r.URL.Path, edgeFlagEvidence(r.Context()))
	}

	h.declaredRoutes = declaredOnly{"/pricing": true, "/pricing-b": true}
	in := request()
	r, _ = h.applyEdgeFlags(httptest.NewRecorder(), in, gated)
	if r.URL.Path != "/pricing-b" || in.URL.Path != "/pricing" {
		t.Fatalf("declared target: routed %s, original request now %s", r.URL.Path, in.URL.Path)
	}
}
