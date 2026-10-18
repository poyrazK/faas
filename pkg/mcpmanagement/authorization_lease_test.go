package mcpmanagement

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

type leaseFixture struct {
	remote  *remoteServer
	grant   *oauthGrant
	manager *management
	client  *mcp.ClientSession
	ctx     context.Context
	token   string
	backend *httptest.Server
}

func connectLeaseFixture(t *testing.T, backend *httptest.Server, opts Options, version string) *leaseFixture {
	t.Helper()
	opts.APIBase = backend.URL
	native := api.NewClient(backend.URL, testToken)
	server, manager, err := newManagementServer(native, opts)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := httptest.NewUnstartedServer(http.NotFoundHandler())
	t.Cleanup(endpoint.Close)
	origin, err := url.Parse("http://" + endpoint.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	token := "gmcp_at_authorization_fixture"
	grant := &oauthGrant{id: "lease-grant", accountID: "account-one", server: server, management: manager, client: native, accessExpires: time.Now().Add(time.Hour), expires: time.Now().Add(time.Hour), accessHash: tokenHash(token)}
	remote := &remoteServer{opts: HTTPOptions{APIBase: backend.URL, Timeout: time.Second}, host: origin.Host, origin: origin.String(), access: map[string]*oauthGrant{tokenHash(token): grant}, grants: map[string]*oauthGrant{grant.id: grant}, refresh: map[string]*oauthGrant{}}
	endpoint.Config.Handler = remote.handler()
	endpoint.Start()
	httpClient := &http.Client{Transport: bearerTransport{base: http.DefaultTransport, token: token}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	client, err := mcp.NewClient(&mcp.Implementation{Name: "lease-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint.URL + "/mcp", HTTPClient: httpClient}, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return &leaseFixture{remote: remote, grant: grant, manager: manager, client: client, ctx: ctx, token: token, backend: backend}
}

func TestAuthorizationLeaseCancelsAllRPCViews(t *testing.T) {
	for _, version := range []string{"2025-06-18", "2026-07-28"} {
		for _, method := range []string{"tool", "resource", "prompt", "completion", "watch"} {
			t.Run(version+"/"+method, func(t *testing.T) {
				f := progressFixture()
				var block atomic.Bool
				entered := make(chan struct{})
				canceled := make(chan struct{})
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if block.Load() && (r.URL.Path == "/v1/apps/alpha" || r.URL.Path == "/v1/apps") {
						close(entered)
						<-r.Context().Done()
						close(canceled)
						return
					}
					f.serveHTTP(w, r)
				}))
				t.Cleanup(backend.Close)
				h := connectLeaseFixture(t, backend, Options{}, version)
				h.remote.mu.Lock()
				leaseWindow := 200 * time.Millisecond
				if method == "watch" {
					leaseWindow = 500 * time.Millisecond
				}
				h.grant.accessExpires = time.Now().Add(leaseWindow)
				h.remote.mu.Unlock()
				block.Store(true)
				done := make(chan struct{})
				go func() {
					defer close(done)
					switch method {
					case "tool":
						_, _ = h.client.CallTool(h.ctx, &mcp.CallToolParams{Name: "gregale_get_app", Arguments: map[string]string{"app": "alpha"}})
					case "watch":
						_, _ = h.client.CallTool(h.ctx, watchParams(nil, 5))
					case "resource":
						_, _ = h.client.ReadResource(h.ctx, &mcp.ReadResourceParams{URI: "gregale://default/apps/alpha/health"})
					case "prompt":
						_, _ = h.client.GetPrompt(h.ctx, &mcp.GetPromptParams{Name: "gregale_deployment_review", Arguments: map[string]string{"app": "alpha"}})
					case "completion":
						_, _ = h.client.Complete(h.ctx, completionParams("app", "", nil))
					}
				}()
				select {
				case <-entered:
				case <-h.ctx.Done():
					t.Fatal("leased native read did not begin")
				}
				select {
				case <-canceled:
				case <-time.After(time.Second):
					t.Fatal("expiry did not cancel native read")
				}
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("expired RPC did not finish")
				}
				awaitAdmissionEmpty(t, h.manager.admission)
				// No second HTTP request is needed to trigger cleanup or grant pruning.
				h.remote.mu.Lock()
				revoked := h.grant.revoked
				h.remote.mu.Unlock()
				if revoked {
					t.Fatal("request expiry unnecessarily revoked refreshable grant")
				}
			})
		}
	}
}

func TestAuthorizationLeaseGrantAndCredentialExpiry(t *testing.T) {
	for _, kind := range []string{"grant", "credential"} {
		t.Run(kind, func(t *testing.T) {
			f := progressFixture()
			entered := make(chan struct{})
			canceled := make(chan struct{})
			var block atomic.Bool
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if block.Load() && r.URL.Path == "/v1/apps/alpha" {
					close(entered)
					<-r.Context().Done()
					close(canceled)
					return
				}
				f.serveHTTP(w, r)
			}))
			t.Cleanup(backend.Close)
			h := connectLeaseFixture(t, backend, Options{}, "2026-07-28")
			h.remote.mu.Lock()
			expiry := time.Now().Add(150 * time.Millisecond)
			if kind == "grant" {
				h.grant.expires = expiry
			} else {
				h.grant.credentialExpires = expiry
			}
			h.remote.mu.Unlock()
			block.Store(true)
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = h.client.CallTool(h.ctx, &mcp.CallToolParams{Name: "gregale_get_app", Arguments: map[string]string{"app": "alpha"}})
			}()
			select {
			case <-entered:
			case <-h.ctx.Done():
				t.Fatal("native read not started")
			}
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("earliest grant/credential expiry ignored")
			}
			<-done
			awaitAdmissionEmpty(t, h.manager.admission)
		})
	}
}

func TestAuthorizationLeaseRechecksAfterVerification(t *testing.T) {
	for _, kind := range []string{"expiry", "rotation", "revocation"} {
		t.Run(kind, func(t *testing.T) {
			f := progressFixture()
			entered := make(chan struct{})
			release := make(chan struct{})
			var block atomic.Bool
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if block.Load() && r.URL.Path == "/v1/account" {
					close(entered)
					<-release
				}
				f.serveHTTP(w, r)
			}))
			t.Cleanup(backend.Close)
			h := connectLeaseFixture(t, backend, Options{}, "2026-07-28")
			block.Store(true)
			var dispatched atomic.Bool
			handler := h.remote.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { dispatched.Store(true) }))
			request := httptest.NewRequest("POST", "/mcp", nil)
			request.Header.Set("Authorization", "Bearer "+h.token)
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); handler.ServeHTTP(response, request) }()
			<-entered
			h.remote.mu.Lock()
			switch kind {
			case "expiry":
				h.grant.credentialExpires = time.Now().Add(-time.Second)
			case "rotation":
				delete(h.remote.access, tokenHash(h.token))
				h.grant.accessExpires = time.Now().Add(time.Hour)
			case "revocation":
				h.grant.revoked = true
			}
			h.remote.mu.Unlock()
			close(release)
			<-done
			if dispatched.Load() || response.Code != http.StatusUnauthorized {
				t.Fatal("verification race admitted an invalid lease")
			}
		})
	}
}

func TestAuthorizationLeaseRefreshDoesNotExtendExistingRequest(t *testing.T) {
	f := progressFixture()
	var block atomic.Bool
	entered := make(chan struct{})
	canceled := make(chan struct{})
	var once sync.Once
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if block.Load() && r.URL.Path == "/v1/apps/alpha" {
			once.Do(func() { close(entered) })
			<-r.Context().Done()
			close(canceled)
			return
		}
		f.serveHTTP(w, r)
	}))
	t.Cleanup(backend.Close)
	h := connectLeaseFixture(t, backend, Options{}, "2026-07-28")
	h.remote.mu.Lock()
	h.grant.accessExpires = time.Now().Add(200 * time.Millisecond)
	h.remote.mu.Unlock()
	block.Store(true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = h.client.CallTool(h.ctx, &mcp.CallToolParams{Name: "gregale_get_app", Arguments: map[string]string{"app": "alpha"}})
	}()
	<-entered
	h.remote.mu.Lock()
	fresh, err := h.remote.mintLocked(h.grant)
	h.remote.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("refresh extended old in-flight lease")
	}
	<-done
	awaitAdmissionEmpty(t, h.manager.admission)
	block.Store(false)
	request := httptest.NewRequest("GET", "/mcp", nil)
	request.Header.Set("Authorization", "Bearer "+fresh.AccessToken)
	response := httptest.NewRecorder()
	h.remote.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatal("fresh token could not use the retained grant")
	}
}

func TestAuthorizationLeaseBoundsAccountVerification(t *testing.T) {
	f := progressFixture()
	var block atomic.Bool
	entered := make(chan struct{})
	canceled := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if block.Load() && r.URL.Path == "/v1/account" {
			close(entered)
			<-r.Context().Done()
			close(canceled)
			return
		}
		f.serveHTTP(w, r)
	}))
	t.Cleanup(backend.Close)
	h := connectLeaseFixture(t, backend, Options{}, "2026-07-28")
	h.remote.mu.Lock()
	h.grant.accessExpires = time.Now().Add(100 * time.Millisecond)
	h.remote.mu.Unlock()
	block.Store(true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = h.client.CallTool(h.ctx, &mcp.CallToolParams{Name: "gregale_get_app", Arguments: map[string]string{"app": "alpha"}})
	}()
	select {
	case <-entered:
	case <-h.ctx.Done():
		t.Fatal("account check did not start")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("account verification exceeded token lease")
	}
	<-done
	awaitAdmissionEmpty(t, h.manager.admission)
}

func TestAuthorizationLeaseMutationExpiryKeepsDurableRetryKey(t *testing.T) {
	// Keep real encrypted journal writes, but drive the lease with virtual time.
	// A short wall-clock lease can expire during fsync before the native write.
	synctest.Test(t, testAuthorizationLeaseMutationExpiryKeepsDurableRetryKey)
}

type leaseRoundTripper func(*http.Request) (*http.Response, error)

func (f leaseRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testAuthorizationLeaseMutationExpiryKeepsDurableRetryKey(t *testing.T) {
	store, path, key := testStateStore(t)
	f := progressFixture()
	f.keys = map[string]api.DeploymentResponse{}
	entered := make(chan struct{})
	canceled := make(chan struct{})
	var replay atomic.Bool
	native := api.NewClient("http://native.test", testToken)
	native.SetCompletionCache(nil)
	native.HTTPClient().Transport = leaseRoundTripper(func(r *http.Request) (*http.Response, error) {
		reply := httptest.NewRecorder()
		if r.Method == http.MethodPost && r.URL.Path == "/v1/apps/alpha/deployments" && !replay.Load() {
			f.serveHTTP(reply, r)
			close(entered)
			<-r.Context().Done()
			if !errors.Is(r.Context().Err(), context.DeadlineExceeded) {
				t.Error("native mutation was canceled without lease expiry")
			}
			close(canceled)
			return nil, r.Context().Err()
		}
		f.serveHTTP(reply, r)
		return reply.Result(), nil
	})
	var leaseUntil atomic.Int64
	leaseUntil.Store(time.Now().Add(time.Hour).UnixNano())
	opts := Options{APIBase: native.BaseURL(), AllowWrites: true, planStore: store, planNamespace: "authorization-lease-fixture"}
	connect := func() (*mcp.ClientSession, *management) {
		t.Helper()
		s, manager, err := newManagementServer(native, opts)
		if err != nil {
			t.Fatal(err)
		}
		// Supply the retained authorization deadline just as HTTP authentication
		// does; the production receiving middleware must enforce it on the RPC.
		s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				return next(context.WithValue(ctx, authorizationLeaseExpiresKey{}, time.Unix(0, leaseUntil.Load())), method, req)
			}
		})
		ct, st := mcp.NewInMemoryTransports()
		ss, err := s.Connect(t.Context(), st, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ss.Close() })
		client, err := mcp.NewClient(&mcp.Implementation{Name: "lease-retry-test", Version: "1"}, nil).Connect(t.Context(), ct, &mcp.ClientSessionOptions{ProtocolVersion: "2026-07-28"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		return client, manager
	}
	client, manager := connect()
	plan := callTool(t, t.Context(), client, "gregale_plan_deploy", map[string]any{"app": "alpha", "image": testImage, "reason": "lease retry test"}, false)
	id := planID(t, plan)
	leaseUntil.Store(time.Now().Add(150 * time.Millisecond).UnixNano())
	done := make(chan struct{})
	go func() {
		defer close(done)
		result, err := client.CallTool(t.Context(), &mcp.CallToolParams{Name: "gregale_apply_deploy", Arguments: map[string]string{"plan_id": id}})
		if err == nil && !result.IsError {
			t.Error("expired mutation unexpectedly succeeded")
		}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("native mutation was not accepted")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("mutation did not stop at expiry")
	}
	<-done
	awaitAdmissionEmpty(t, manager.admission)
	shutdown, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := manager.lifecycle.Shutdown(shutdown); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	replay.Store(true)
	leaseUntil.Store(time.Now().Add(time.Hour).UnixNano())
	opts.planStore = restored
	fresh, _ := connect()
	callTool(t, t.Context(), fresh, "gregale_apply_deploy", map[string]string{"plan_id": id}, false)
	f.mu.Lock()
	writes, keys := f.writes, len(f.keys)
	f.mu.Unlock()
	if writes != 1 || keys != 1 {
		t.Fatalf("expiry retry duplicated native mutation: writes=%d keys=%d", writes, keys)
	}
}
