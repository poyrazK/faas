package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// declaredMethodsStub declares one method set for every path.
type declaredMethodsStub struct {
	methods map[string]bool
	called  atomic.Int32
}

func (m *declaredMethodsStub) MatchDeclaredRoute(_ context.Context, _ App, _ string, method string) (bool, error) {
	m.called.Add(1)
	return m.methods[method], nil
}

// adr: 091 — amendment 2026-11-04 (review round 11): a declared-route app
// runs a POST carrying a method override as the overriding method, so that
// method must be declared too.
func TestDeclaredRoutesCoverTheOverrideMethod(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
		query  string
		want   int
	}{
		{name: "plain declared POST", want: http.StatusOK},
		{name: "override to an undeclared DELETE", header: "DELETE", want: http.StatusNotFound},
		{name: "query override to an undeclared DELETE", query: "?_method=DELETE", want: http.StatusNotFound},
		{name: "override to a declared PUT", header: "PUT", want: http.StatusOK},
		{name: "override equal to the request method", header: "POST", want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, b, _ := newTestHandler(t)
			b.app.OnlyAllowDeclaredRoutes = true
			h.WithDeclaredRouteMatcher(&declaredMethodsStub{methods: map[string]bool{"POST": true, "PUT": true}})
			req := httptest.NewRequest(http.MethodPost, "http://jane-api.apps.dom/items/1"+tc.query, nil)
			if tc.header != "" {
				req.Header.Set("X-HTTP-Method-Override", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body)
			}
			if tc.want == http.StatusNotFound && !strings.Contains(rec.Body.String(), "(as DELETE)") {
				t.Fatalf("the refusal should name the override method: %s", rec.Body)
			}
		})
	}
}

// adr: 091 — amendment 2026-11-04 (review round 11): the guest receives the
// caller a jwt rule verified only from the request context.
func TestGuestEdgeJWTHeadersComeOnlyFromTheContext(t *testing.T) {
	if got := guestEdgeJWTHeaders(context.Background()); got != nil {
		t.Fatalf("no identity recorded, got %v", got)
	}
	ctx := WithEdgeJWTIdentity(context.Background(), EdgeJWTIdentity{Issuer: "https://idp.example", Subject: "user-1"})
	got := guestEdgeJWTHeaders(ctx)
	want := [][2]string{{api.EdgeJWTSubjectHeader, "user-1"}, {api.EdgeJWTIssuerHeader, "https://idp.example"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("headers = %v, want %v", got, want)
	}
	for name, id := range map[string]EdgeJWTIdentity{
		"no subject":         {Issuer: "https://idp.example"},
		"a line break":       {Subject: "user\r\nX-Evil: 1"},
		"an oversized value": {Subject: strings.Repeat("a", api.MaxEdgeJWTIdentityBytes+1)},
		"a broken issuer":    {Issuer: "bad\nissuer", Subject: "user-1"},
	} {
		if got := guestEdgeJWTHeaders(WithEdgeJWTIdentity(context.Background(), id)); got != nil {
			t.Fatalf("%s: identity forwarded: %v", name, got)
		}
	}
	// A client's own copy is an x-faas-* header the guest never receives.
	if guestReceivesFaasHeader(context.Background(), api.EdgeJWTSubjectHeader) ||
		guestReceivesFaasHeader(WithSyntheticInvocation(context.Background()), api.EdgeJWTIssuerHeader) {
		t.Fatal("a client-supplied X-Faas-Edge-Jwt-* header would reach the guest")
	}
}

// adr: 091 — amendment 2026-11-04 (review round 11): one app's hostnames
// (a wildcard custom domain's random subdomains) cannot evict other apps'
// rule sets.
func TestEdgeRuleCacheBoundsOneAppsShare(t *testing.T) {
	c := NewEdgeRuleCache(1024) // share = 128
	for i := range 10 {
		c.Put(EdgeRuleCacheKey(fmt.Sprintf("v%d.victim.example", i), "victim"), &HostEntry{})
	}
	for i := range 2000 {
		c.Put(EdgeRuleCacheKey(fmt.Sprintf("r%d.attacker.example", i), "attacker"), &HostEntry{})
	}
	for i := range 10 {
		if !c.HasHost(EdgeRuleCacheKey(fmt.Sprintf("v%d.victim.example", i), "victim")) {
			t.Fatalf("victim set %d was evicted by another app's hosts", i)
		}
	}
	if got := c.apps["attacker"].Len(); got != 128 {
		t.Fatalf("attacker holds %d sets, want its share of 128", got)
	}
	if got := c.Len(); got != 138 {
		t.Fatalf("cache holds %d sets, want 138", got)
	}
	// The app's most recent hosts are the ones it keeps.
	if !c.HasHost(EdgeRuleCacheKey("r1999.attacker.example", "attacker")) || c.HasHost(EdgeRuleCacheKey("r0.attacker.example", "attacker")) {
		t.Fatal("an app's share should keep its most recently used sets")
	}
	// Removal through invalidation keeps the share bookkeeping consistent.
	c.Reset()
	if len(c.apps) != 0 || c.Len() != 0 {
		t.Fatalf("reset left %d app shares and %d sets", len(c.apps), c.Len())
	}
}

func TestEdgeRuleCacheShareFollowsUse(t *testing.T) {
	c := NewEdgeRuleCache(16)
	c.appCap = 2
	hot := EdgeRuleCacheKey("hot.example", "app")
	c.Put(hot, &HostEntry{})
	c.Put(EdgeRuleCacheKey("a.example", "app"), &HostEntry{})
	if _, ok := c.GetHost(hot); !ok {
		t.Fatal("hot set missing")
	}
	c.Put(EdgeRuleCacheKey("b.example", "app"), &HostEntry{})
	if !c.HasHost(hot) {
		t.Fatal("a recently read set was evicted before an idle one")
	}
	// Replacing a set keeps its place in the share.
	c.Put(hot, &HostEntry{})
	if got := c.apps["app"].Len(); got != 2 {
		t.Fatalf("share holds %d sets after a replacement, want 2", got)
	}
}

// adr: 091 — amendment 2026-11-04 (review round 11): an async route keeps the
// caller a jwt rule verified, never a client's claim of one, and delivery
// reads it back from the stored invocation only.
func TestAsyncRouteCarriesTheVerifiedEdgeJWTCaller(t *testing.T) {
	enqueuer := &recordingAsyncRouteEnqueuer{}
	h := &Handler{asyncRoutes: enqueuer}
	r := httptest.NewRequest("POST", "http://api.example.com/jobs", strings.NewReader(`{}`))
	r.Header.Set(api.EdgeJWTSubjectHeader, "forged")
	r.Header.Set("Authorization", "Bearer token")
	r = r.WithContext(WithEdgeJWTIdentity(r.Context(), EdgeJWTIdentity{Issuer: "https://idp.example", Subject: "user-1"}))
	w := httptest.NewRecorder()
	h.applyEdgeRuleAsync(w, r, App{ID: "app", AccountID: "account", Plan: api.PlanHobby, RequestInvocationsEnabled: true}, &EdgeRuleAsyncResolved{ID: "async"})
	if w.Code != http.StatusAccepted || enqueuer.request.EdgeJWT != (EdgeJWTIdentity{Issuer: "https://idp.example", Subject: "user-1"}) {
		t.Fatalf("enqueue: %d %+v", w.Code, enqueuer.request)
	}
	if _, ok := enqueuer.request.Headers[api.EdgeJWTSubjectHeader]; ok {
		t.Fatal("a client's identity claim was persisted as a header")
	}

	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "edge-jwt-async@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "edge-jwt-async", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := EnqueueAsyncRoute(ctx, store, AsyncRouteRequest{AppID: app.ID, AccountID: account.ID, Method: "POST", Path: "/jobs",
		Payload: []byte(`{}`), EdgeJWT: EdgeJWTIdentity{Issuer: "https://idp.example", Subject: "user-1"}})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := store.InvocationByID(ctx, accepted.ID)
	if err != nil || stored.EdgeJWTSubject != "user-1" || stored.EdgeJWTIssuer != "https://idp.example" {
		t.Fatalf("stored identity: %+v %v", stored, err)
	}
	// The transport that names the invocation cannot assert another caller.
	claimed := stored
	claimed.EdgeJWTSubject, claimed.EdgeJWTIssuer = "forged", "https://evil.example"
	prepared, _, _, err := state.ResolveInvocationDispatch(ctx, store, claimed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.EdgeJWTSubject != "user-1" || prepared.EdgeJWTIssuer != "https://idp.example" {
		t.Fatalf("dispatch identity = %q/%q, want the stored caller", prepared.EdgeJWTIssuer, prepared.EdgeJWTSubject)
	}
	// An invocation that no jwt rule admitted carries no caller.
	plain, err := EnqueueAsyncRoute(ctx, store, AsyncRouteRequest{AppID: app.ID, AccountID: account.ID, Method: "POST", Path: "/jobs", Payload: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	plainRow, err := store.InvocationByID(ctx, plain.ID)
	if err != nil {
		t.Fatal(err)
	}
	plainRow.EdgeJWTSubject = "forged"
	prepared, _, _, err = state.ResolveInvocationDispatch(ctx, store, plainRow, nil)
	if err != nil || prepared.EdgeJWTSubject != "" {
		t.Fatalf("anonymous dispatch identity = %q (%v)", prepared.EdgeJWTSubject, err)
	}
}
