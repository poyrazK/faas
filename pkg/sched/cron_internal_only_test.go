package sched

// cron_internal_only_test.go — ADR-119 tests for the
// cron-fired Authorization: Bearer JWT attachment path. The
// production code in loop.go::httpGatewaySynth.SynthesizeRequest
// consults the per-app public_auth_mode and attaches a minted
// JWT when the mode is 'internal_only'. These tests stand up
// a real HTTP test server (no unix socket) + an
// httpGatewaySynth wired with the mode lookup + minter, then
// assert the inbound request shape on the receiving side.

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/internalsvc"
	"github.com/onebox-faas/faas/pkg/state"
)

// capturedRequest is the recorded inbound request the test
// server sees. The Authorization header is the load-bearing
// assertion target — every test below scans it for the
// expected substring.
type capturedRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   []byte
}

func (c capturedRequest) authHeader() string {
	return c.Header.Get("Authorization")
}

// captureServer is the test-side HTTP handler that captures
// every inbound request so tests can assert on it. Returns
// 200 OK + a minimal JSON envelope so both SynthesizeRequest
// (which doesn't read the body) and Invoke (which decodes a
// {state,result} pair) are happy. Round-6 follow-up: before
// this fix, /v1/invocations:dispatch received an empty 200,
// and Invoke's json.NewDecoder hit EOF decoding the empty
// body. SynthesizeRequest doesn't decode, so it slipped past
// earlier rounds.
func captureServer(captured *atomic.Value) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cp := capturedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Header: r.Header.Clone(),
			Body:   body,
		}
		captured.Store(cp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"state":"dispatching","result":null}`))
	})
}

// newTestHTTPGatewaySynth builds a real *httpGatewaySynth
// targeting the test server, with the given mode-lookup +
// minter closures. Returns the synth + the captured-request
// sink the test server writes into.
func newTestHTTPGatewaySynth(t *testing.T, srvURL string, modeLookup PublicAuthModeLookupFunc, minter InternalSvcMintFunc) (*httpGatewaySynth, *atomic.Value) {
	t.Helper()
	captured := &atomic.Value{}
	mux := http.NewServeMux()
	mux.Handle("/v1/synthesize", captureServer(captured))
	mux.Handle("/v1/invocations:dispatch", captureServer(captured))
	mux.Handle("/v1/invocations:dispatch_batch", captureServer(captured))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	// Build the client manually so we can dial the test
	// server (http://...) — production DialGatewaySynth uses
	// a unix-socket dialer.
	tr := &http.Transport{}
	c := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	hg := &httpGatewaySynth{
		client:     c,
		basePrefix: srv.URL,
		log:        slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
	if modeLookup != nil {
		hg.WithAppPublicAuthModeLookup(modeLookup)
	}
	if minter != nil {
		hg.WithMintInternalSvcToken(minter)
	}
	return hg, captured
}

// loadCaptured reads the captured-request atomic. Returns the
// zero-value when nothing was captured yet.
func loadCaptured(c *atomic.Value) capturedRequest {
	v := c.Load()
	if v == nil {
		return capturedRequest{}
	}
	return v.(capturedRequest)
}

// mintFn builds an InternalSvcMintFunc closure over the given
// keypair + svcName. Returns a token whose signature the
// receiver can verify (the test allowlist mirrors this
// keypair).
func mintFn(t *testing.T, pub ed25519.PublicKey, priv ed25519.PrivateKey, svcName string) InternalSvcMintFunc {
	t.Helper()
	sum := sha256.Sum256(pub)
	kid := base64.RawURLEncoding.EncodeToString(sum[:16])
	return func(appID string) (string, error) {
		return internalsvc.Mint(svcName, 30*time.Second, map[string]any{"app_id": appID}, priv, kid)
	}
}

// TestSynthesizeRequest_InternalOnlyAttachesJWT covers the
// load-bearing assertion: when the app's mode is
// 'internal_only', the outbound /v1/synthesize request MUST
// carry Authorization: Bearer <JWT>. Without this attachment,
// the synth-side gate (synth.go) would 403 and the cron fire
// would fail silently.
func TestSynthesizeRequest_InternalOnlyAttachesJWT(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	modeLookup := func(_ context.Context, appID string) (PublicAuthModeLookupResult, error) {
		if appID == "app-internal" {
			return PublicAuthModeLookupResult{Mode: "internal_only"}, nil
		}
		return PublicAuthModeLookupResult{Mode: "open"}, nil
	}
	hg, captured := newTestHTTPGatewaySynth(t, "http://example.invalid", modeLookup, mintFn(t, pub, priv, "schedd"))

	if err := hg.SynthesizeRequest(context.Background(), "app-internal", http.MethodPost, "/"); err != nil {
		t.Fatalf("SynthesizeRequest: %v", err)
	}
	cap := loadCaptured(captured)
	if cap.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", cap.Method)
	}
	if cap.Path != "/v1/synthesize" {
		t.Errorf("path = %s, want /v1/synthesize", cap.Path)
	}
	auth := cap.authHeader()
	if !strings.HasPrefix(auth, "Bearer ") {
		t.Fatalf("missing Authorization header; got %q", auth)
	}
	// Verify the token is well-formed: parses via
	// internalsvc.Verify against the matching allowlist.
	tok := strings.TrimPrefix(auth, "Bearer ")
	_, verr := internalsvc.Verify(tok, map[string]ed25519.PublicKey{"schedd": pub})
	if verr != nil {
		t.Errorf("token failed Verify: %v", verr)
	}
}

// TestSynthesizeRequest_OpenModeCarriesToken: an open-mode app's synth
// request carries a scheduler token bound to its app too. Public traffic
// reaches the gateway's synth listener, so the gateway accepts no synth
// request without one (ADR-20261103090000). The token goes to the gateway
// only; the synth handler never forwards the HTTP Authorization header to
// the app.
func TestSynthesizeRequest_OpenModeCarriesToken(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	modeLookup := func(_ context.Context, _ string) (PublicAuthModeLookupResult, error) {
		return PublicAuthModeLookupResult{Mode: "open"}, nil
	}
	hg, captured := newTestHTTPGatewaySynth(t, "http://example.invalid", modeLookup, mintFn(t, pub, priv, "schedd"))

	if err := hg.SynthesizeRequest(context.Background(), "app-open", http.MethodPost, "/"); err != nil {
		t.Fatalf("SynthesizeRequest: %v", err)
	}
	tok := strings.TrimPrefix(loadCaptured(captured).authHeader(), "Bearer ")
	if _, err := internalsvc.Verify(tok, map[string]ed25519.PublicKey{"schedd": pub}); err != nil {
		t.Fatalf("open-mode token failed Verify: %v", err)
	}
	if got := internalsvc.StringClaim(tok, "app_id"); got != "app-open" {
		t.Fatalf("token app_id = %q, want app-open", got)
	}
}

// TestSynthesizeRequest_NilMinterIsLoud covers the operator-
// misconfig posture: app is in internal_only mode but no
// minter is wired. The current implementation logs a WARN
// + returns no error (the gate 403s on the receiving end).
// Future hardening could turn this into a hard error; for
// PR-A it's loud-but-non-fatal.
func TestSynthesizeRequest_NilMinterIsLoud(t *testing.T) {
	modeLookup := func(_ context.Context, _ string) (PublicAuthModeLookupResult, error) {
		return PublicAuthModeLookupResult{Mode: "internal_only"}, nil
	}
	// minter = nil
	hg, _ := newTestHTTPGatewaySynth(t, "http://example.invalid", modeLookup, nil)

	// No assertion on log output (slog to io.Discard); the
	// contract is "doesn't panic, doesn't error". The gate
	// on the receiving side is what 403s.
	if err := hg.SynthesizeRequest(context.Background(), "app-internal", http.MethodPost, "/"); err != nil {
		t.Errorf("SynthesizeRequest errored on nil-minter; want non-fatal: %v", err)
	}
}

// TestPublicAuthModeFromStore covers the cmd-side closure
// factory. Reads from a fake store; returns ErrAuthModeLookup
// on miss (round-2 follow-up: was "" — a fail-open posture).
func TestPublicAuthModeFromStore(t *testing.T) {
	lookup := PublicAuthModeFromStore(func(_ context.Context, appID string) (state.App, error) {
		if appID == "app-internal" {
			return state.App{PublicAuthMode: "internal_only"}, nil
		}
		return state.App{}, errors.New("not found") // pretend "not found"
	})
	res, err := lookup(context.Background(), "app-internal")
	if err != nil {
		t.Errorf("mode lookup for internal: err=%v, want nil", err)
	}
	if res.Mode != "internal_only" {
		t.Errorf("mode = %q, want internal_only", res.Mode)
	}
	// Miss: err != ErrAuthModeLookup (the sentinel); callers
	// branch on the sentinel to fail-closed.
	_, err = lookup(context.Background(), "app-other")
	if err == nil {
		t.Errorf("mode lookup for missing app: err=nil, want non-nil")
	}
	if !errors.Is(err, ErrAuthModeLookup) {
		t.Errorf("mode lookup miss: err=%v, want ErrAuthModeLookup sentinel", err)
	}
}

// TestConfigureInternalSvcAuth covers the cmd-side wiring
// helper. Sets a mode lookup + minter; asserts the underlying
// httpGatewaySynth retains both (round-trip via a synthetic
// request).
func TestConfigureInternalSvcAuth(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	var gotMode string
	modeLookup := func(_ context.Context, appID string) (PublicAuthModeLookupResult, error) {
		gotMode = appID
		return PublicAuthModeLookupResult{Mode: "open"}, nil
	}
	mint := mintFn(t, pub, priv, "schedd")
	gw, captured := newTestHTTPGatewaySynth(t, "http://example.invalid", nil, nil)

	if ok := ConfigureInternalSvcAuth(gw, modeLookup, mint); !ok {
		t.Errorf("ConfigureInternalSvcAuth returned false")
	}
	if err := gw.SynthesizeRequest(context.Background(), "app-x", http.MethodPost, "/"); err != nil {
		t.Fatalf("SynthesizeRequest: %v", err)
	}
	if gotMode != "app-x" {
		t.Errorf("mode lookup not consulted; got appID = %q, want \"app-x\"", gotMode)
	}
	// ADR-20261103090000: open-mode apps carry a scheduler token too.
	tok := strings.TrimPrefix(loadCaptured(captured).authHeader(), "Bearer ")
	if internalsvc.StringClaim(tok, "app_id") != "app-x" {
		t.Errorf("open-mode request token = %q, want one bound to app-x", tok)
	}
}

// ----------------------------------------------------------------------
// Round-2 code-review tests (peer-review findings #1, #3, #4, #5).
// These cover the three new outbound surfaces
// (SynthesizeRequest, Invoke, postBatch) and the fail-closed
// posture plus the caller-ctx propagation.
// ----------------------------------------------------------------------

// mintFnLoop builds a Loop-level minter closure over the given
// keypair. Mirrors mintFn (httpGatewaySynth-level) but typed
// for the Loop's mintInternalSvcToken field.
func mintFnLoop(t *testing.T, pub ed25519.PublicKey, priv ed25519.PrivateKey, svcName string) func(appID string) (string, error) {
	t.Helper()
	sum := sha256.Sum256(pub)
	kid := base64.RawURLEncoding.EncodeToString(sum[:16])
	return func(appID string) (string, error) {
		return internalsvc.Mint(svcName, 30*time.Second, map[string]any{"app_id": appID}, priv, kid)
	}
}

// TestInvoke_InternalOnlyAttachesJWT covers peer-review finding #1:
// schedd's Invoke path (the move-1 /v1/invocations:dispatch dial)
// MUST attach the JWT for internal_only apps. Without this, the
// gate at synth.go::handleInvocationDispatch 403s every cron + drain
// call to an internal_only app.
func TestInvoke_InternalOnlyAttachesJWT(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	modeLookup := func(_ context.Context, appID string) (PublicAuthModeLookupResult, error) {
		if appID == "app-internal" {
			return PublicAuthModeLookupResult{Mode: "internal_only"}, nil
		}
		return PublicAuthModeLookupResult{Mode: "open"}, nil
	}
	hg, captured := newTestHTTPGatewaySynth(t, "http://example.invalid", modeLookup, mintFn(t, pub, priv, "schedd"))

	inv := state.Invocation{
		ID:     "inv-x",
		AppID:  "app-internal",
		Method: http.MethodPost,
		Path:   "/",
	}
	if _, err := hg.Invoke(context.Background(), "app-internal", inv); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	cap := loadCaptured(captured)
	if cap.Path != "/v1/invocations:dispatch" {
		t.Errorf("path = %s, want /v1/invocations:dispatch", cap.Path)
	}
	auth := cap.authHeader()
	if !strings.HasPrefix(auth, "Bearer ") {
		t.Fatalf("Invoke missing Authorization header; got %q", auth)
	}
	tok := strings.TrimPrefix(auth, "Bearer ")
	if _, verr := internalsvc.Verify(tok, map[string]ed25519.PublicKey{"schedd": pub}); verr != nil {
		t.Errorf("Invoke token failed Verify: %v", verr)
	}
}

// TestInvoke_OpenModeCarriesToken: an open-mode app's dispatch carries a
// scheduler token bound to its app (ADR-20261103090000).
func TestInvoke_OpenModeCarriesToken(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	modeLookup := func(_ context.Context, _ string) (PublicAuthModeLookupResult, error) {
		return PublicAuthModeLookupResult{Mode: "open"}, nil
	}
	hg, captured := newTestHTTPGatewaySynth(t, "http://example.invalid", modeLookup, mintFn(t, pub, priv, "schedd"))

	inv := state.Invocation{ID: "inv-open", AppID: "app-open", Method: http.MethodPost, Path: "/"}
	if _, err := hg.Invoke(context.Background(), "app-open", inv); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	tok := strings.TrimPrefix(loadCaptured(captured).authHeader(), "Bearer ")
	if _, err := internalsvc.Verify(tok, map[string]ed25519.PublicKey{"schedd": pub}); err != nil || internalsvc.StringClaim(tok, "app_id") != "app-open" {
		t.Errorf("Invoke token on open mode = %q (verify err %v), want one bound to app-open", tok, err)
	}
}

// TestSynthesizeRequest_LookupErrorFailsClosed covers peer-review
// finding #3: a transient lookup error (Postgres outage, missing
// app, etc.) MUST degrade to "assume internal_only" (fail-closed)
// rather than "open" (fail-open). Without this, a DB outage
// during a cron tick to an internal_only app would omit the JWT
// AND the gateway-side gate would also return open on the same
// error → invoke succeeds without auth.
//
// The test asserts that a lookup error → JWT IS attached.
func TestSynthesizeRequest_LookupErrorFailsClosed(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	modeLookup := func(_ context.Context, _ string) (PublicAuthModeLookupResult, error) {
		// Simulate a Postgres outage / not-found / etc.
		return PublicAuthModeLookupResult{}, ErrAuthModeLookup
	}
	hg, captured := newTestHTTPGatewaySynth(t, "http://example.invalid", modeLookup, mintFn(t, pub, priv, "schedd"))

	if err := hg.SynthesizeRequest(context.Background(), "app-internal", http.MethodPost, "/"); err != nil {
		t.Fatalf("SynthesizeRequest: %v", err)
	}
	auth := loadCaptured(captured).authHeader()
	if !strings.HasPrefix(auth, "Bearer ") {
		t.Errorf("Lookup-failure should attach JWT (fail-closed); got %q", auth)
	}
}

// TestSynthesizeRequest_LookupErrorNoMinterStillLoud covers the
// edge case: lookup fails AND minter is not wired. The current
// implementation logs a WARN and emits no header (the gate 403s
// on the receiving end). This is the loud-but-non-fatal posture.
func TestSynthesizeRequest_LookupErrorNoMinterStillLoud(t *testing.T) {
	modeLookup := func(_ context.Context, _ string) (PublicAuthModeLookupResult, error) {
		return PublicAuthModeLookupResult{}, ErrAuthModeLookup
	}
	// minter nil
	hg, captured := newTestHTTPGatewaySynth(t, "http://example.invalid", modeLookup, nil)

	if err := hg.SynthesizeRequest(context.Background(), "app-internal", http.MethodPost, "/"); err != nil {
		t.Errorf("SynthesizeRequest err = %v, want non-fatal", err)
	}
	if got := loadCaptured(captured).authHeader(); got != "" {
		t.Errorf("no minter → no header; got %q", got)
	}
}

// TestSynthesizeRequest_CallerCtxPropagates covers peer-review
// finding #4: the lookup fn must receive the caller's ctx, not
// context.Background(). This test marks the parent ctx with a
// sentinel deadline and asserts the lookup observes the same
// deadline (proving the ctx flow), without forcing the HTTP
// round-trip to fail.
//
// Pre-canceling the ctx would propagate to the HTTP client
// (Post "...": context canceled), so the test instead uses
// a deadline-bound ctx and asserts the lookup's ctx carries
// the same deadline.
func TestSynthesizeRequest_CallerCtxPropagates(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	var gotDeadline time.Time
	var gotHasDeadline bool
	modeLookup := func(ctx context.Context, _ string) (PublicAuthModeLookupResult, error) {
		gotDeadline, gotHasDeadline = ctx.Deadline()
		return PublicAuthModeLookupResult{Mode: "open"}, nil
	}
	hg, _ := newTestHTTPGatewaySynth(t, "http://example.invalid", modeLookup, mintFn(t, pub, priv, "schedd"))

	// Mark the ctx with a 1-hour deadline to give the lookup
	// a unique deadline fingerprint. The test server returns
	// 200 immediately, so the deadline is never hit.
	parent, cancel := context.WithTimeout(context.Background(), 1*time.Hour)
	defer cancel()
	if err := hg.SynthesizeRequest(parent, "app-x", http.MethodPost, "/"); err != nil {
		t.Fatalf("SynthesizeRequest: %v", err)
	}
	if !gotHasDeadline {
		t.Errorf("mode lookup received ctx without deadline; want caller's deadline")
	}
	if gotDeadline.IsZero() {
		t.Errorf("mode lookup got zero deadline; want caller's deadline propagated")
	}
}

// TestPostBatch_InternalOnlyAttachesJWT covers peer-review finding #5:
// the trigger batch endpoint /v1/invocations:dispatch_batch must
// also attach the JWT for internal_only apps. The Loop has its own
// mode-lookup + minter (postBatch calls l.appPublicAuthModeLookup,
// not httpGatewaySynth.appPublicAuthModeLookup).
func TestPostBatch_InternalOnlyAttachesJWT(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	modeLookup := func(_ context.Context, appID string) (PublicAuthModeLookupResult, error) {
		if appID == "app-internal" {
			return PublicAuthModeLookupResult{Mode: "internal_only"}, nil
		}
		return PublicAuthModeLookupResult{Mode: "open"}, nil
	}
	mint := mintFnLoop(t, pub, priv, "schedd")

	// Build a Loop with HTTPClientForGatewaySynthTarget pointing
	// at the test server. The test server only mounts the routes
	// we care about; everything else 404s.
	captured := &atomic.Value{}
	mux := http.NewServeMux()
	mux.Handle("/v1/invocations:dispatch_batch", captureServer(captured))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Construct a minimal Loop. We don't need the engine for
	// postBatch — the test invokes postBatch directly.
	loop := &Loop{
		gatewayHTTPClient:       &http.Client{Timeout: 5 * time.Second},
		gatewayBaseURL:          srv.URL,
		log:                     slog.New(slog.NewJSONHandler(io.Discard, nil)),
		appPublicAuthModeLookup: modeLookup,
		mintInternalSvcToken:    mint,
	}

	env := triggerDispatchRequest{
		InvocationID: "trigger-x",
		AppID:        "app-internal",
		Source:       "esm",
		TriggerID:    "tr-1",
		Records:      []triggerDispatchRecord{{ItemIdentifier: "item-1"}},
	}
	if _, err := loop.postBatch(context.Background(), env); err != nil {
		t.Fatalf("postBatch: %v", err)
	}
	auth := loadCaptured(captured).authHeader()
	if !strings.HasPrefix(auth, "Bearer ") {
		t.Errorf("postBatch missing Authorization header; got %q", auth)
	}
	tok := strings.TrimPrefix(auth, "Bearer ")
	if _, verr := internalsvc.Verify(tok, map[string]ed25519.PublicKey{"schedd": pub}); verr != nil {
		t.Errorf("postBatch token failed Verify: %v", verr)
	}
}

// TestPostBatch_OpenModeCarriesToken: an open-mode app's trigger batch
// carries a scheduler token bound to its app (ADR-20261103090000).
func TestPostBatch_OpenModeCarriesToken(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	modeLookup := func(_ context.Context, _ string) (PublicAuthModeLookupResult, error) {
		return PublicAuthModeLookupResult{Mode: "open"}, nil
	}
	mint := mintFnLoop(t, pub, priv, "schedd")

	captured := &atomic.Value{}
	mux := http.NewServeMux()
	mux.Handle("/v1/invocations:dispatch_batch", captureServer(captured))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	loop := &Loop{
		gatewayHTTPClient:       &http.Client{Timeout: 5 * time.Second},
		gatewayBaseURL:          srv.URL,
		log:                     slog.New(slog.NewJSONHandler(io.Discard, nil)),
		appPublicAuthModeLookup: modeLookup,
		mintInternalSvcToken:    mint,
	}
	env := triggerDispatchRequest{
		InvocationID: "trigger-x",
		AppID:        "app-open",
		Source:       "esm",
		TriggerID:    "tr-1",
		Records:      []triggerDispatchRecord{{ItemIdentifier: "item-1"}},
	}
	if _, err := loop.postBatch(context.Background(), env); err != nil {
		t.Fatalf("postBatch: %v", err)
	}
	tok := strings.TrimPrefix(loadCaptured(captured).authHeader(), "Bearer ")
	if _, err := internalsvc.Verify(tok, map[string]ed25519.PublicKey{"schedd": pub}); err != nil || internalsvc.StringClaim(tok, "app_id") != "app-open" {
		t.Errorf("postBatch token on open mode = %q (verify err %v), want one bound to app-open", tok, err)
	}
}
