package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestTrafficPolicySealIsIndependentAndAppDigestIncludesEffectiveInputs(t *testing.T) {
	entry := &HostEntry{Host: "app.test", Headers: []EdgeRuleHeadersResolved{{
		ID: "headers", MatchHeaders: map[string]string{"X-Mode": "original"},
		ResponseHeaders: []EdgeRuleHeaderOp{{Name: "X-Policy", Value: "old", Action: "set"}},
	}}}
	if err := entry.SealPolicy(); err != nil {
		t.Fatal(err)
	}
	ctx, err := WithPinnedHostPolicy(t.Context(), "app.test", entry)
	if err != nil {
		t.Fatal(err)
	}
	entry.Headers[0].MatchHeaders["X-Mode"] = "changed"
	entry.Headers[0].ResponseHeaders[0].Value = "new"
	pinned, _ := PinnedHostPolicy(ctx, "app.test")
	if pinned.Headers[0].MatchHeaders["X-Mode"] != "original" || pinned.Headers[0].ResponseHeaders[0].Value != "old" {
		t.Fatal("source mutation changed a pinned policy")
	}
	app := App{ID: "app", AccountID: "owner", Plan: api.PlanPro, CORSDefaultOrigins: []string{"https://old.test"}}
	frozen, before, err := freezeTrafficApp(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	app.CORSDefaultOrigins[0] = "https://new.test"
	if frozen.CORSDefaultOrigins[0] != "https://old.test" {
		t.Fatal("app snapshot retained mutable source origins")
	}
	if _, same, err := freezeTrafficApp(ctx, frozen); err != nil || same != before {
		t.Fatalf("unchanged effective policy digest changed: %s %s %v", before, same, err)
	}
	for _, changed := range []App{app, {ID: frozen.ID, AccountID: frozen.AccountID, Plan: api.PlanScale},
		{ID: frozen.ID, AccountID: frozen.AccountID, Plan: frozen.Plan, RequireAuthn: true}} {
		if _, after, err := freezeTrafficApp(ctx, changed); err != nil || after == before {
			t.Fatalf("changed app policy kept digest: %s %v", after, err)
		}
	}
}

type snapshotTestMatcher struct {
	noOpEdgeRuleMatcher
	entry *HostEntry
	err   error
}

func (m snapshotTestMatcher) PinHostPolicy(ctx context.Context, host string) (context.Context, error) {
	if m.err != nil {
		return nil, m.err
	}
	return WithPinnedHostPolicy(ctx, host, m.entry)
}

func (m snapshotTestMatcher) MatchBudget(ctx context.Context, host, path, method string) *EdgeRuleBudgetResolved {
	entry, ok := PinnedHostPolicy(ctx, host)
	if !ok {
		return nil
	}
	return PickFirstBudgetMatch(entry.Budget, path, method, EdgeRuleRequestHeaders(ctx))
}

type updatingPolicyBackend struct {
	*fakeBackend
	onAdmit func()
}

func (b *updatingPolicyBackend) Admit(ctx context.Context, app, deployment, scope, trigger string, maxConcurrency int) (string, WakeMethod, bool, error) {
	b.onAdmit()
	return b.fakeBackend.Admit(ctx, app, deployment, scope, trigger, maxConcurrency)
}

func TestTrafficPolicyPinsHostAndAppAcrossActualHandlerWake(t *testing.T) {
	fixture, backend, _ := newTestHandler(t)
	backend.app.CORSDefaultOrigins = []string{"https://old.test"}
	entry := &HostEntry{Host: backend.host, Budget: []EdgeRuleBudgetResolved{{
		ID: "old-budget", AccountID: backend.app.AccountID, AppID: backend.app.ID, BudgetMs: 1000,
	}}}
	if err := entry.SealPolicy(); err != nil {
		t.Fatal(err)
	}
	expectedCtx, err := WithPinnedHostPolicy(t.Context(), backend.host, entry)
	if err != nil {
		t.Fatal(err)
	}
	expectedApp, _ := backend.Lookup(t.Context(), backend.host)
	_, expectedRevision, err := freezeTrafficApp(expectedCtx, expectedApp)
	if err != nil {
		t.Fatal(err)
	}
	updating := &updatingPolicyBackend{fakeBackend: backend, onAdmit: func() {
		entry.Budget[0].ID = "changed-during-wake"
		backend.mu.Lock()
		backend.app.CORSDefaultOrigins[0] = "https://changed.test"
		backend.mu.Unlock()
	}}
	h := NewHandlerWith(updating, NewMetrics(), fixture.log)
	h.WithEdgeRules(snapshotTestMatcher{entry: entry}, nil, nil)
	var forwardedRevision string
	h.proxyByNode = func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			forwardedRevision = TrafficPolicyRevision(r.Context())
			pinned, _ := PinnedHostPolicy(r.Context(), backend.host)
			if pinned.Budget[0].ID != "old-budget" {
				t.Error("wake replaced the request's budget policy")
			}
			w.Header().Set(TrafficPolicyRevisionHeader, "guest override")
			w.WriteHeader(http.StatusOK)
		})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
	if rec.Code != http.StatusOK || backend.admits != 1 || forwardedRevision != expectedRevision || rec.Header().Get(TrafficPolicyRevisionHeader) != forwardedRevision {
		t.Fatalf("wake policy evidence: %d admits=%d forwarded=%s response=%s", rec.Code, backend.admits,
			forwardedRevision, rec.Header().Get(TrafficPolicyRevisionHeader))
	}
}

func TestTrafficPolicyUnavailableRefusesBeforeWakeAndProtectsResponseEvidence(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	h.WithEdgeRules(snapshotTestMatcher{err: errors.New("store unavailable")}, nil, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), api.CodeTrafficPolicyUnavailable) || backend.admits != 0 {
		t.Fatalf("unverified policy reached admission: %d %s admits=%d", rec.Code, rec.Body, backend.admits)
	}
	for _, revision := range []string{"traffic-v1:verified", ""} {
		rec = httptest.NewRecorder()
		writer := &statusRecorder{ResponseWriter: rec, trafficPolicyRevision: revision}
		writer.Header().Set(TrafficPolicyRevisionHeader, "guest forgery")
		writer.installHeaderOps([]EdgeRuleHeaderOp{{Name: TrafficPolicyRevisionHeader, Value: "rule forgery", Action: "set"}})
		writer.WriteHeader(http.StatusOK)
		if got := rec.Header().Get(TrafficPolicyRevisionHeader); got != revision {
			t.Fatalf("response evidence overwritten: %q, want %q", got, revision)
		}
	}
}

func BenchmarkTrafficPolicyFreeze(b *testing.B) {
	entry := &HostEntry{Host: "app.test", Budget: []EdgeRuleBudgetResolved{{ID: "budget", BudgetMs: 1000}}}
	if err := entry.SealPolicy(); err != nil {
		b.Fatal(err)
	}
	ctx, err := WithPinnedHostPolicy(b.Context(), entry.Host, entry)
	if err != nil {
		b.Fatal(err)
	}
	app := App{ID: "app", AccountID: "owner", Plan: api.PlanScale, PublicAuth: PublicAuthConfig{Mode: "open"},
		CORSDefaultOrigins: []string{"https://app.test"}, DeclaredRoutes: []DeclaredRoute{{Path: "/orders/{id}", Methods: []string{"GET", "POST"}}}}
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := freezeTrafficApp(ctx, app); err != nil {
			b.Fatal(err)
		}
	}
}
