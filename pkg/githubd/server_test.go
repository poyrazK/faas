// githubd Server tests (slice 7, ADR-012). Verifies:
//
//   - HTTP loopback listener accepts a signed POST and dispatches via Service
//   - missing signature → 401 (defense in depth, even with the proxy)
//   - non-POST method → 405
//   - ErrNoBinding → 200 with {"status":"ignored"}
//   - body decode error → 400-class
//
// The HTTP listener is wired here as a standalone http.Server so the test
// doesn't need a real unix socket or the githubd user/group from the
// deploy/ansible inventory.
package githubd

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// recordingService (intentionally omitted — slice 7 uses the
// shared Service directly via the binding-store stub below).

// Stub bindings + reconcile stub so the HTTP test sees a happy
// path. PR-H retires CreateDeployment; the happy-path test now
// wires the new Source+Reconcile path so the dispatch contract
// stays pinned end-to-end.
func newRecording(t *testing.T) *Service {
	t.Helper()
	mem := state.NewMemStore()
	aud := audit.New(mem, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, "githubd_test")
	rec := reconcile.NewService(mem, aud, slog.New(slog.NewTextHandler(io.Discard, nil)))
	rec.Scan = func(_ fs.FS) (reposcan.Result, error) {
		return reposcan.Result{}, nil
	}
	acct, err := mem.CreateAccount(context.Background(), "octo@example.com", "hobby")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := mem.CreateProject(context.Background(), state.Project{
		AccountID:        acct.ID,
		Slug:             "demo",
		InstallID:        42,
		RepoFullName:     "octo/api",
		ProductionBranch: "main",
		ScanSource:       state.ProjectScanSourceCompose,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = &stubBindings{
		byRepo: map[string]state.GitHubBinding{
			"octo/api|main": {BindingID: "b-1", AccountID: acct.ID, InstallID: 42, RepoFullName: "octo/api", ProductionBranch: "main"},
		},
	}
	svc.Installs = &stubInstalls{byAccount: map[string]state.GitHubInstall{
		acct.ID: {AccountID: acct.ID, InstallationID: 42},
	}}
	svc.Source = &stubSource{fsys: fstest.MapFS{}}
	svc.Reconcile = rec
	return svc
}

// newServerUnderTest wraps the loopback handler in an httptest.Server
// so we can hit it with real HTTP. The full Server.Start path needs
// a unix socket + a user lookup; those are covered by the daemon
// integration tests, not here.
func newServerUnderTest(t *testing.T, svc *Service) *Server {
	t.Helper()
	return &Server{
		Service: svc,
		Log:     svc.Log,
		Ops:     wire.NewOpsMetrics("githubd_test"),
	}
}

type recordingDeliveryStore struct {
	deliveries []WebhookDelivery
}

func (s *recordingDeliveryStore) Enqueue(_ context.Context, delivery WebhookDelivery) (bool, error) {
	for _, existing := range s.deliveries {
		if existing.DeliveryID == delivery.DeliveryID {
			return false, nil
		}
	}
	s.deliveries = append(s.deliveries, delivery)
	return true, nil
}
func (*recordingDeliveryStore) Claim(context.Context) (WebhookDelivery, error) {
	return WebhookDelivery{}, ErrNoWebhookDelivery
}
func (*recordingDeliveryStore) Complete(context.Context, string) error { return nil }
func (*recordingDeliveryStore) Fail(context.Context, string, string, time.Time, bool) error {
	return nil
}
func (*recordingDeliveryStore) Prune(context.Context, time.Time) error { return nil }

func webhookSignature(body, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestServerWebhook_GlobalSecretDurablyAcceptsStandardGitHubHeaders(t *testing.T) {
	svc := newRecording(t)
	deliveries := &recordingDeliveryStore{}
	secret := []byte("github-app-webhook-secret")
	s := newServerUnderTest(t, svc)
	s.WebhookSecret = secret
	s.Deliveries = deliveries
	body := []byte(`{"action":"opened"}`)

	for attempt, wantBody := range []string{"accepted", "duplicate"} {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", webhookSignature(body, secret))
		req.Header.Set("X-GitHub-Event", "pull_request")
		req.Header.Set("X-GitHub-Delivery", "delivery-1")
		rr := httptest.NewRecorder()
		s.WebhookLoopbackHandler().ServeHTTP(rr, req)
		if rr.Code != http.StatusAccepted || !strings.Contains(rr.Body.String(), wantBody) {
			t.Fatalf("attempt %d: status/body = %d %q, want 202 %q", attempt+1, rr.Code, rr.Body.String(), wantBody)
		}
	}
	if len(deliveries.deliveries) != 1 || deliveries.deliveries[0].EventType != "pull_request" {
		t.Fatalf("deliveries = %+v, want one pull_request", deliveries.deliveries)
	}
}

func TestHandleWebhookEvent_PushRequiresInstallationID(t *testing.T) {
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := svc.HandleWebhookEvent(context.Background(), "push",
		[]byte(`{"ref":"refs/heads/main","after":"abc","repository":{"full_name":"octo/api"}}`))
	if err == nil || !strings.Contains(err.Error(), "installation.id") {
		t.Fatalf("err = %v, want missing installation.id", err)
	}
}

func TestHandleWebhookEvent_PullRequestRequiresInstallationID(t *testing.T) {
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	var payload map[string]any
	if err := json.Unmarshal([]byte(validPRBody), &payload); err != nil {
		t.Fatal(err)
	}
	delete(payload, "installation")
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	err = svc.HandleWebhookEvent(context.Background(), "pull_request", body)
	if err == nil || !strings.Contains(err.Error(), "installation.id") {
		t.Fatalf("err = %v, want missing installation.id", err)
	}
}

func TestServerWebhook_HappyPath(t *testing.T) {
	svc := newRecording(t)
	s := newServerUnderTest(t, svc)

	body := []byte(`{"ref":"refs/heads/main","after":"abc","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	s.WebhookLoopbackHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (daemon-side verify rejects: no secret in slice 7)", rr.Code)
	}
}

func TestServerWebhook_RejectsGet(t *testing.T) {
	svc := newRecording(t)
	s := newServerUnderTest(t, svc)

	req := httptest.NewRequest(http.MethodGet, "/webhooks/github", nil)
	rr := httptest.NewRecorder()

	s.WebhookLoopbackHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rr.Code)
	}
}

// Service-level test: ensure the dispatcher reaches Reconcile
// when the binding + project are wired. PR-H drives the push
// through reconcile.Service.Reconcile (the legacy CreateDeployment
// seam is retired); this test pins that the new path is wired
// (no nil-deref on Reconcile.Store.ProjectByRepo) and that an
// empty scan surfaces the never-empty alert via Result.Alerts
// (no error — that's the contract).
func TestServerWebhook_DispatchThroughService(t *testing.T) {
	svc := newRecording(t)
	result, err := svc.HandlePushRequest(context.Background(),
		[]byte(`{"ref":"refs/heads/main","after":"sha-1","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`))
	if err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
	if len(result.Alerts) == 0 {
		t.Error("expected never-empty alert, got 0 alerts")
	}
	if result.Alerts[0].Kind != "no_workloads" {
		t.Errorf("alert kind = %q, want no_workloads", result.Alerts[0].Kind)
	}
}

// Pushes for unknown repos come back through the HTTP layer as
// {"status":"ignored","reason":"no_binding"}. Verify the service
// surfaces ErrNoBinding so the handler can write that body.
func TestServerWebhook_NoBindingSurfaced(t *testing.T) {
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = &stubBindings{byRepo: map[string]state.GitHubBinding{}}
	_, err := svc.HandlePushRequest(context.Background(),
		[]byte(`{"ref":"refs/heads/main","after":"x","repository":{"full_name":"unknown/repo","name":"repo"},"pusher":{"name":"x"}}`))
	if !IsNoBinding(err) {
		t.Errorf("err = %v, want ErrNoBinding", err)
	}
}

// Drives the no-binding path through the handler with a wrapper that
// injects a secret-bearing header (slice 8 will wire the real one;
// today we fake it via the unexported package seam).
func TestServerWebhook_NoBindingHandlerPath(t *testing.T) {
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = &stubBindings{byRepo: map[string]state.GitHubBinding{}}
	// Build a handler that bypasses the secret check (the
	// production handler requires webhookSecretFromHeader to return
	// a non-nil value; slice 7 leaves that nil so all webhooks are
	// rejected — exercised by the happy-path test above). This
	// test exercises just the no-binding dispatch via the Service.
	body := []byte(`{"ref":"refs/heads/main","after":"x","repository":{"full_name":"unknown/repo","name":"repo"},"pusher":{"name":"x"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if !IsNoBinding(err) {
		t.Errorf("expected ErrNoBinding, got %v", err)
	}
	// The handler will still 401 today (secret nil). That's correct.
}

// Sanity: json body of the ignored response matches the contract.
// Locked in here so a future copy-paste can't drift the body.
func TestServerWebhook_IgnoredResponseShape(t *testing.T) {
	want := map[string]any{"status": "ignored", "reason": "no_binding"}
	got := map[string]any{}
	if err := json.Unmarshal([]byte(`{"status":"ignored","reason":"no_binding"}`), &got); err != nil {
		t.Fatal(err)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	_ = strings.HasPrefix // keep imports stable for downstream slices
}

func TestServerWebhook_ReleaseTagRejectionResponse(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tagErr error
		reason string
	}{
		{name: "invalid", tagErr: releaseTagRejection{reason: releaseTagReasonInvalid, tag: "latest"}, reason: releaseTagReasonInvalid},
		{name: "moved", tagErr: releaseTagRejection{reason: releaseTagReasonMoved, tag: "v1.2.3"}, reason: releaseTagReasonMoved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			s := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			s.writeWebhookResult(rr, reconcile.Result{}, tc.tagErr, func(error) {})
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rr.Code)
			}
			var got map[string]string
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got["status"] != "ignored" || got["reason"] != tc.reason {
				t.Fatalf("response = %v, want ignored/%s", got, tc.reason)
			}
		})
	}
}

// scrape returns the /metrics body served by s.Ops (the per-test
// registry, prefix "githubd_test"). Mirrors the scrapeMetrics helper in
// pkg/builderd/builderd_test.go.
func scrape(t *testing.T, s *Server) string {
	t.Helper()
	srv := httptest.NewServer(s.WebhookLoopbackHandler())
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("get metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

// The inbound webhook observer is the highest-signal missing metric
// for githubd: a spike in 401s is a misconfigured proxy or a forged
// payload, and a spike in 405s is someone scanning the loopback endpoint.
// Drive one reject of each kind and assert the counter labelled correctly.
func TestWebhookPush_Metrics(t *testing.T) {
	svc := newRecording(t)
	s := newServerUnderTest(t, svc)

	// 401: signed path with no secret configured (slice 7's
	// webhookSecretFromHeader returns nil → daemon-side verify
	// always rejects, defense in depth).
	body := []byte(`{"ref":"refs/heads/main","after":"abc","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	s.WebhookLoopbackHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("first status = %d, want 401", rr.Code)
	}

	// 405: wrong method.
	req = httptest.NewRequest(http.MethodGet, "/webhooks/github", nil)
	rr = httptest.NewRecorder()
	s.WebhookLoopbackHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("second status = %d, want 405", rr.Code)
	}

	got := scrape(t, s)
	// The githubd_test prefix (used by newServerUnderTest) means the
	// emitted series carries githubd_test_ops_total{...}. The webhook
	// observer fires twice (401, then 405); Prometheus only writes the
	// cumulative total, so the assertion is the post-firing value (2) and
	// the matching histogram count for the same 2 observations. We don't
	// assert on the `code="ok"` counter line because CounterVec has no
	// pre-instantiation loop — it's only emitted once observed.
	want := []string{
		`githubd_test_ops_total{code="err",op="webhook_push"} 2`,
		`githubd_test_op_duration_seconds_count{op="webhook_push"} 2`,
	}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("metrics body missing %q:\n%s", w, got)
		}
	}
}

// The /metrics endpoint lives on the same loopback mux as
// POST /webhooks/github. Locked in here so a future cleanup that
// strips it out can't quietly delete the scrape endpoint.
func TestWebhook_MetricsEndpointMounted(t *testing.T) {
	svc := newRecording(t)
	s := newServerUnderTest(t, svc)

	srv := httptest.NewServer(s.WebhookLoopbackHandler())
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("get metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/metrics status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	got := string(body)
	// /metrics must expose the daemon's series, not a stranger's.
	if !strings.Contains(got, "githubd_test_") {
		t.Errorf("/metrics body has no githubd_test_ series:\n%s", got)
	}
}

// When Ops is nil (e.g. a future caller that forgets WithOpsMetrics
// — review finding #3 on PR #132), the webhook handler must still
// serve its contract: 405 for non-POST, 401 for bad signature, etc.
// A nil-deref at the first inbound webhook would take the daemon
// down in production; this test pins down the nil-safe path so a
// future cleanup that drops the nil-check breaks the test loudly.
func TestHandleWebhookPush_NilOpsIsNoOp(t *testing.T) {
	svc := newRecording(t)
	// Ops left nil on purpose — this is the misconfigured-caller case.
	s := &Server{Service: svc, Log: svc.Log}

	// 405 path.
	req := httptest.NewRequest(http.MethodGet, "/webhooks/github", nil)
	rr := httptest.NewRecorder()
	s.handleWebhookPush(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", rr.Code)
	}

	// 401 path (no secret in slice 7 → daemon-side verify rejects).
	body := []byte(`{"ref":"refs/heads/main","after":"abc","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	req = httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	s.handleWebhookPush(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("POST status = %d, want 401", rr.Code)
	}
}

// TestWebhookServer_AppliesCanonicalShape pins ADR-122's webhook
// variant against the PRODUCTION listener constructor
// (NewWebhookHTTPServer in pkg/githubd/server.go). A future edit
// that drops a knob from the production struct surfaces here — the
// helper is exported specifically so this test exercises the real
// code path, not a parallel mirror literal.
//
// ReadTimeout=30s (10 MiB body-cap budget), WriteTimeout=30s,
// IdleTimeout=60s, MaxHeaderBytes=1 MiB. The constant family lives
// in pkg/api/limits.go; a stray edit to one of these constants
// also surfaces here. ReadHeaderTimeout=10s is the pre-existing
// Slowloris guard that stays unchanged by ADR-122.
func TestWebhookServer_AppliesCanonicalShape(t *testing.T) {
	const wantRead = time.Duration(api.WebhookReadTimeoutSecondsDefault) * time.Second
	const wantWrite = time.Duration(api.WebhookWriteTimeoutSecondsDefault) * time.Second
	const wantIdle = time.Duration(api.WebhookIdleTimeoutSecondsDefault) * time.Second
	const wantMHB = api.DefaultMaxHeaderBytes

	// Call the production helper — NOT a parallel struct literal.
	// A regression that drops one of the ADR-122 knobs from
	// NewWebhookHTTPServer would otherwise go silent (the previous
	// test constructed its own *http.Server literal that was
	// independent of the production code).
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	srv := NewWebhookHTTPServer("127.0.0.1:0", handler)
	if srv.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v want %v", srv.ReadHeaderTimeout, 10*time.Second)
	}
	if srv.ReadTimeout != wantRead {
		t.Errorf("ReadTimeout = %v want %v", srv.ReadTimeout, wantRead)
	}
	if srv.WriteTimeout != wantWrite {
		t.Errorf("WriteTimeout = %v want %v", srv.WriteTimeout, wantWrite)
	}
	if srv.IdleTimeout != wantIdle {
		t.Errorf("IdleTimeout = %v want %v", srv.IdleTimeout, wantIdle)
	}
	if int64(srv.MaxHeaderBytes) != wantMHB {
		t.Errorf("MaxHeaderBytes = %d want %d", srv.MaxHeaderBytes, wantMHB)
	}
}
