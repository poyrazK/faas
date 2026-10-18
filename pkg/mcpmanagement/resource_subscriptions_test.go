// adr: 1254
package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

type subscriptionHarness struct {
	serverSession *mcp.ServerSession
	client        *mcp.ClientSession
	manager       *management
	ctx           context.Context
	updates       chan *mcp.ResourceUpdatedNotificationParams
	acks          chan *mcp.SubscriptionsAcknowledgedParams
}

func connectSubscriptionHarness(t *testing.T, b *httptest.Server, opts Options, version string, httpTransport bool) *subscriptionHarness {
	t.Helper()
	s, m, err := newManagementServer(api.NewClient(b.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	m.subscriptions.poll = 20 * time.Millisecond
	m.subscriptions.ttl = 2 * time.Second
	h := &subscriptionHarness{manager: m, updates: make(chan *mcp.ResourceUpdatedNotificationParams, 32), acks: make(chan *mcp.SubscriptionsAcknowledgedParams, 32)}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	h.ctx = ctx
	t.Cleanup(cancel)
	var transport mcp.Transport
	if httpTransport {
		endpoint := httptest.NewServer(newWatchHTTPHandler(func(*http.Request) *mcp.Server { return s }))
		t.Cleanup(endpoint.Close)
		transport = &mcp.StreamableClientTransport{Endpoint: endpoint.URL}
	} else {
		ct, st := mcp.NewInMemoryTransports()
		ss, err := s.Connect(ctx, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ss.Close() })
		h.serverSession = ss
		transport = ct
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "subscription-test", Version: "1"}, &mcp.ClientOptions{ResourceUpdatedHandler: func(_ context.Context, req *mcp.ResourceUpdatedNotificationRequest) { h.updates <- req.Params }})
	c.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "notifications/subscriptions/acknowledged" {
				h.acks <- req.GetParams().(*mcp.SubscriptionsAcknowledgedParams)
			}
			return next(ctx, method, req)
		}
	})
	h.client, err = c.Connect(ctx, transport, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.client.Close() })
	return h
}
func (h *subscriptionHarness) subscribe(t *testing.T, uri string) {
	t.Helper()
	if err := h.client.Subscribe(h.ctx, &mcp.SubscribeParams{URI: uri}); err != nil {
		t.Fatal(err)
	}
	select {
	case ack := <-h.acks:
		if len(ack.Notifications.ResourceSubscriptions) != 1 || ack.Notifications.ResourceSubscriptions[0] != uri || ack.Meta["gregale_subscription_expires_at"] == nil {
			t.Fatalf("ack=%+v", ack)
		}
	case <-h.ctx.Done():
		t.Fatal("subscription was not acknowledged")
	}
}
func activeResourceGroups(m *management) int {
	m.subscriptions.mu.Lock()
	defer m.subscriptions.mu.Unlock()
	return len(m.subscriptions.groups)
}
func awaitResourceGroups(t *testing.T, h *subscriptionHarness, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if activeResourceGroups(h.manager) == want {
			return
		}
		select {
		case <-time.After(5 * time.Millisecond):
		case <-h.ctx.Done():
			t.Fatal("subscription did not clean up")
		}
	}
	t.Fatalf("active groups=%d want=%d", activeResourceGroups(h.manager), want)
}

func TestResourceSubscriptionsChangeAndUnsubscribe(t *testing.T) {
	for _, httpTransport := range []bool{false, true} {
		name := "session"
		if httpTransport {
			name = "http"
		}
		t.Run(name, func(t *testing.T) {
			f := progressFixture()
			b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			t.Cleanup(b.Close)
			h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL}, "2026-07-28", httpTransport)
			uri := "gregale://default/apps/alpha/health"
			h.subscribe(t, uri)
			f.mu.Lock()
			f.current = "another"
			f.mu.Unlock()
			select {
			case update := <-h.updates:
				if update.URI != uri || update.Meta[mcp.MetaKeySubscriptionID] == nil {
					t.Fatalf("update=%+v", update)
				}
			case <-h.ctx.Done():
				t.Fatal("resource change was not delivered")
			}
			select {
			case <-h.updates:
				t.Fatal("duplicate invalidation")
			case <-time.After(80 * time.Millisecond):
			}
			if err := h.client.Unsubscribe(h.ctx, &mcp.UnsubscribeParams{URI: uri}); err != nil {
				t.Fatal(err)
			}
			awaitResourceGroups(t, h, 0)
			if f.writes != 0 {
				t.Fatal("subscription changed customer intent")
			}
		})
	}
}

func TestResourceSubscriptionsExpiryDisconnectAndRevocation(t *testing.T) {
	for _, why := range []string{"expiry", "disconnect", "revocation"} {
		t.Run(why, func(t *testing.T) {
			f := progressFixture()
			b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
			t.Cleanup(b.Close)
			h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL}, "2026-07-28", true)
			h.manager.subscriptions.ttl = 100 * time.Millisecond
			h.subscribe(t, "gregale://default/apps/alpha/health")
			if why == "disconnect" {
				_ = h.client.Close()
			}
			if why == "revocation" {
				h.manager.stopResourceSubscriptions()
			}
			awaitResourceGroups(t, h, 0)
			select {
			case <-h.updates:
				t.Fatal("cleanup emitted resource data")
			default:
			}
		})
	}
}

func TestResourceSubscriptionsRecreatedEnvironmentAndApp(t *testing.T) {
	t.Run("environment", func(t *testing.T) {
		f, b, opts := namedStageOptions(t)
		h := connectSubscriptionHarness(t, b, opts, "2026-07-28", false)
		h.subscribe(t, "gregale://projects/shop/environments/staging/apps/alpha/health")
		f.mu.Lock()
		f.envID = "recreated"
		f.mu.Unlock()
		awaitResourceGroups(t, h, 0)
		select {
		case <-h.updates:
			t.Fatal("recreated environment inherited notifications")
		default:
		}
	})
	t.Run("app", func(t *testing.T) {
		f := progressFixture()
		b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
		t.Cleanup(b.Close)
		h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL}, "2026-07-28", false)
		h.subscribe(t, "gregale://default/apps/alpha/health")
		f.mu.Lock()
		f.app.ID = "recreated"
		f.mu.Unlock()
		awaitResourceGroups(t, h, 0)
		select {
		case <-h.updates:
			t.Fatal("recreated app inherited notifications")
		default:
		}
	})
}

func TestResourceSubscriptionsLegacyAndURIRestrictions(t *testing.T) {
	f := progressFixture()
	b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(b.Close)
	h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL}, "2025-11-25", false)
	caps := h.client.InitializeResult().Capabilities
	if caps.Resources.Subscribe {
		t.Fatal("legacy subscription support was incorrectly advertised")
	}
	if err := h.client.Subscribe(h.ctx, &mcp.SubscribeParams{URI: "gregale://default/apps/alpha/health"}); err == nil {
		t.Fatal("legacy subscription accepted")
	}
	if _, err := h.client.ReadResource(h.ctx, &mcp.ReadResourceParams{URI: "gregale://default/apps/alpha/health"}); err != nil {
		t.Fatal("legacy reads regressed")
	}
	for _, uri := range []string{"gregale://context", "gregale://default/inventory", "https://example.com/private", "gregale://default/apps/alpha/health?secret=value"} {
		_, view, err := parseContextURI(uri)
		if err == nil && view != "context" && view != "inventory" {
			t.Fatal("arbitrary subscription URI accepted")
		}
	}
}

func TestResourceSemanticDigestIgnoresClocksButTracksFreshnessAndState(t *testing.T) {
	now := time.Now().UTC()
	a := api.AppHealthResponse{AppID: "owned", Scope: "default", Status: "healthy", EvaluatedAt: now.Add(-time.Second).Format(time.RFC3339Nano), ValidForSeconds: 30}
	data, _ := json.Marshal(a)
	before, err := resourceSemanticDigest(data, now)
	if err != nil {
		t.Fatal(err)
	}
	a.EvaluatedAt = now.Format(time.RFC3339Nano)
	data, _ = json.Marshal(a)
	clockOnly, _ := resourceSemanticDigest(data, now)
	if before != clockOnly {
		t.Fatal("clock-only observation invalidated resource")
	}
	expired, _ := resourceSemanticDigest(data, now.Add(time.Minute))
	if expired == clockOnly {
		t.Fatal("freshness expiry was hidden")
	}
	a.Status = "unhealthy"
	data, _ = json.Marshal(a)
	changed, _ := resourceSemanticDigest(data, now)
	if changed == clockOnly {
		t.Fatal("health transition was hidden")
	}
	aRaw := json.RawMessage(`{"checked_at":"one","report":{"routes":[{"windows":[{"start":"one","end":"two","requests":20}]}]}}`)
	bRaw := json.RawMessage(strings.ReplaceAll(string(aRaw), "one", "three"))
	left, _ := resourceSemanticDigest(aRaw, now)
	right, _ := resourceSemanticDigest(bRaw, now)
	if left != right {
		t.Fatal("moving window clock caused heartbeat")
	}
}

func TestResourceSubscriptionNoPollingWithoutExplicitSubscribe(t *testing.T) {
	f := progressFixture()
	var calls atomic.Int32
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); f.serveHTTP(w, r) }))
	t.Cleanup(b.Close)
	h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL}, "2026-07-28", false)
	select {
	case <-time.After(80 * time.Millisecond):
	case <-h.ctx.Done():
		t.Fatal("connection ended")
	}
	if calls.Load() != 0 || activeResourceGroups(h.manager) != 0 {
		t.Fatal("connection/discovery began implicit API polling")
	}
}

func listenResourceRequest(h *subscriptionHarness, uris []string) (mcp.Result, error) {
	params := &mcp.SubscriptionsListenParams{Notifications: &mcp.NotificationSubscriptions{ResourceSubscriptions: uris}}
	req := &mcp.ServerRequest[*mcp.SubscriptionsListenParams]{Session: h.serverSession, Params: params}
	return h.manager.listenResources(h.ctx, func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.SubscriptionsListenResult{}, nil
	}, "subscriptions/listen", req)
}

func TestResourceSubscriptionLimitsAndRejectedSelectors(t *testing.T) {
	f := progressFixture()
	b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(b.Close)
	h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL}, "2026-07-28", false)
	uri := "gregale://default/apps/alpha/health"
	for _, uris := range [][]string{{uri, uri}, {"gregale://context"}, {"gregale://default/inventory"}, {"https://example.com/private"}, {uri, uri, uri, uri, uri}} {
		if _, err := listenResourceRequest(h, uris); err == nil {
			t.Fatal("invalid subscription request admitted")
		}
	}
	manager := h.manager.subscriptions
	manager.mu.Lock()
	for range maxResourceListenStreams {
		group := &resourceListenGroup{session: new(mcp.ServerSession), uris: []string{uri}}
		manager.groups[group] = struct{}{}
	}
	manager.mu.Unlock()
	if _, err := listenResourceRequest(h, []string{uri}); err == nil {
		t.Fatal("listen stream cap ignored")
	}
	manager.mu.Lock()
	manager.groups = map[*resourceListenGroup]struct{}{}
	for range maxResourceSubscriptions / maxListenResources {
		group := &resourceListenGroup{session: new(mcp.ServerSession), uris: []string{uri, uri, uri, uri}}
		manager.groups[group] = struct{}{}
	}
	manager.mu.Unlock()
	if _, err := listenResourceRequest(h, []string{uri}); err == nil {
		t.Fatal("resource slot cap ignored")
	}
	manager.mu.Lock()
	manager.groups = map[*resourceListenGroup]struct{}{}
	manager.mu.Unlock()
}

func TestResourceSubscriptionConsentCheckedBeforeAckAndEverySend(t *testing.T) {
	f, b, opts := namedStageOptions(t)
	h := connectSubscriptionHarness(t, b, opts, "2026-07-28", false)
	uri := "gregale://projects/shop/environments/production/apps/alpha/health"
	args, view, err := parseContextURI(uri)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.manager.subscriptionSnapshot(h.ctx, args, view, nil); err == nil {
		t.Fatal("unconsented environment was inspected")
	}
	h.subscribe(t, "gregale://projects/shop/environments/staging/apps/alpha/health")
	// The recipient's identity must be rechecked even if a publisher invokes
	// ResourceUpdated with an old URI while its former grant remains registered.
	f.mu.Lock()
	f.envID = "recreated"
	f.mu.Unlock()
	if err := h.manager.resourceServer.ResourceUpdated(h.ctx, &mcp.ResourceUpdatedNotificationParams{URI: "gregale://projects/shop/environments/staging/apps/alpha/health"}); err != nil {
		t.Fatal(err)
	}
	awaitResourceGroups(t, h, 0)
	select {
	case <-h.updates:
		t.Fatal("unchecked recipient received invalidation")
	default:
	}
}

func TestResourceSubscriptionCancelsInFlightReadOnDisconnect(t *testing.T) {
	f := progressFixture()
	entered, cancelled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps/alpha/health" && calls.Add(1) == 2 {
			close(entered)
			<-r.Context().Done()
			close(cancelled)
			return
		}
		f.serveHTTP(w, r)
	}))
	t.Cleanup(b.Close)
	h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL}, "2026-07-28", true)
	h.subscribe(t, "gregale://default/apps/alpha/health")
	select {
	case <-entered:
	case <-h.ctx.Done():
		t.Fatal("poll read did not start")
	}
	if err := h.client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("disconnect left API read running")
	}
	awaitResourceGroups(t, h, 0)
	if f.writes != 0 {
		t.Fatal("subscription shutdown mutated intent")
	}
}

func TestResourceSubscriptionCrossSessionChangesCoalesce(t *testing.T) {
	f := progressFixture()
	b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(b.Close)
	s, m, err := newManagementServer(api.NewClient(b.URL, testToken), Options{APIBase: b.URL})
	if err != nil {
		t.Fatal(err)
	}
	m.subscriptions.poll = 20 * time.Millisecond
	m.subscriptions.ttl = time.Second
	endpoint := httptest.NewServer(newWatchHTTPHandler(func(*http.Request) *mcp.Server { return s }))
	t.Cleanup(endpoint.Close)
	clients := make([]*mcp.ClientSession, 0, 2)
	acks := make(chan struct{}, 2)
	updates := make(chan *mcp.ResourceUpdatedNotificationParams, 8)
	for range 2 {
		c := mcp.NewClient(&mcp.Implementation{Name: "peer", Version: "1"}, &mcp.ClientOptions{ResourceUpdatedHandler: func(_ context.Context, r *mcp.ResourceUpdatedNotificationRequest) { updates <- r.Params }})
		c.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, method string, r mcp.Request) (mcp.Result, error) {
				if method == "notifications/subscriptions/acknowledged" {
					acks <- struct{}{}
				}
				return next(ctx, method, r)
			}
		})
		cs, err := c.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: endpoint.URL}, nil)
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, cs)
		t.Cleanup(func() { _ = cs.Close() })
	}
	uri := "gregale://default/apps/alpha/health"
	for _, cs := range clients {
		if err := cs.Subscribe(t.Context(), &mcp.SubscribeParams{URI: uri}); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		select {
		case <-acks:
		case <-time.After(time.Second):
			t.Fatal("ack missing")
		}
	}
	f.mu.Lock()
	f.current = "next"
	f.mu.Unlock()
	for range 2 {
		select {
		case update := <-updates:
			if update.URI != uri {
				t.Fatal("notification crossed URI")
			}
		case <-time.After(time.Second):
			t.Fatal("peer invalidation missing")
		}
	}
	select {
	case <-updates:
		t.Fatal("duplicate publication across polling groups")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestResourceSubscriptionRemoteGrantRevocationAndLeaseExpiry(t *testing.T) {
	for _, test := range []struct{ why, uri string }{
		{"revocation", "gregale://default/apps/alpha/health"},
		{"access_expiry", "gregale://default/apps/alpha/health"},
		{"revocation", regressionResourceURI},
		{"access_expiry", regressionResourceURI},
	} {
		t.Run(test.why+"/"+test.uri, func(t *testing.T) {
			f := newRemoteFixture(t, false)
			f.mu.Lock()
			f.apiHook = func(w http.ResponseWriter, req *http.Request, account string) bool {
				if req.URL.Path == "/v1/apps/alpha/debug/regressions" {
					_ = json.NewEncoder(w).Encode(regressionsFixture())
					return true
				}
				if req.URL.Path == "/v1/deployments/"+discoveryDeploymentID || req.URL.Path == "/v1/deployments/"+comparisonTargetID {
					_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: strings.TrimPrefix(req.URL.Path, "/v1/deployments/"), AppID: account + "-app", Scope: "staging"})
					return true
				}
				return false
			}
			f.mu.Unlock()
			native := api.NewClient(f.opts.APIBase, "fp_test_alice_remote_key")
			s, m, err := newManagementServer(native, Options{APIBase: f.opts.APIBase, accountID: "alice", grantID: "subscription-fixture", AllowedApps: map[string]string{"alpha": "alice-app"}, Environments: &EnvironmentConsent{RequestReadApps: map[string]string{"alpha": "alice-app"}}})
			if err != nil {
				t.Fatal(err)
			}
			m.subscriptions.poll = 20 * time.Millisecond
			m.subscriptions.ttl = 2 * time.Second
			origin, err := url.Parse(f.server.URL)
			if err != nil {
				t.Fatal(err)
			}
			token := "gmcp_at_subscription_fixture"
			expiry := time.Now().Add(time.Minute)
			if test.why == "access_expiry" {
				expiry = time.Now().Add(500 * time.Millisecond)
			}
			g := &oauthGrant{id: "subscription-fixture", accountID: "alice", server: s, management: m, client: native, accessExpires: expiry, expires: time.Now().Add(time.Hour), accessHash: tokenHash(token)}
			remoteOptions := f.opts
			remoteOptions.Timeout = api.DefaultClientTimeout
			r := &remoteServer{opts: remoteOptions, host: origin.Host, origin: f.server.URL, access: map[string]*oauthGrant{tokenHash(token): g}, grants: map[string]*oauthGrant{g.id: g}}
			f.setHandler(r.handler())
			httpClient := *f.client
			httpClient.Transport = bearerTransport{base: f.client.Transport, token: token}
			acks := make(chan *mcp.SubscriptionsAcknowledgedParams, 1)
			c := mcp.NewClient(&mcp.Implementation{Name: "leased-subscription", Version: "1"}, nil)
			c.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
				return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
					if method == "notifications/subscriptions/acknowledged" {
						acks <- req.GetParams().(*mcp.SubscriptionsAcknowledgedParams)
					}
					return next(ctx, method, req)
				}
			})
			cs, err := c.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: f.resource, HTTPClient: &httpClient}, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = cs.Close() })
			if err := cs.Subscribe(t.Context(), &mcp.SubscribeParams{URI: test.uri}); err != nil {
				t.Fatal(err)
			}
			select {
			case ack := <-acks:
				value, _ := ack.Meta["gregale_subscription_expires_at"].(string)
				until, err := time.Parse(time.RFC3339Nano, value)
				if err != nil || until.After(expiry) {
					t.Fatal("listen outlived initiating access token")
				}
			case <-time.After(time.Second):
				t.Fatal("authenticated listen was not acknowledged")
			}
			if test.why == "revocation" {
				r.mu.Lock()
				r.revokeLocked(g)
				r.mu.Unlock()
			}
			h := &subscriptionHarness{manager: m, ctx: t.Context()}
			awaitResourceGroups(t, h, 0)
			if test.why == "revocation" {
				m.subscriptions.mu.Lock()
				stopped := m.subscriptions.stopped
				m.subscriptions.mu.Unlock()
				if !stopped {
					t.Fatal("revoked grant could admit a replacement listen")
				}
			}
		})
	}
}

func TestResourceSubscriptionsNamedReleaseHealthAndBudgets(t *testing.T) {
	f, _, opts := namedStageOptions(t)
	const appID = "11111111-1111-4111-8111-111111111111"
	if opts.AllowedApps != nil {
		opts.AllowedApps["alpha"] = appID
	}
	opts.Environments.Named["shop/staging"].Apps["alpha"] = appID
	var budgetRevision atomic.Int64
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/alpha/operational-summary" {
			record := httptest.NewRecorder()
			f.serve(record, r)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(record.Code)
			_, _ = w.Write([]byte(strings.ReplaceAll(record.Body.String(), "app-one", appID)))
			return
		}
		f.mu.Lock()
		env := f.envID
		f.mu.Unlock()
		report := routemonitor.NewReport(api.RouteMonitorConfig{AppID: appID, Revision: budgetRevision.Load(), Routes: []api.RouteMonitorRoute{}}, time.Now())
		routemonitor.Evaluate(&report, "")
		out := api.AppOperationalSummary{Version: 1, AppID: appID, Scope: "staging", ProjectID: "project-one", EnvironmentID: env, Monitoring: api.AppOperationalMonitoring{Available: true, Status: report.Status, Reason: report.Reason, Coverage: "deployment_attributed_current_windows", CheckedAt: &report.CheckedAt, RouteBudgets: &api.ScopedRouteBudgetReport{Scope: "staging", ProjectID: "project-one", EnvironmentID: env, BudgetSource: "app_policy", AppBudgetRevision: report.Revision, Report: report}}, Recovery: api.AppOperationalRecovery{Coverage: "explicit_scope_only", Rollbacks: []api.AppOperationalRollback{}, Restarts: []api.RuntimeConfigRestartStatusResponse{}}}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(b.Close)
	opts.APIBase = b.URL
	h := connectSubscriptionHarness(t, b, opts, "2026-07-28", true)
	prefix := "gregale://projects/shop/environments/staging/apps/alpha/"
	for _, view := range []string{"release", "health", "budgets"} {
		args, parsedView, err := parseContextURI(prefix + view)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := h.manager.subscriptionSnapshot(h.ctx, args, parsedView, nil); err != nil {
			t.Fatalf("%s baseline: %v", view, err)
		}
		h.subscribe(t, prefix+view)
	}
	f.mu.Lock()
	f.hash = strings.Repeat("f", 64)
	f.current = "next"
	f.mu.Unlock()
	budgetRevision.Store(1)
	seen := map[string]bool{}
	for range 3 {
		select {
		case update := <-h.updates:
			seen[update.URI] = true
		case <-h.ctx.Done():
			t.Fatal("named resource did not invalidate")
		}
	}
	for _, view := range []string{"release", "health", "budgets"} {
		if !seen[prefix+view] {
			t.Fatal("notification missed selected named view")
		}
	}
	select {
	case <-h.updates:
		t.Fatal("clock-only budget report produced heartbeat")
	case <-time.After(80 * time.Millisecond):
	}
}
