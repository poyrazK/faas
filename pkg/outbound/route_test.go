package outbound

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type routeFixture struct {
	handler *Handler
	mu      sync.Mutex
	seen    []string
	status  map[string]int
}

func (f *routeFixture) record(entry string) {
	f.mu.Lock()
	f.seen = append(f.seen, entry)
	f.mu.Unlock()
}

func (f *routeFixture) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

func newRouteFixture(t *testing.T, primaryStatus int, configurePrimary func(*Integration)) *routeFixture {
	t.Helper()
	f := &routeFixture{status: map[string]int{"Bearer primary": primaryStatus, "Bearer backup": http.StatusOK}}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		credential := r.Header.Get("Authorization")
		f.record(credential + " " + string(body))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status[credential])
		_, _ = io.WriteString(w, `{"model":"m","usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	t.Cleanup(upstream.Close)
	integration := func(id string) Integration {
		i, err := NewIntegration(id, upstream.URL, "unused", []string{"app-1"}, 100, 10, 10, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		i.ProviderAuthMode = ProviderAuthManaged
		i.AllowedMethods = []string{http.MethodPost}
		i.AllowedPathPrefixes = []string{"/v1"}
		i.AIProvider = api.OutboundAIProviderOpenAI
		return i
	}
	primary := integration("primary")
	if configurePrimary != nil {
		configurePrimary(&primary)
	}
	resolver, err := NewStaticResolver([]Integration{primary, integration("backup")})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.SetManagedAuthorizations(map[string]string{"primary": "Bearer primary", "backup": "Bearer backup"}); err != nil {
		t.Fatal(err)
	}
	handler.Routes = StaticRouteResolver{
		"route-1": {ID: "route-1", AccountID: "account-1", Targets: []AIRouteTarget{{IntegrationID: "primary"}, {IntegrationID: "backup", Model: "gpt-4o-mini"}}},
	}
	f.handler = handler
	return f
}

func (f *routeFixture) post(account, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:2774"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if account != "" {
		req = req.WithContext(withRelayIdentity(context.Background(), WorkloadIdentity{AccountID: account, AppID: "app-1", InstanceID: "i-1"}))
	}
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, req)
	return recorder
}

func TestRouteFallsBackPastAFailingProvider(t *testing.T) {
	f := newRouteFixture(t, http.StatusServiceUnavailable, nil)
	resp := f.post("account-1", "/r/route-1/v1/chat/completions", `{"model":"gpt-4o","messages":[]}`)
	if resp.Code != http.StatusOK || resp.Header().Get(RouteTargetHeader) != "backup" {
		t.Fatalf("route response = %d target=%q body=%s", resp.Code, resp.Header().Get(RouteTargetHeader), resp.Body.String())
	}
	calls := f.calls()
	if len(calls) != 2 || !strings.HasPrefix(calls[0], `Bearer primary {"model":"gpt-4o"`) || !strings.Contains(calls[1], `"model":"gpt-4o-mini"`) {
		t.Fatalf("provider calls = %q", calls)
	}
}

func TestRouteReturnsClientErrorsWithoutFallback(t *testing.T) {
	f := newRouteFixture(t, http.StatusBadRequest, nil)
	resp := f.post("account-1", "/r/route-1/v1/chat/completions", `{"model":"gpt-4o"}`)
	if resp.Code != http.StatusBadRequest || resp.Header().Get(RouteTargetHeader) != "primary" || len(f.calls()) != 1 {
		t.Fatalf("400 response = %d target=%q calls=%d", resp.Code, resp.Header().Get(RouteTargetHeader), len(f.calls()))
	}
}

func TestRouteRequiresRelayIdentityOfTheOwningAccount(t *testing.T) {
	f := newRouteFixture(t, http.StatusOK, nil)
	if resp := f.post("", "/r/route-1/v1/chat/completions", `{}`); resp.Code != http.StatusForbidden {
		t.Fatalf("non-relay route call = %d", resp.Code)
	}
	if resp := f.post("account-2", "/r/route-1/v1/chat/completions", `{}`); resp.Code != http.StatusNotFound {
		t.Fatalf("foreign-account route call = %d", resp.Code)
	}
	if resp := f.post("account-1", "/r/missing/v1/chat/completions", `{}`); resp.Code != http.StatusNotFound {
		t.Fatalf("missing route = %d", resp.Code)
	}
	if len(f.calls()) != 0 {
		t.Fatalf("rejected route calls reached the provider: %q", f.calls())
	}
}

func TestRouteFallsBackPastAGatewayBudgetRejection(t *testing.T) {
	f := newRouteFixture(t, http.StatusOK, func(i *Integration) { i.DailyAITokenLimit = int64Ref(1) })
	first := f.post("account-1", "/r/route-1/v1/chat/completions", `{"model":"gpt-4o"}`)
	second := f.post("account-1", "/r/route-1/v1/chat/completions", `{"model":"gpt-4o"}`)
	if first.Header().Get(RouteTargetHeader) != "primary" || second.Code != http.StatusOK || second.Header().Get(RouteTargetHeader) != "backup" {
		t.Fatalf("budget fallback: first=%q second=%d %q", first.Header().Get(RouteTargetHeader), second.Code, second.Header().Get(RouteTargetHeader))
	}
}
