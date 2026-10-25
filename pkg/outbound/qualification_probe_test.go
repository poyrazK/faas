package outbound

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	qualificationTestIntegration = "6b1f6a52-62c1-4a8e-9d4f-3f0d7c1a2b10"
	qualificationTestRequest     = "0d6c8f1e-7a43-4b6e-8f25-9a3c1e5b7d20"
	qualificationTestRevision    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type qualificationProvider struct {
	mu    sync.Mutex
	calls map[string]int
	auth  []string
}

func newQualificationHandler(t *testing.T, edit func(*Integration)) (*Handler, *qualificationProvider) {
	t.Helper()
	seen := &qualificationProvider{calls: map[string]int{}}
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.mu.Lock()
		seen.calls[r.Method+" "+r.URL.Path]++
		seen.auth = append(seen.auth, r.Header.Get("Authorization"))
		seen.mu.Unlock()
		w.Header().Set("Cache-Control", "max-age=60")
		_, _ = w.Write([]byte("healthy"))
	}))
	t.Cleanup(provider.Close)
	integration, err := NewIntegration(qualificationTestIntegration, provider.URL, "gateway-token", []string{"app-1"}, 100, 5, 5, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.AccountID = "acct-1"
	integration.ProviderAuthMode = ProviderAuthManaged
	integration.AllowedMethods = []string{http.MethodGet, http.MethodHead}
	integration.AllowedPathPrefixes = []string{"/v1"}
	integration.ResponseCacheTTLSeconds = 60
	if edit != nil {
		edit(&integration)
	}
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.SetManagedAuthorizations(map[string]string{qualificationTestIntegration: "Bearer provider-secret"}); err != nil {
		t.Fatal(err)
	}
	return handler, seen
}

func qualificationTestProbe(path string, status int) api.OutboundQualificationProbe {
	return api.OutboundQualificationProbe{RequestID: qualificationTestRequest, Attempt: 1, AccountID: "acct-1", AppID: "app-1",
		IntegrationID: qualificationTestIntegration, Revision: qualificationTestRevision,
		Policy: api.OutboundBindingProbePolicy{Method: http.MethodGet, Path: path, ExpectedStatus: status}}
}

func TestQualificationPrincipalAdmission(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(*Integration)
		probe  func(*api.OutboundQualificationProbe)
		status int
	}{
		{name: "configured probe", status: http.StatusOK},
		{name: "other account", probe: func(p *api.OutboundQualificationProbe) { p.AccountID = "acct-2" }, status: http.StatusForbidden},
		{name: "app not attached", probe: func(p *api.OutboundQualificationProbe) { p.AppID = "app-2" }, status: http.StatusForbidden},
		{name: "application auth", edit: func(i *Integration) {
			// Route permissions require managed auth, so an application
			// integration carries none and would otherwise admit the path.
			i.ProviderAuthMode, i.AllowedMethods, i.AllowedPathPrefixes = ProviderAuthApplication, nil, nil
		}, status: http.StatusForbidden},
		{name: "route outside the binding", probe: func(p *api.OutboundQualificationProbe) { p.Policy.Path = "/v2/health" }, status: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, seen := newQualificationHandler(t, tc.edit)
			probe := qualificationTestProbe("/v1/health", http.StatusOK)
			if tc.probe != nil {
				tc.probe(&probe)
			}
			if got := handler.qualificationProbe(context.Background(), probe); got != tc.status {
				t.Fatalf("status = %d, want %d", got, tc.status)
			}
			if want := map[bool]int{true: 1, false: 0}[tc.status == http.StatusOK]; seen.calls["GET /v1/health"]+seen.calls["GET /v2/health"] != want {
				t.Fatalf("provider calls = %v, want %d", seen.calls, want)
			}
			if tc.status == http.StatusOK && seen.auth[0] != "Bearer provider-secret" {
				t.Fatalf("provider authorization = %q", seen.auth[0])
			}
		})
	}
}

func TestQualificationPrincipalRequiresTheConfiguredProbe(t *testing.T) {
	handler, seen := newQualificationHandler(t, nil)
	principal := qualificationPrincipal{accountID: "acct-1", appID: "app-1", requestID: qualificationTestRequest, method: http.MethodGet, path: "/v1/health"}
	for _, target := range []struct{ method, path string }{{http.MethodHead, "/v1/health"}, {http.MethodGet, "/v1/items"}} {
		ctx := context.WithValue(context.Background(), qualificationPrincipalKey{}, principal)
		request := httptest.NewRequest(target.method, Prefix+qualificationTestIntegration+target.path, nil).WithContext(ctx)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s %s status = %d, want 403", target.method, target.path, recorder.Code)
		}
	}
	if len(seen.calls) != 0 {
		t.Fatalf("provider calls = %v", seen.calls)
	}
}

func TestQualificationProbeBypassesTheResponseCache(t *testing.T) {
	handler, seen := newQualificationHandler(t, nil)
	for range 2 {
		if got := handler.qualificationProbe(context.Background(), qualificationTestProbe("/v1/health", http.StatusOK)); got != http.StatusOK {
			t.Fatalf("status = %d", got)
		}
	}
	if seen.calls["GET /v1/health"] != 2 {
		t.Fatalf("provider calls = %v, want 2", seen.calls)
	}
}

type qualificationTestStore struct {
	pending  []api.OutboundQualificationProbe
	recorded []int
}

func (s *qualificationTestStore) PendingEnvironmentQualificationOutboundProbes(context.Context, int) ([]api.OutboundQualificationProbe, error) {
	return s.pending, nil
}

func (s *qualificationTestStore) RecordEnvironmentQualificationOutboundReceipt(_ context.Context, probe api.OutboundQualificationProbe, status int) error {
	if probe.Policy.ExpectedStatus == status {
		s.recorded = append(s.recorded, status)
	}
	return nil
}

func TestQualificationProbeWorkerRecordsOnlyTheExpectedStatus(t *testing.T) {
	handler, seen := newQualificationHandler(t, nil)
	now := time.Unix(1_800_000_000, 0)
	failing := qualificationTestProbe("/v1/ready", http.StatusNoContent)
	store := &qualificationTestStore{pending: []api.OutboundQualificationProbe{qualificationTestProbe("/v1/health", http.StatusOK), failing}}
	worker := &QualificationProbeWorker{Store: store, Handler: handler, Now: func() time.Time { return now }}
	if err := worker.ProcessPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.recorded) != 1 || store.recorded[0] != http.StatusOK {
		t.Fatalf("recorded = %v, want one 200 receipt", store.recorded)
	}
	// The failed probe waits before it spends another provider call.
	store.pending = []api.OutboundQualificationProbe{failing}
	if err := worker.ProcessPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen.calls["GET /v1/ready"] != 1 {
		t.Fatalf("failed probe ran again inside its retry delay: %v", seen.calls)
	}
	now = now.Add(qualificationProbeRetry + time.Second)
	if err := worker.ProcessPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen.calls["GET /v1/ready"] != 2 || len(store.recorded) != 1 {
		t.Fatalf("calls = %v recorded = %v", seen.calls, store.recorded)
	}
}
