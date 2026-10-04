// adr: 531
package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/trafficdeadline"
)

func testDeadlineSigner(t *testing.T) *trafficdeadline.Signer {
	t.Helper()
	signer, err := trafficdeadline.New(bytes.Repeat([]byte{42}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func deadlineServiceConfig(caller, target string, signer *trafficdeadline.Signer) ServiceProxyConfig {
	return ServiceProxyConfig{
		TrafficDeadlines: signer,
		Provider:         &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: target, Endpoints: []ServiceEndpoint{{InstanceID: target + "-1", NodeID: "node", Port: 8080}}}},
		ResolveCaller:    func(context.Context, string) (string, error) { return caller, nil },
		Resolve: func(context.Context, string, string) (ServiceTarget, bool, error) {
			return ServiceTarget{AppID: target}, true, nil
		},
		Authorize: func(context.Context, string, string) (ServiceCaller, error) {
			return ServiceCaller{AppID: caller, AccountID: "owner", Reliability: &api.ServiceReliabilityPolicy{TimeoutMS: 5000, MaxAttempts: 1}}, nil
		},
	}
}

func forwardDeadlineCall(w http.ResponseWriter, r *http.Request, url string) {
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		panic(err)
	}
	request.Header.Set(trafficdeadline.Header, r.Header.Get(trafficdeadline.Header))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		if handleForwardRequestCancellation(w, r, true) {
			return
		}
		http.Error(w, "dependency transport failed", http.StatusBadGateway)
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusGatewayTimeout && response.Header.Get(api.ErrorCodeHeader) == api.CodeRequestBudgetExceeded {
		// A downstream hop can expire just before this hop's timer fires.
		// Propagate its canonical budget refusal with the bounded error writer.
		writeRequestBudgetExceededForRequest(w, r)
		return
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func TestManagedDeadlineThroughHTTPChain(t *testing.T) {
	signer := testDeadlineSigner(t)
	claims := make(chan trafficdeadline.Claims, 3)
	leafDone := make(chan struct{})
	configC := deadlineServiceConfig("app-b", "app-c", signer)
	configC.Forward = func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer close(leafDone)
			claim, err := signer.Verify(r.Header.Get(trafficdeadline.Header), "app-c")
			if err != nil {
				t.Error(err)
				return
			}
			claims <- claim
			<-r.Context().Done()
			writeRequestBudgetExceededForRequest(w, r)
		})
	}
	proxyC := NewServiceProxy(configC)
	serverC := httptest.NewServer(proxyC)
	t.Cleanup(serverC.Close)
	t.Cleanup(func() { _ = proxyC.retryBudget.Close() })
	configB := deadlineServiceConfig("app-a", "app-b", signer)
	configB.Forward = func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claim, err := signer.Verify(r.Header.Get(trafficdeadline.Header), "app-b")
			if err != nil {
				t.Error(err)
				return
			}
			claims <- claim
			forwardDeadlineCall(w, r, serverC.URL+"/v1/internal/services/c/slow")
		})
	}
	proxyB := NewServiceProxy(configB)
	serverB := httptest.NewServer(proxyB)
	t.Cleanup(serverB.Close)
	t.Cleanup(func() { _ = proxyB.retryBudget.Close() })
	h, backend, _ := newTestHandler(t)
	backend.app.ID = "app-a"
	backend.app.AccountID = "owner"
	backend.setLegacyHot()
	setTotalBudget(h, backend.app, 300)
	h.WithTrafficDeadlines(signer).WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claim, err := signer.Verify(r.Header.Get(trafficdeadline.Header), "app-a")
			if err != nil {
				t.Error(err)
				return
			}
			claims <- claim
			forwardDeadlineCall(w, r, serverB.URL+"/v1/internal/services/b/slow")
		})
	})
	serverA := httptest.NewServer(h)
	t.Cleanup(serverA.Close)
	request, err := http.NewRequest(http.MethodGet, serverA.URL+"/chain", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = backend.host
	// An incoming public token is untrusted, even when it names a real chain.
	request.Header.Set(trafficdeadline.Header, "forged-longer-deadline")
	response, err := serverA.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusGatewayTimeout || !strings.Contains(string(body), api.CodeRequestBudgetExceeded) {
		t.Fatalf("status=%d body=%q error=%v", response.StatusCode, body, err)
	}
	select {
	case <-leafDone:
	case <-time.After(time.Second):
		t.Fatal("leaf retained the expired chain")
	}
	first := <-claims
	for range 2 {
		child := <-claims
		if child.ChainID != first.ChainID || child.DeadlineNS > first.DeadlineNS || first.DeadlineNS-child.DeadlineNS > int64(5*time.Millisecond) {
			t.Fatalf("chain extended or reset: parent=%+v child=%+v", first, child)
		}
	}
}

func TestManagedDeadlineRefusesBeforeWake(t *testing.T) {
	signer := testDeadlineSigner(t)
	valid, err := signer.Mint("app-a", "owner", uuid.NewString(), time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	wrongApp, err := signer.Mint("another-app", "owner", uuid.NewString(), time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	wrongAccount, err := signer.Mint("app-a", "another-owner", uuid.NewString(), time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		headers    []string
		signer     *trafficdeadline.Signer
		wantStatus int
	}{
		{"tamper", []string{"invalid"}, signer, http.StatusBadRequest},
		{"audience", []string{wrongApp}, signer, http.StatusBadRequest},
		{"account", []string{wrongAccount}, signer, http.StatusBadRequest},
		{"duplicate", []string{valid, valid}, signer, http.StatusBadRequest},
		{"key-unavailable", []string{valid}, nil, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var wake, forward atomic.Int32
			cfg := deadlineServiceConfig("app-a", "app-b", tc.signer)
			cfg.Provider = &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "app-b"}}
			cfg.Wake = func(context.Context, string) error { wake.Add(1); return nil }
			cfg.Forward = func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { forward.Add(1); w.WriteHeader(http.StatusNoContent) })
			}
			proxy := NewServiceProxy(cfg)
			t.Cleanup(func() { _ = proxy.retryBudget.Close() })
			r := httptest.NewRequest(http.MethodGet, "/v1/internal/services/b", nil)
			for _, header := range tc.headers {
				r.Header.Add(trafficdeadline.Header, header)
			}
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, r)
			if rec.Code != tc.wantStatus || wake.Load() != 0 || forward.Load() != 0 {
				t.Fatalf("status=%d wakes=%d forwards=%d; want refusal before side effects", rec.Code, wake.Load(), forward.Load())
			}
		})
	}
}

func TestTotalDeadlineRequiresSharedKeyBeforeAdmission(t *testing.T) {
	h, backend, _ := newTestHandler(t)
	setTotalBudget(h, backend.app, 100)
	h.WithTrafficDeadlines(nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
	if rec.Code != http.StatusServiceUnavailable || backend.admits != 0 || !strings.Contains(rec.Body.String(), api.CodeTrafficDeadlineUnavailable) {
		t.Fatalf("status=%d admits=%d body=%q", rec.Code, backend.admits, rec.Body.String())
	}
}

func TestManagedDeadlineNeverResetsOnRetry(t *testing.T) {
	signer := testDeadlineSigner(t)
	deadline := time.Now().Add(time.Second)
	chain := uuid.NewString()
	token, err := signer.Mint("app-a", "owner", chain, deadline)
	if err != nil {
		t.Fatal(err)
	}
	cfg := deadlineServiceConfig("app-a", "app-b", signer)
	cfg.Provider = &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "app-b", Endpoints: []ServiceEndpoint{{InstanceID: "first", NodeID: "node", Port: 8080}, {InstanceID: "second", NodeID: "node", Port: 8080}}}}
	cfg.Authorize = func(context.Context, string, string) (ServiceCaller, error) {
		return ServiceCaller{AppID: "app-a", AccountID: "owner", Reliability: &api.ServiceReliabilityPolicy{TimeoutMS: 5000, MaxAttempts: 2, MinRemainingMS: 5}}, nil
	}
	var calls int
	cfg.Forward = func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			claim, err := signer.Verify(r.Header.Get(trafficdeadline.Header), "app-b")
			if err != nil || claim.ChainID != chain || claim.Deadline().After(deadline) {
				t.Errorf("retry changed chain: claims=%+v error=%v", claim, err)
			}
			if calls == 1 {
				markStaleTarget(r.Context())
				writeForwarderProblem(w, http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}
	proxy := NewServiceProxy(cfg)
	t.Cleanup(func() { _ = proxy.retryBudget.Close() })
	r := httptest.NewRequest(http.MethodGet, "/v1/internal/services/b", nil)
	r.Header.Set(trafficdeadline.Header, token)
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, r)
	if rec.Code != http.StatusNoContent || calls != 2 {
		t.Fatalf("status=%d calls=%d, want successful replay", rec.Code, calls)
	}
}

func TestManagedDeadlineBoundsWakeAndDiscovery(t *testing.T) {
	for _, stage := range []string{"identity", "wake", "discovery", "authorization"} {
		t.Run(stage, func(t *testing.T) {
			signer := testDeadlineSigner(t)
			token, err := signer.Mint("app-a", "owner", uuid.NewString(), time.Now().Add(80*time.Millisecond))
			if err != nil {
				t.Fatal(err)
			}
			cfg := deadlineServiceConfig("app-a", "app-b", signer)
			done := false
			switch stage {
			case "identity":
				cfg.ResolveCaller = func(ctx context.Context, _ string) (string, error) { <-ctx.Done(); done = true; return "", ctx.Err() }
			case "wake":
				cfg.Provider = &serviceProxyProvider{snapshot: ServiceEndpointsSnapshot{AppID: "app-b"}}
				cfg.Wake = func(ctx context.Context, _ string) error { <-ctx.Done(); done = true; return ctx.Err() }
			case "discovery":
				cfg.Resolve = func(ctx context.Context, _, _ string) (ServiceTarget, bool, error) {
					<-ctx.Done()
					done = true
					return ServiceTarget{}, false, ctx.Err()
				}
			case "authorization":
				cfg.Authorize = func(ctx context.Context, _, _ string) (ServiceCaller, error) {
					<-ctx.Done()
					done = true
					return ServiceCaller{}, ctx.Err()
				}
			}
			cfg.Forward = func(Target) http.Handler {
				return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("expired request reached the guest") })
			}
			proxy := NewServiceProxy(cfg)
			t.Cleanup(func() { _ = proxy.retryBudget.Close() })
			r := httptest.NewRequest(http.MethodGet, "/v1/internal/services/b", nil)
			r.Header.Set(trafficdeadline.Header, token)
			rec := httptest.NewRecorder()
			proxy.ServeHTTP(rec, r)
			assertTotalTimeout(t, rec)
			if !done {
				t.Fatal("deadline did not cancel the selected stage")
			}
		})
	}
}
