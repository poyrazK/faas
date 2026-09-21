// normal_path_e2e_test.go — KVM-free customer-path acceptance.
//
// This test deliberately boots the real apid, schedd, and gatewayd-internal
// processes against a real Postgres schema. A small fake vmmd speaks the real
// Unix-socket ForwardHTTPStream protocol so the test can exercise the
// production gateway route/cache → gRPC bridge without requiring KVM.
//
// This file is the general-path acceptance slice. It deliberately keeps one
// test prefix so developers can run the whole group without waiting for the
// unrelated E2E families.
//
// The scenarios cover the most important general-path invariants:
//
//   create app → publish a live deployment → route a request → publish a
//   replacement deployment → route the next request to the replacement.
//   forward method/path/headers/body faithfully through the bridge, including
//   chunking a larger request body.
//   keep gateway-internal headers out of the guest request.
//   reject an unknown host before it reaches the bridge.
//   normalize host casing and ports without changing the selected app.
//   enforce per-app authentication before waking or forwarding.
//   reassemble a response delivered in multiple VMMD body frames.
//   preserve an empty 204 response and its response metadata.
//   preserve HEAD response metadata while suppressing its body.
//   preserve response trailers delivered after the body.
//   deliver an async invocation through the real schedd/gateway synth socket,
//   persist its result, and retry it after a transient bridge outage.
//   turn a guest 4xx into a terminal failed async invocation.
//   replay an async request with the same idempotency key without duplicating
//   durable work.
//   complete a synchronous invoke through the same bridge and long-poll path.
//   deliver a queue row through the real synthetic gateway bridge and preserve
//   its payload/result projection.
//   deliver a queue row through a first-class queue trigger without a manual
//   receive call, and persist both invocation and trigger-record success.
//   deliver multiple queue rows without dropping messages.
//   exhaust queue retries and expose the preserved row in dead-letter reads.
//   hold a delayed task until scheduled_at, then deliver it through the real
//   synthetic gateway bridge.
//   cancel a delayed task before its due time without delivering it.
//   preserve a guest-generated non-2xx status, headers, and body.
//   preserve a guest-generated 5xx on direct HTTP instead of confusing it with
//   a bridge outage.
//   flush successful proxy activity through gatewayd -> schedd into durable
//   request_count/last_request_at state, including a coalesced request burst.
//   avoid refreshing durable activity for guest 4xx responses.
//   surface a VMMD outage as a customer-visible 503.
//   retry a durable invocation after a guest 503, then persist the recovery.
//   stop a running instance and invalidate the cached route.
//   restart gatewayd and reload the durable route from Postgres.
//   terminate an in-flight response during a gateway restart and serve the
//   next request from a fresh gateway process.
//   reclaim an abandoned async dispatch after schedd restart and complete it
//   exactly once from its durable lease.
//   preserve request/body/header isolation across concurrent bridge streams.
//   apply per-instance backpressure and release the waiting request after
//   the active request completes.
//   cancel a queued request without reaching the bridge or leaking capacity.
//   map a queued platform-budget expiry to the canonical 504 problem.
//   distribute warm traffic across live sibling instances and fail over away
//   from a stale bridge target when a replacement is published.
//   isolate a live 0%-traffic candidate, apply live traffic weight changes
//   through the API, observe pg_notify refresh, and roll the split back.
//   carry the http1/http2/grpc app-protocol selector through the bridge,
//   including grpc trailers.
//   keep guest hop-by-hop response headers off the customer-facing response.
//
// Deployment creation is seeded at the state boundary here because the
// source/build/image pipeline is intentionally owned by the native acceptance
// suite. This test owns the control-plane/data-plane wiring that can run in
// ordinary PR CI.

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type normalPathFixture struct {
	h         *e2etest.Harness
	vmmd      *e2etest.FakeVMMD
	store     *state.PgStore
	app       api.AppResponse
	key       string
	nodeID    string
	host      string
	ctx       context.Context
	artifacts storage.StorageBackend
}

func newNormalPathFixture(t *testing.T, slug string) *normalPathFixture {
	return newNormalPathFixtureWithPlan(t, slug, api.PlanHobby)
}

func newNormalPathFixtureWithPlan(t *testing.T, slug string, plan api.Plan) *normalPathFixture {
	return newNormalPathFixtureWithPlanAndEnv(t, slug, plan)
}

func newNormalPathFixtureWithPlanAndEnv(t *testing.T, slug string, plan api.Plan, extraEnv ...string) *normalPathFixture {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return nil
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	vmmdSockDir, err := os.MkdirTemp("", "faas-e2e-normal-vmmd-*")
	if err != nil {
		t.Fatalf("create fake vmmd socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(vmmdSockDir) })
	vmmdSock := filepath.Join(vmmdSockDir, "vmmd.sock")
	vmmd := e2etest.StartFakeVMMD(t, vmmdSock)
	t.Setenv("FAAS_E2E_VMMD_SOCKET", vmmdSock)
	// Every normal-path fixture gets a real artifact store, because every live
	// deployment needs a readable, signed rootfs layer.
	//
	// This used to be optional, and the tests passed only because the fake
	// vmmd left Destroy/StopInstance/PauseAndSnapshot Unimplemented: the
	// reaper could never actually tear an instance down, so a seeded RUNNING
	// instance lived forever and no request ever had to wake. Once those RPCs
	// worked, the reaper did too, the next request became a real wake, and the
	// wake failed its layer check — surfacing as a 404 "no live deployment to
	// wake" several steps removed from the cause.
	//
	// A deployment with neither a live snapshot nor a cold-bootable rootfs
	// violates invariant §6.2-3 and is a state production never reaches, so
	// the fixture was asserting against an impossible app. Callers may still
	// override the root via extraEnv (the debugger fixture does); extraEnv is
	// appended last so their value wins.
	artifactDir, err := os.MkdirTemp("", "faas-e2e-normal-artifacts-*")
	if err != nil {
		t.Fatalf("create artifact dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(artifactDir) })
	artifacts, err := storage.NewLocalStorageBackend(artifactDir)
	if err != nil {
		t.Fatalf("create artifact store: %v", err)
	}
	// Pin the Firecracker version for every normal-path test, not just the
	// snapshot ones. An unpinned schedd runs with fcVer="" — a state a
	// production node never has — and silently routes every wake down the
	// cold-boot edge, which is exactly how the restore path escaped e2e
	// coverage in the first place.
	extraEnv = append([]string{
		"FAAS_SCHEDD_FC_VERSION=" + e2etest.FakeFCVersion,
		"FAAS_STORAGE_BACKEND=local",
		"FAAS_STORAGE_ROOT=" + artifactDir,
	}, extraEnv...)
	// Boot the real public edge in front of gatewayd-internal (ADR-070).
	//
	// Production has no path that reaches gatewayd-internal from outside: every
	// customer request lands on gatewayd-public and is handed over a unix
	// socket. Testing against gatewayd-internal directly cannot observe
	// anything in that handover, and that is exactly where two-hop bugs live —
	// PR #1284 shipped because gatewayd-public's own 3s parent budget silently
	// capped every `kind=budget` rule, with unit tests on both sides passing.
	//
	// gatewayd-public was previously booted by exactly one e2e test, which is
	// metal-tagged and therefore never runs; this is its first coverage on a
	// gate that executes. Requests go through h.EdgeURL(), which resolves to
	// the public listener whenever it is booted.
	h := e2etest.StartWithEnv(t, pool,
		e2etest.APID|e2etest.Schedd|e2etest.Gatewayd|e2etest.GatewaydPublic, extraEnv)
	ctx := context.Background()
	key := h.SeedAccount(ctx, plan, slug)
	body, statusCode := doReq(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug, Type: string(state.AppTypeApp), RequireAuthn: boolPtr(false)})
	if statusCode != http.StatusCreated {
		t.Fatalf("create app: status=%d body=%s", statusCode, body)
	}
	var app api.AppResponse
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, body)
	}
	store := state.NewPgStore(pool)
	return &normalPathFixture{
		artifacts: artifacts,
		h:         h,
		vmmd:      vmmd,
		store:     store,
		app:       app,
		key:       key,
		nodeID:    defaultLocalComputeNodeID(t, ctx, store),
		host:      slug + ".apps.test.example",
		ctx:       ctx,
	}
}

func TestE2E_NormalPath_RealGatewayBridgeAndRedeployRefresh(t *testing.T) {
	f := newNormalPathFixture(t, "normal-path")
	if f == nil {
		return
	}
	firstDep, firstInstance := createNormalPathLiveDeployment(t, f, f.app.ID, "v1")
	f.vmmd.SetVersion(firstInstance.ID, "v1")

	firstBody := waitForNormalPathResponse(t, f.h, f.host, "normal-path:v1\n", 10*time.Second)
	if string(firstBody) != "normal-path:v1\n" {
		t.Fatalf("first request body=%q, want v1", firstBody)
	}
	firstRequest := f.vmmd.LastRequest()
	if firstRequest == nil {
		t.Fatal("fake vmmd received no ForwardHTTPStream request")
	}
	if firstRequest.Instance != firstInstance.ID {
		t.Fatalf("first request instance=%q, want %q", firstRequest.Instance, firstInstance.ID)
	}
	if firstRequest.RequestUri != "/" {
		t.Fatalf("first request URI=%q, want /", firstRequest.RequestUri)
	}

	// A replacement deployment is published through the same durable state
	// transitions used by the deploy pipeline. The gateway must observe the
	// deployment_changed/instance_changed notifications and stop routing to
	// the old live deployment.
	secondDep, secondInstance := createNormalPathLiveDeployment(t, f, f.app.ID, "v2")
	f.vmmd.SetVersion(secondInstance.ID, "v2")
	notifyNormalPathDeploymentChanged(t, f, secondDep.ID)
	notifyNormalPathInstanceChanged(t, f, secondInstance.ID, string(state.StateRunning))

	secondBody := waitForNormalPathResponse(t, f.h, f.host, "normal-path:v2\n", 10*time.Second)
	if string(secondBody) != "normal-path:v2\n" {
		t.Fatalf("replacement request body=%q, want v2", secondBody)
	}
	secondRequest := f.vmmd.LastRequest()
	if secondRequest == nil || secondRequest.Instance != secondInstance.ID {
		t.Fatalf("replacement request instance=%q, want %q", requestInstance(secondRequest), secondInstance.ID)
	}
	if secondRequest.Instance == firstInstance.ID {
		t.Fatalf("replacement request still used old instance %q", firstInstance.ID)
	}

	gotFirst, err := f.store.DeploymentByID(f.ctx, firstDep.ID)
	if err != nil {
		t.Fatalf("read first deployment: %v", err)
	}
	gotSecond, err := f.store.DeploymentByID(f.ctx, secondDep.ID)
	if err != nil {
		t.Fatalf("read second deployment: %v", err)
	}
	if gotFirst.Status != state.DeploySuperseded {
		t.Errorf("first deployment status=%q, want superseded", gotFirst.Status)
	}
	if gotSecond.Status != state.DeployLive {
		t.Errorf("second deployment status=%q, want live", gotSecond.Status)
	}
}

// TestE2E_NormalPath_ForwardsHTTPContract catches bridge regressions that a
// body-only smoke misses: the guest must receive the original method,
// request-target, application headers, and body while hop-by-hop headers are
// removed before the gRPC boundary.
func TestE2E_NormalPath_ForwardsHTTPContract(t *testing.T) {
	f := newNormalPathFixture(t, "normal-contract")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "v1")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:v1\n", 10*time.Second)

	requestBody := []byte(`{"hello":"` + strings.Repeat("x", 20000) + `"}`)
	_, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodPost, "/echo?item=42", struct {
		Hello string `json:"hello"`
	}{Hello: strings.Repeat("x", 20000)}, map[string]string{
		"X-Customer-Test":        "present",
		"Connection":             "close",
		"X-Faas-Internal-Secret": "do-not-leak",
	})
	if statusCode != http.StatusOK || string(body) != "normal-path:v1\n" {
		t.Fatalf("POST bridge: status=%d body=%q", statusCode, body)
	}
	request := f.vmmd.LastRequest()
	if request == nil {
		t.Fatal("fake vmmd received no POST")
	}
	if request.Method != http.MethodPost || request.RequestUri != "/echo?item=42" {
		t.Fatalf("request shape = method %q uri %q", request.Method, request.RequestUri)
	}
	if !hasNormalPathHeader(request, "X-Customer-Test", "present") {
		t.Fatal("customer header did not reach ForwardHTTPStream")
	}
	if hasNormalPathHeaderName(request, "Connection") {
		t.Fatal("hop-by-hop Connection header reached ForwardHTTPStream")
	}
	if hasNormalPathHeaderName(request, "X-Faas-Internal-Secret") {
		t.Fatal("gateway-internal header reached ForwardHTTPStream")
	}
	if request.AppProtocol != "http1" {
		t.Fatalf("app protocol=%q, want http1", request.AppProtocol)
	}
	if !request.ContentLengthKnown || request.ContentLength != int64(len(requestBody)) {
		t.Fatalf("content length = (%d, known=%t), want (%d, true)", request.ContentLength, request.ContentLengthKnown, len(requestBody))
	}
	if string(f.vmmd.LastBody()) != string(requestBody) {
		t.Fatalf("forwarded body = %q, want %q", f.vmmd.LastBody(), requestBody)
	}
	if chunks := f.vmmd.LastBodyChunkCount(); chunks < 2 {
		t.Fatalf("forwarded body used %d bridge chunks, want multiple chunks", chunks)
	}
}

// TestE2E_NormalPath_UnknownHostDoesNotReachBridge catches routing/config
// regressions where an unresolved Host falls through to a previously cached
// target and accidentally serves another customer's instance.
func TestE2E_NormalPath_UnknownHostDoesNotReachBridge(t *testing.T) {
	f := newNormalPathFixture(t, "normal-host-isolation")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "v1")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:v1\n", 10*time.Second)
	before := f.vmmd.ForwardCount()

	_, body, statusCode := doReqHeaders(t, f.h, "missing.apps.test.example", http.MethodGet, "/", nil)
	if statusCode != http.StatusNotFound {
		t.Fatalf("unknown host status=%d body=%q, want 404", statusCode, body)
	}
	if got := f.vmmd.ForwardCount(); got != before {
		t.Fatalf("unknown host reached bridge: forward count=%d, want %d", got, before)
	}
}

// TestE2E_NormalPath_HostNormalizationPreservesRoute catches edge/proxy
// regressions where a valid Host with casing or an explicit port misses the
// app lookup or resolves to the wrong route.
func TestE2E_NormalPath_HostNormalizationPreservesRoute(t *testing.T) {
	f := newNormalPathFixture(t, "normal-host-normalization")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "v1")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:v1\n", 10*time.Second)

	normalizedHost := strings.ToUpper(f.host) + ":443"
	_, body, statusCode := doReqHeaders(t, f.h, normalizedHost, http.MethodGet, "/normalized", nil)
	if statusCode != http.StatusOK || string(body) != "normal-path:v1\n" {
		t.Fatalf("normalized host status=%d body=%q, want 200/v1", statusCode, body)
	}
	if request := f.vmmd.LastRequest(); request == nil || request.Instance != instance.ID {
		t.Fatalf("normalized host request instance=%q, want %q", requestInstance(request), instance.ID)
	}
}

// TestE2E_NormalPath_RequireAuthnBlocksUnauthenticatedTraffic catches a
// security regression where the real gateway route ignores require_authn or
// forwards before checking it. Missing credentials must be denied without
// bridge traffic, while a foreign key is scoped out and the owning account's
// valid key still works.
func TestE2E_NormalPath_RequireAuthnBlocksUnauthenticatedTraffic(t *testing.T) {
	f := newNormalPathFixture(t, "normal-authn")
	if f == nil {
		return
	}
	authKey := f.h.SeedAccount(f.ctx, api.PlanPro, "normal-authn-owner")
	body, statusCode := doReq(t, f.h, authKey, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "normal-authn-gated", Type: string(state.AppTypeApp), RequireAuthn: boolPtr(true)})
	if statusCode != http.StatusCreated {
		t.Fatalf("create gated app: status=%d body=%s", statusCode, body)
	}
	var app api.AppResponse
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode gated app: %v body=%s", err, body)
	}
	_, instance := createNormalPathLiveDeployment(t, f, app.ID, "authn")
	f.vmmd.SetVersion(instance.ID, "authn")

	before := f.vmmd.ForwardCount()
	_, body, statusCode = doReqHeaders(t, f.h, "normal-authn-gated.apps.test.example", http.MethodGet, "/", nil)
	if statusCode != http.StatusUnauthorized {
		t.Fatalf("missing auth status=%d body=%q, want 401", statusCode, body)
	}
	if got := f.vmmd.ForwardCount(); got != before {
		t.Fatalf("missing auth reached bridge: forward count=%d, want %d", got, before)
	}
	_, body, statusCode = doReqHeaders(t, f.h, "normal-authn-gated.apps.test.example", http.MethodGet, "/", nil,
		map[string]string{"Authorization": "Bearer " + f.key})
	if statusCode != http.StatusForbidden {
		t.Fatalf("cross-account auth status=%d body=%q, want 403", statusCode, body)
	}
	if got := f.vmmd.ForwardCount(); got != before {
		t.Fatalf("cross-account auth reached bridge: forward count=%d, want %d", got, before)
	}

	deadline := time.Now().Add(10 * time.Second)
	var lastStatus int
	var lastBody []byte
	for time.Now().Before(deadline) {
		_, body, statusCode = doReqHeaders(t, f.h, "normal-authn-gated.apps.test.example", http.MethodGet, "/", nil,
			map[string]string{"Authorization": "Bearer " + authKey})
		lastStatus, lastBody = statusCode, body
		if statusCode == http.StatusOK && string(body) == "normal-path:authn\n" {
			if request := f.vmmd.LastRequest(); request == nil || request.Instance != instance.ID {
				t.Fatalf("authenticated request instance=%q, want %q", requestInstance(request), instance.ID)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("valid auth did not reach gated app: status=%d body=%q", lastStatus, lastBody)
}

// TestE2E_NormalPath_ReassemblesResponseChunks catches bridges that handle a
// single response frame but truncate, overwrite, or prematurely close when
// VMMD streams the same guest response in several chunks.
func TestE2E_NormalPath_ReassemblesResponseChunks(t *testing.T) {
	f := newNormalPathFixture(t, "normal-chunks")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "chunks")
	f.vmmd.SetVersion(instance.ID, "chunks")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:chunks\n", 10*time.Second)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status:  http.StatusOK,
		Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "text/plain"}},
		Chunks:  [][]byte{[]byte("chunk-1|"), []byte("chunk-2|"), []byte("chunk-3\n")},
	})

	_, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, "/chunks", nil)
	if statusCode != http.StatusOK || string(body) != "chunk-1|chunk-2|chunk-3\n" {
		t.Fatalf("chunked response = status %d body %q, want concatenated body", statusCode, body)
	}
}

// TestE2E_NormalPath_NoContentResponsePreservesEmptyBody catches response
// framing regressions where a successful guest 204 is rewritten, gains a
// synthetic body, or loses response metadata while crossing the bridge.
func TestE2E_NormalPath_NoContentResponsePreservesEmptyBody(t *testing.T) {
	f := newNormalPathFixture(t, "normal-no-content")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "no-content")
	f.vmmd.SetVersion(instance.ID, "no-content")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:no-content\n", 10*time.Second)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status: http.StatusNoContent,
		Headers: []*vmmdpb.Header{
			{Name: "X-Guest-Empty", Value: "true"},
		},
		// An explicit empty frame list distinguishes a deliberate empty body
		// from the fake's default response body.
		Chunks: [][]byte{{}},
	})

	headers, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, "/empty", nil)
	if statusCode != http.StatusNoContent {
		t.Fatalf("empty response status=%d body=%q, want 204", statusCode, body)
	}
	if len(body) != 0 {
		t.Fatalf("empty response body=%q, want empty", body)
	}
	if headers.Get("X-Guest-Empty") != "true" {
		t.Fatalf("X-Guest-Empty=%q, want true", headers.Get("X-Guest-Empty"))
	}
}

// TestE2E_NormalPath_HEADSuppressesResponseBody catches protocol regressions
// where the gateway forwards guest bytes on HEAD even though HTTP requires a
// bodyless response while retaining the response headers.
func TestE2E_NormalPath_HEADSuppressesResponseBody(t *testing.T) {
	f := newNormalPathFixture(t, "normal-head")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "head")
	f.vmmd.SetVersion(instance.ID, "head")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:head\n", 10*time.Second)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status: http.StatusOK,
		Headers: []*vmmdpb.Header{
			{Name: "Content-Type", Value: "text/plain"},
			{Name: "X-Guest-Head", Value: "present"},
		},
		Body: []byte("must-not-be-visible-on-head\n"),
	})

	headers, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodHead, "/head", nil)
	if statusCode != http.StatusOK {
		t.Fatalf("HEAD status=%d body=%q, want 200", statusCode, body)
	}
	if len(body) != 0 {
		t.Fatalf("HEAD body=%q, want empty", body)
	}
	if headers.Get("X-Guest-Head") != "present" {
		t.Fatalf("X-Guest-Head=%q, want present", headers.Get("X-Guest-Head"))
	}
	if request := f.vmmd.LastRequest(); request == nil || request.Method != http.MethodHead {
		t.Fatalf("bridge method=%q, want HEAD", requestMethod(request))
	}
}

// TestE2E_NormalPath_PreservesResponseTrailers catches bridge implementations
// that forward the body but lose metadata sent after the initial response
// frame, which breaks streaming checksums and downstream audit consumers.
func TestE2E_NormalPath_PreservesResponseTrailers(t *testing.T) {
	f := newNormalPathFixture(t, "normal-trailers")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "trailers")
	f.vmmd.SetVersion(instance.ID, "trailers")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:trailers\n", 10*time.Second)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status:   http.StatusOK,
		Headers:  []*vmmdpb.Header{{Name: "Content-Type", Value: "text/plain"}},
		Trailers: []*vmmdpb.Header{{Name: "X-Guest-Trailer", Value: "done"}},
		Body:     []byte("trailer-body\n"),
	})

	req, err := http.NewRequestWithContext(f.ctx, http.MethodGet, f.h.EdgeURL()+"/trailers", nil)
	if err != nil {
		t.Fatalf("new trailer request: %v", err)
	}
	req.Host = f.host
	client := *f.h.HTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /trailers: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read trailer response: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "trailer-body\n" {
		t.Fatalf("trailer response status=%d body=%q, want 200/trailer-body", resp.StatusCode, body)
	}
	if resp.Trailer.Get("X-Guest-Trailer") != "done" {
		t.Fatalf("X-Guest-Trailer=%q, want done", resp.Trailer.Get("X-Guest-Trailer"))
	}
}

// TestE2E_NormalPath_AsyncInvokeUsesRealGatewayBridge closes the gap between
// the existing durable-invocation test (which uses a synth stub) and the
// production path. The row must cross apid -> schedd -> gatewayd-internal ->
// vmmd and return the bridge result to the polling API.
func TestE2E_NormalPath_AsyncInvokeUsesRealGatewayBridge(t *testing.T) {
	f := newNormalPathFixture(t, "normal-async")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "async")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:async\n", 10*time.Second)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status:  http.StatusOK,
		Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/json"}},
		Body:    []byte(`{"ok":true,"bridge":"vmmd"}`),
	})

	payload := json.RawMessage(`{"job":"42"}`)
	body, statusCode := doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/normal-async/invoke/async", api.InvokeRequest{
			Method:  http.MethodPut,
			Path:    "/jobs/42?dry_run=true",
			Headers: json.RawMessage(`{"X-Customer-Test":"async","X-Faas-Invocation-Id":"spoofed"}`),
			Payload: payload,
		})
	if statusCode != http.StatusAccepted {
		t.Fatalf("POST /invoke/async: status=%d body=%s", statusCode, body)
	}
	var accepted api.AsyncInvokeResponse
	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatalf("decode async response: %v body=%s", err, body)
	}
	if accepted.ID == "" || accepted.StatusURL != "/v1/invocations/"+accepted.ID {
		t.Fatalf("async response = %+v, want id and matching status_url", accepted)
	}

	inv := pollUntilCompleted(t, f.h, f.key, accepted.ID, 10*time.Second)
	if inv.State != string(state.InvocationCompleted) {
		t.Fatalf("invocation state=%q last_error=%q", inv.State, inv.LastError)
	}
	if inv.InstanceID != instance.ID {
		t.Fatalf("invocation instance_id=%q, want %q", inv.InstanceID, instance.ID)
	}
	if string(inv.Result) != `{"ok":true,"bridge":"vmmd"}` {
		t.Fatalf("invocation result=%s, want bridge JSON", inv.Result)
	}

	request := f.vmmd.LastRequest()
	if request == nil {
		t.Fatal("fake vmmd received no async invocation")
	}
	if request.Instance != instance.ID || request.Method != http.MethodPut || request.RequestUri != "/jobs/42?dry_run=true" {
		t.Fatalf("async request = instance %q method %q uri %q", request.Instance, request.Method, request.RequestUri)
	}
	if !hasNormalPathHeader(request, "X-Customer-Test", "async") {
		t.Fatal("async customer header did not reach ForwardHTTPStream")
	}
	if !hasNormalPathHeader(request, "X-Faas-Invocation-Id", accepted.ID) {
		t.Fatal("platform invocation id header did not reach ForwardHTTPStream")
	}
	if !normalPathJSONEqual(f.vmmd.LastBody(), payload) {
		t.Fatalf("async forwarded body=%q, want %q", f.vmmd.LastBody(), payload)
	}
}

// TestE2E_NormalPath_AsyncInvokeGuestFailureIsTerminal proves that a guest
// response in the client-error range is an application outcome, not a
// scheduler retry signal. The real synth bridge must persist failed state and
// retain the guest failure detail for diagnosis.
func TestE2E_NormalPath_AsyncInvokeGuestFailureIsTerminal(t *testing.T) {
	f := newNormalPathFixture(t, "normal-async-failure")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "async-failure")
	f.vmmd.SetVersion(instance.ID, "async-failure")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:async-failure\n", 10*time.Second)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status:  http.StatusUnprocessableEntity,
		Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/json"}},
		Body:    []byte(`{"error":"invalid input"}`),
	})

	body, statusCode := doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/normal-async-failure/invoke/async", api.InvokeRequest{
			Payload: json.RawMessage(`{"bad":true}`),
		})
	if statusCode != http.StatusAccepted {
		t.Fatalf("POST /invoke/async: status=%d body=%s", statusCode, body)
	}
	var accepted api.AsyncInvokeResponse
	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatalf("decode async response: %v body=%s", err, body)
	}

	inv := pollUntilCompleted(t, f.h, f.key, accepted.ID, 10*time.Second)
	if inv.State != string(state.InvocationFailed) {
		t.Fatalf("guest failure state=%q last_error=%q, want failed", inv.State, inv.LastError)
	}
	if !strings.Contains(inv.LastError, "application returned HTTP 422") ||
		!strings.Contains(inv.LastError, `{"error":"invalid input"}`) {
		t.Fatalf("guest failure last_error=%q, want HTTP 422 and guest JSON", inv.LastError)
	}
}

// TestE2E_NormalPath_AsyncIdempotencyDoesNotDuplicateWork pins the retry
// contract at the real API/state boundary. A client that retries after losing
// the first response must observe the original invocation id, and that row
// must still travel through the real bridge exactly as ordinary work does.
func TestE2E_NormalPath_AsyncIdempotencyDoesNotDuplicateWork(t *testing.T) {
	f := newNormalPathFixture(t, "normal-idempotency")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "idempotency")
	f.vmmd.SetVersion(instance.ID, "idempotency")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:idempotency\n", 10*time.Second)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status:  http.StatusOK,
		Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/json"}},
		Body:    []byte(`{"idempotent":true}`),
	})

	request := api.InvokeRequest{Payload: json.RawMessage(`{"once":true}`)}
	extra := map[string]string{"Idempotency-Key": "normal-path-idempotency-key"}
	firstBody, firstStatus := doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/normal-idempotency/invoke/async", request, extra)
	secondBody, secondStatus := doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/normal-idempotency/invoke/async", request, extra)
	if firstStatus != http.StatusAccepted || secondStatus != http.StatusAccepted {
		t.Fatalf("idempotent statuses=(%d,%d), bodies=(%s,%s); want two 202 responses", firstStatus, secondStatus, firstBody, secondBody)
	}
	var first, second api.AsyncInvokeResponse
	if err := json.Unmarshal(firstBody, &first); err != nil {
		t.Fatalf("decode first async response: %v body=%s", err, firstBody)
	}
	if err := json.Unmarshal(secondBody, &second); err != nil {
		t.Fatalf("decode replay async response: %v body=%s", err, secondBody)
	}
	if first.ID == "" || first.ID != second.ID || first.StatusURL != second.StatusURL {
		t.Fatalf("idempotent responses=(%+v,%+v), want the same invocation", first, second)
	}

	inv := pollUntilCompleted(t, f.h, f.key, first.ID, 10*time.Second)
	if inv.State != string(state.InvocationCompleted) || string(inv.Result) != `{"idempotent":true}` {
		t.Fatalf("idempotent invocation=(state=%q,result=%s), want completed guest result", inv.State, inv.Result)
	}
}

// TestE2E_NormalPath_SyncInvokeReturnsRealBridgeResult covers the request /
// response API on top of the same durable invocation machinery. It catches a
// mismatch where async polling works but the invocation_done notification or
// result projection used by the synchronous long-poll path does not.
func TestE2E_NormalPath_SyncInvokeReturnsRealBridgeResult(t *testing.T) {
	f := newNormalPathFixture(t, "normal-sync")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "sync")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:sync\n", 10*time.Second)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status:  http.StatusOK,
		Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/json"}},
		Body:    []byte(`{"ok":true,"mode":"sync"}`),
	})

	payload := json.RawMessage(`{"sync":true}`)
	body, statusCode := doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/normal-sync/invoke", api.InvokeRequest{
			Method:  http.MethodPatch,
			Path:    "/sync?probe=1",
			Payload: payload,
		})
	if statusCode != http.StatusOK {
		t.Fatalf("POST /invoke: status=%d body=%s", statusCode, body)
	}
	var result api.InvokeResponse
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode sync response: %v body=%s", err, body)
	}
	if result.ID == "" || result.Status != string(state.InvocationCompleted) {
		t.Fatalf("sync response = %+v, want completed invocation", result)
	}
	if string(result.Result) != `{"ok":true,"mode":"sync"}` {
		t.Fatalf("sync result=%s, want bridge JSON", result.Result)
	}

	request := f.vmmd.LastRequest()
	if request == nil || request.Instance != instance.ID || request.Method != http.MethodPatch || request.RequestUri != "/sync?probe=1" {
		t.Fatalf("sync request = %#v, want target/method/path", request)
	}
	if !normalPathJSONEqual(f.vmmd.LastBody(), payload) {
		t.Fatalf("sync forwarded body=%q, want %q", f.vmmd.LastBody(), payload)
	}
}

// TestE2E_NormalPath_QueueUsesRealGatewayBridge closes the equivalent gap for
// the queue source. Queue send/receive already has an API-level E2E, but that
// test uses the synth stub and can pass while the real scheduler-to-gateway
// synthetic bridge is broken. The queued payload must reach the fake VMMD and
// the guest result must come back through the queue receive projection.
func TestE2E_NormalPath_QueueUsesRealGatewayBridge(t *testing.T) {
	f := newNormalPathFixture(t, "normal-queue")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "queue")
	f.vmmd.SetVersion(instance.ID, "queue")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:queue\n", 10*time.Second)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status:  http.StatusOK,
		Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/json"}},
		Body:    []byte(`{"queue":"ok"}`),
	})

	payload := json.RawMessage(`{"queued":true}`)
	body, statusCode := doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/normal-queue/queues/send", api.QueueSendRequest{Payload: payload})
	if statusCode != http.StatusCreated {
		t.Fatalf("POST /queues/send: status=%d body=%s", statusCode, body)
	}
	var sent api.QueueSendResponse
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode queue send response: %v body=%s", err, body)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		body, statusCode = doReq(t, f.h, f.key, http.MethodPost,
			"/v1/apps/normal-queue/queues/receive", nil)
		switch statusCode {
		case http.StatusOK:
			var received api.QueueReceiveResponse
			if err := json.Unmarshal(body, &received); err != nil {
				t.Fatalf("decode queue receive response: %v body=%s", err, body)
			}
			if received.ID != sent.ID {
				t.Fatalf("queue receive id=%q, want %q", received.ID, sent.ID)
			}
			if string(received.Payload) != string(payload) {
				t.Fatalf("queue payload=%s, want %s", received.Payload, payload)
			}
			if string(received.Result) != `{"queue":"ok"}` {
				t.Fatalf("queue result=%s, want guest JSON", received.Result)
			}
			request := f.vmmd.LastRequest()
			if request == nil || request.Instance != instance.ID || request.Method != http.MethodPost || request.RequestUri != "/" {
				t.Fatalf("queue bridge request = %#v, want target POST /", request)
			}
			if !normalPathJSONEqual(f.vmmd.LastBody(), payload) {
				t.Fatalf("queue forwarded body=%q, want %q", f.vmmd.LastBody(), payload)
			}
			return
		case http.StatusNoContent:
			time.Sleep(100 * time.Millisecond)
		default:
			t.Fatalf("POST /queues/receive: status=%d body=%s", statusCode, body)
		}
	}
	t.Fatalf("queue receive did not return %q within 15s", sent.ID)
}

// TestE2E_NormalPath_QueueTriggerPushesWithoutReceive proves the queue
// binding is a real push consumer rather than a metadata-only trigger. Once a
// queue trigger is bound to source=queue, schedd must claim and dispatch the
// row through the batch synth path; callers must not need to poll
// /queues/receive to make progress.
func TestE2E_NormalPath_QueueTriggerPushesWithoutReceive(t *testing.T) {
	f := newNormalPathFixture(t, "normal-queue-push")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "queue-push")
	f.vmmd.SetVersion(instance.ID, "queue-push")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:queue-push\n", 10*time.Second)
	// A valid JSON scalar without a batchItemFailures member is a successful
	// delivery. This is the function response the fake guest returns for the
	// synthetic /_triggers/esm/<trigger-id> request.
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status:  http.StatusOK,
		Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/json"}},
		Body:    []byte(`"ok"`),
	})

	createdBody, statusCode := doReq(t, f.h, f.key, http.MethodPost,
		"/v1/triggers", api.CreateTriggerRequest{
			AppID:  f.app.ID,
			Kind:   api.TriggerKindQueue,
			Slug:   "queue-push",
			Config: json.RawMessage(`{"mode":"queue"}`),
		})
	if statusCode != http.StatusCreated {
		t.Fatalf("POST /v1/triggers: status=%d body=%s", statusCode, createdBody)
	}
	var trigger api.Trigger
	if err := json.Unmarshal(createdBody, &trigger); err != nil {
		t.Fatalf("decode trigger response: %v body=%s", err, createdBody)
	}
	if trigger.ID == "" || trigger.Source == nil || *trigger.Source != "queue" {
		t.Fatalf("trigger=%+v, want queue source binding", trigger)
	}

	payload := json.RawMessage(`{"push":true}`)
	sentBody, statusCode := doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/normal-queue-push/queues/send", api.QueueSendRequest{Payload: payload})
	if statusCode != http.StatusCreated {
		t.Fatalf("POST /queues/send: status=%d body=%s", statusCode, sentBody)
	}
	var sent api.QueueSendResponse
	if err := json.Unmarshal(sentBody, &sent); err != nil {
		t.Fatalf("decode queue send response: %v body=%s", err, sentBody)
	}

	invocation := waitForNormalPathInvocationState(t, f.store, sent.ID, state.InvocationCompleted, 20*time.Second)
	if invocation.Outcome == nil || *invocation.Outcome != state.OutcomeSuccess {
		t.Fatalf("invocation outcome=%v, want success", invocation.Outcome)
	}
	if invocation.Attempts != 1 {
		t.Fatalf("invocation attempts=%d, want exactly one delivery", invocation.Attempts)
	}
	if invocation.Source != state.InvocationQueue {
		t.Fatalf("invocation source=%q, want queue", invocation.Source)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		recordsBody, recordsStatus := doReq(t, f.h, f.key, http.MethodGet,
			"/v1/triggers/"+trigger.ID+"/records", nil)
		if recordsStatus != http.StatusOK {
			t.Fatalf("GET trigger records: status=%d body=%s", recordsStatus, recordsBody)
		}
		var records api.ListTriggerRecordsResponse
		if err := json.Unmarshal(recordsBody, &records); err != nil {
			t.Fatalf("decode trigger records: %v body=%s", err, recordsBody)
		}
		for _, record := range records.Records {
			if record.ItemIdentifier != sent.ID {
				continue
			}
			if record.State != "succeeded" {
				t.Fatalf("trigger record state=%q, want succeeded", record.State)
			}
			request := f.vmmd.LastRequest()
			if request == nil || request.Instance != instance.ID || request.RequestUri != "/_triggers/esm/"+trigger.ID {
				t.Fatalf("push request=%#v, want trigger path", request)
			}
			if !normalPathJSONEqual(f.vmmd.LastBody(), payload) {
				t.Fatalf("push payload=%q, want %q", f.vmmd.LastBody(), payload)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("trigger record for queue invocation %q did not reach succeeded", sent.ID)
}

// TestE2E_NormalPath_QueueDeliversMultipleMessages catches queue worker
// regressions hidden by a single-message smoke: rows must retain their
// payloads, all reach the real bridge, and no row may be silently dropped.
func TestE2E_NormalPath_QueueDeliversMultipleMessages(t *testing.T) {
	f := newNormalPathFixture(t, "normal-queue-batch")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "queue-batch")
	f.vmmd.SetVersion(instance.ID, "queue-batch")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:queue-batch\n", 10*time.Second)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status:  http.StatusOK,
		Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/json"}},
		Body:    []byte(`{"queue":"batch-ok"}`),
	})

	payloads := []json.RawMessage{
		json.RawMessage(`{"message":"one"}`),
		json.RawMessage(`{"message":"two"}`),
	}
	sent := make([]api.QueueSendResponse, 0, len(payloads))
	for _, payload := range payloads {
		body, statusCode := doReq(t, f.h, f.key, http.MethodPost,
			"/v1/apps/normal-queue-batch/queues/send", api.QueueSendRequest{Payload: payload})
		if statusCode != http.StatusCreated {
			t.Fatalf("POST /queues/send: status=%d body=%s", statusCode, body)
		}
		var response api.QueueSendResponse
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatalf("decode queue send response: %v body=%s", err, body)
		}
		if response.ID == "" {
			t.Fatalf("queue send response=%+v, want id", response)
		}
		sent = append(sent, response)
	}

	wantPayload := map[string]string{
		sent[0].ID: string(payloads[0]),
		sent[1].ID: string(payloads[1]),
	}
	seen := make(map[string]bool, len(sent))
	duplicates := 0
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		body, statusCode := doReq(t, f.h, f.key, http.MethodPost,
			"/v1/apps/normal-queue-batch/queues/receive", nil)
		switch statusCode {
		case http.StatusNoContent:
			time.Sleep(100 * time.Millisecond)
			continue
		case http.StatusOK:
			var received api.QueueReceiveResponse
			if err := json.Unmarshal(body, &received); err != nil {
				t.Fatalf("decode queue receive response: %v body=%s", err, body)
			}
			want, ok := wantPayload[received.ID]
			if !ok {
				t.Fatalf("received unknown queue id=%q", received.ID)
			}
			if seen[received.ID] {
				duplicates++
				continue
			}
			if string(received.Payload) != want {
				t.Fatalf("queue id=%q payload=%s, want %s", received.ID, received.Payload, want)
			}
			if string(received.Result) != `{"queue":"batch-ok"}` {
				t.Fatalf("queue id=%q result=%s, want batch result", received.ID, received.Result)
			}
			seen[received.ID] = true
			if len(seen) != len(sent) {
				continue
			}

			if duplicates > 0 {
				t.Logf("queue receive redelivered %d completed row(s); queue contract remains at-least-once", duplicates)
			}
			return
		default:
			t.Fatalf("POST /queues/receive: status=%d body=%s", statusCode, body)
		}
	}
	t.Fatalf("queue batch did not deliver all messages within 20s: seen=%v", seen)
}

// TestE2E_NormalPath_QueueFailureExhaustsIntoDeadLetter catches a dangerous
// durable-worker regression where transient guest failures retry forever or
// disappear instead of reaching the plan-bounded dead-letter surface.
func TestE2E_NormalPath_QueueFailureExhaustsIntoDeadLetter(t *testing.T) {
	f := newNormalPathFixture(t, "normal-queue-dead-letter")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "queue-dead-letter")
	f.vmmd.SetVersion(instance.ID, "queue-dead-letter")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:queue-dead-letter\n", 10*time.Second)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status:  http.StatusServiceUnavailable,
		Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/json"}},
		Body:    []byte(`{"error":"queue_guest_down"}`),
	})

	payload := json.RawMessage(`{"dead_letter":true}`)
	body, statusCode := doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/normal-queue-dead-letter/queues/send", api.QueueSendRequest{Payload: payload})
	if statusCode != http.StatusCreated {
		t.Fatalf("POST /queues/send: status=%d body=%s", statusCode, body)
	}
	var sent api.QueueSendResponse
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decode queue send response: %v body=%s", err, body)
	}

	budget := api.MustLimitsFor(api.PlanHobby).MaxQueueAttempts
	before := f.vmmd.ForwardCount()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		body, statusCode = doReq(t, f.h, f.key, http.MethodGet,
			"/v1/apps/normal-queue-dead-letter/queues/dead_letter", nil)
		if statusCode != http.StatusOK {
			t.Fatalf("GET /queues/dead_letter: status=%d body=%s", statusCode, body)
		}
		var response api.QueueDeadLetterResponse
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatalf("decode dead-letter response: %v body=%s", err, body)
		}
		for _, message := range response.Messages {
			if message.ID != sent.ID {
				continue
			}
			if message.Attempts != budget {
				t.Fatalf("dead-letter attempts=%d, want plan budget %d", message.Attempts, budget)
			}
			if !normalPathJSONEqual([]byte(message.Payload), payload) {
				t.Fatalf("dead-letter payload=%s, want %s", message.Payload, payload)
			}
			if !strings.Contains(message.LastError, "503") {
				t.Fatalf("dead-letter last_error=%q, want HTTP 503 context", message.LastError)
			}
			if got := f.vmmd.ForwardCount() - before; got < budget {
				t.Fatalf("bridge attempts=%d, want at least %d", got, budget)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("queue row %q did not reach dead-letter within 25s", sent.ID)
}

// TestE2E_NormalPath_DelayedTaskWaitsThenUsesRealGatewayBridge proves that a
// delayed row is not merely accepted by apid: the scheduler must honor its
// due time, dispatch it through gatewayd-internal, and persist the guest result
// where both the delayed-task and invocation APIs can observe it.
func TestE2E_NormalPath_DelayedTaskWaitsThenUsesRealGatewayBridge(t *testing.T) {
	f := newNormalPathFixture(t, "normal-delayed")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "delayed")
	f.vmmd.SetVersion(instance.ID, "delayed")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:delayed\n", 10*time.Second)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status:  http.StatusOK,
		Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/json"}},
		Body:    []byte(`{"delayed":"ok"}`),
	})

	scheduledAt := time.Now().UTC().Add(5 * time.Second)
	payload := json.RawMessage(`{"scheduled":true}`)
	body, statusCode := doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/normal-delayed/delayed-tasks", api.DelayedTaskRequest{
			Payload:     payload,
			ScheduledAt: scheduledAt,
		})
	if statusCode != http.StatusCreated {
		t.Fatalf("POST /delayed-tasks: status=%d body=%s", statusCode, body)
	}
	var created api.DelayedTaskResponse
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode delayed task response: %v body=%s", err, body)
	}
	if created.ID == "" || created.State != "" {
		t.Fatalf("delayed task response=%+v, want id and no terminal state", created)
	}
	if created.ScheduledAt.Before(scheduledAt.Add(-time.Second)) || created.ScheduledAt.After(scheduledAt.Add(time.Second)) {
		t.Fatalf("scheduled_at=%v, want near %v", created.ScheduledAt, scheduledAt)
	}

	body, statusCode = doReq(t, f.h, f.key, http.MethodGet,
		"/v1/delayed-tasks/"+created.ID, nil)
	if statusCode != http.StatusOK {
		t.Fatalf("GET /v1/delayed-tasks/%s: status=%d body=%s", created.ID, statusCode, body)
	}
	var pending api.DelayedTaskResponse
	if err := json.Unmarshal(body, &pending); err != nil {
		t.Fatalf("decode pending delayed task: %v body=%s", err, body)
	}
	if pending.State != string(state.InvocationPending) {
		t.Fatalf("delayed task state immediately after create=%q, want pending", pending.State)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		body, statusCode = doReq(t, f.h, f.key, http.MethodGet,
			"/v1/delayed-tasks/"+created.ID, nil)
		if statusCode != http.StatusOK {
			t.Fatalf("poll delayed task: status=%d body=%s", statusCode, body)
		}
		var current api.DelayedTaskResponse
		if err := json.Unmarshal(body, &current); err != nil {
			t.Fatalf("decode delayed task poll: %v body=%s", err, body)
		}
		if current.State == string(state.InvocationCompleted) {
			body, statusCode = doReq(t, f.h, f.key, http.MethodGet,
				"/v1/invocations/"+created.ID, nil)
			if statusCode != http.StatusOK {
				t.Fatalf("GET /v1/invocations/%s: status=%d body=%s", created.ID, statusCode, body)
			}
			var inv api.Invocation
			if err := json.Unmarshal(body, &inv); err != nil {
				t.Fatalf("decode delayed invocation: %v body=%s", err, body)
			}
			if inv.Source != string(state.InvocationDelayedTask) || string(inv.Payload) != string(payload) {
				t.Fatalf("delayed invocation source/payload=(%q,%s), want (%q,%s)", inv.Source, inv.Payload, state.InvocationDelayedTask, payload)
			}
			if string(inv.Result) != `{"delayed":"ok"}` {
				t.Fatalf("delayed invocation result=%s, want guest JSON", inv.Result)
			}
			request := f.vmmd.LastRequest()
			if request == nil || request.Instance != instance.ID || request.Method != http.MethodPost || request.RequestUri != "/" {
				t.Fatalf("delayed bridge request = %#v, want target POST /", request)
			}
			if !normalPathJSONEqual(f.vmmd.LastBody(), payload) {
				t.Fatalf("delayed forwarded body=%q, want %q", f.vmmd.LastBody(), payload)
			}
			return
		}
		// The scheduler claims due work by moving it through dispatching before
		// the gateway result is persisted as completed. Both states are valid
		// while this poll is in flight.
		if current.State != string(state.InvocationPending) && current.State != string(state.InvocationDispatching) {
			t.Fatalf("delayed task terminal state=%q, want completed", current.State)
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("delayed task %q did not complete within 15s", created.ID)
}

// TestE2E_NormalPath_CancelledDelayedTaskNeverReachesBridge protects the
// cancellation race boundary. A task cancelled while still pending must stay
// terminally cancelled and must not wake or invoke an instance later.
func TestE2E_NormalPath_CancelledDelayedTaskNeverReachesBridge(t *testing.T) {
	f := newNormalPathFixture(t, "normal-delayed-cancel")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "cancel")
	f.vmmd.SetVersion(instance.ID, "cancel")

	body, statusCode := doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/normal-delayed-cancel/delayed-tasks", api.DelayedTaskRequest{
			Payload:     json.RawMessage(`{"cancel":true}`),
			ScheduledAt: time.Now().UTC().Add(30 * time.Second),
		})
	if statusCode != http.StatusCreated {
		t.Fatalf("POST /delayed-tasks: status=%d body=%s", statusCode, body)
	}
	var created api.DelayedTaskResponse
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode delayed task response: %v body=%s", err, body)
	}

	body, statusCode = doReq(t, f.h, f.key, http.MethodDelete,
		"/v1/delayed-tasks/"+created.ID, nil)
	if statusCode != http.StatusOK {
		t.Fatalf("DELETE /v1/delayed-tasks/%s: status=%d body=%s", created.ID, statusCode, body)
	}
	var cancelled api.DelayedTaskResponse
	if err := json.Unmarshal(body, &cancelled); err != nil {
		t.Fatalf("decode cancelled delayed task: %v body=%s", err, body)
	}
	if cancelled.ID != created.ID || cancelled.State != string(state.InvocationCancelled) {
		t.Fatalf("cancel response=%+v, want cancelled %q", cancelled, created.ID)
	}

	time.Sleep(2 * time.Second)
	body, statusCode = doReq(t, f.h, f.key, http.MethodGet,
		"/v1/delayed-tasks/"+created.ID, nil)
	if statusCode != http.StatusOK {
		t.Fatalf("GET cancelled delayed task: status=%d body=%s", statusCode, body)
	}
	if err := json.Unmarshal(body, &cancelled); err != nil {
		t.Fatalf("decode persisted cancellation: %v body=%s", err, body)
	}
	if cancelled.State != string(state.InvocationCancelled) {
		t.Fatalf("persisted delayed task state=%q, want cancelled", cancelled.State)
	}
	if got := f.vmmd.ForwardCount(); got != 0 {
		t.Fatalf("cancelled task reached VMMD %d times, want 0", got)
	}
}

// TestE2E_NormalPath_ProxyActivityBecomesDurable catches a production-only
// failure mode where requests succeed but gatewayd's batched activity flush
// never reaches schedd. The durable fields drive idle reaping, metering, and
// warm-snapshot promotion, so a bridge smoke that checks only the response can
// still miss a serious instance-lifecycle regression.
func TestE2E_NormalPath_ProxyActivityBecomesDurable(t *testing.T) {
	f := newNormalPathFixture(t, "normal-activity")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "activity")
	f.vmmd.SetVersion(instance.ID, "activity")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:activity\n", 10*time.Second)
	before, err := f.store.InstanceByID(f.ctx, instance.ID)
	if err != nil {
		t.Fatalf("read pre-burst activity instance: %v", err)
	}
	for i := 0; i < 3; i++ {
		_, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, "/burst", nil)
		if statusCode != http.StatusOK || string(body) != "normal-path:activity\n" {
			t.Fatalf("burst request %d: status=%d body=%q", i, statusCode, body)
		}
	}
	wantCount := before.RequestCount + 3

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got, err := f.store.InstanceByID(f.ctx, instance.ID)
		if err != nil {
			t.Fatalf("read activity instance: %v", err)
		}
		if got.RequestCount >= wantCount && !got.LastRequestAt.IsZero() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	got, err := f.store.InstanceByID(f.ctx, instance.ID)
	if err != nil {
		t.Fatalf("read final activity instance: %v", err)
	}
	t.Fatalf("durable activity = request_count=%d last_request_at=%v; want request_count >= %d and a timestamp", got.RequestCount, got.LastRequestAt, wantCount)
}

// TestE2E_NormalPath_GuestFailureDoesNotRefreshActivity protects the idle
// reaper contract at the process boundary. A guest 4xx is customer-code
// traffic, not proof that the instance is healthy, so it must not increment
// request_count or move last_request_at forward.
func TestE2E_NormalPath_GuestFailureDoesNotRefreshActivity(t *testing.T) {
	f := newNormalPathFixture(t, "normal-activity-failure")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "activity-failure")
	f.vmmd.SetVersion(instance.ID, "activity-failure")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:activity-failure\n", 10*time.Second)

	deadline := time.Now().Add(6 * time.Second)
	var before state.Instance
	for time.Now().Before(deadline) {
		var err error
		before, err = f.store.InstanceByID(f.ctx, instance.ID)
		if err != nil {
			t.Fatalf("read baseline activity: %v", err)
		}
		if before.RequestCount > instance.RequestCount && !before.LastRequestAt.IsZero() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if before.RequestCount == instance.RequestCount || before.LastRequestAt.IsZero() {
		t.Fatalf("baseline activity did not flush: request_count=%d last_request_at=%v", before.RequestCount, before.LastRequestAt)
	}

	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status:  http.StatusTooManyRequests,
		Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "text/plain"}},
		Body:    []byte("guest-throttled\n"),
	})
	_, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, "/throttled", nil)
	if statusCode != http.StatusTooManyRequests || string(body) != "guest-throttled\n" {
		t.Fatalf("guest failure response: status=%d body=%q", statusCode, body)
	}

	// Wait beyond one gateway flush tick. If the error path touches the sink,
	// the mistaken increment/last-seen update becomes durable here.
	time.Sleep(3 * time.Second)
	after, err := f.store.InstanceByID(f.ctx, instance.ID)
	if err != nil {
		t.Fatalf("read post-failure activity: %v", err)
	}
	if after.RequestCount != before.RequestCount || !after.LastRequestAt.Equal(before.LastRequestAt) {
		t.Fatalf("guest failure refreshed activity: before=(count=%d,last=%v) after=(count=%d,last=%v)", before.RequestCount, before.LastRequestAt, after.RequestCount, after.LastRequestAt)
	}
}

// TestE2E_NormalPath_AsyncInvokeRetriesTransientBridgeFailure pins the
// scheduler/gateway boundary for a failure that occurs after the invocation
// has been claimed. A VMMD Unavailable must leave the durable row retryable;
// the next dispatch must use the same live target and complete it.
func TestE2E_NormalPath_AsyncInvokeRetriesTransientBridgeFailure(t *testing.T) {
	f := newNormalPathFixture(t, "normal-async-retry")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "retry")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:retry\n", 10*time.Second)
	f.vmmd.FailNext(instance.ID, status.Error(codes.Unavailable, "simulated async bridge outage"))

	body, statusCode := doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/normal-async-retry/invoke/async", api.InvokeRequest{
			Payload: json.RawMessage(`{"retry":true}`),
		})
	if statusCode != http.StatusAccepted {
		t.Fatalf("POST /invoke/async: status=%d body=%s", statusCode, body)
	}
	var accepted api.AsyncInvokeResponse
	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatalf("decode async response: %v body=%s", err, body)
	}

	inv := pollUntilCompleted(t, f.h, f.key, accepted.ID, 15*time.Second)
	if inv.State != string(state.InvocationCompleted) {
		t.Fatalf("retried invocation state=%q last_error=%q", inv.State, inv.LastError)
	}
	if inv.Attempts < 2 {
		t.Fatalf("attempts=%d, want at least 2 after transient bridge failure", inv.Attempts)
	}
	if inv.InstanceID != instance.ID {
		t.Fatalf("retried invocation instance_id=%q, want %q", inv.InstanceID, instance.ID)
	}
}

// TestE2E_NormalPath_AsyncInvokeRetriesGuestServerError distinguishes an
// application/guest 503 from a permanent guest 4xx. The first 503 must remain
// retryable at the durable scheduler boundary; the next response completes
// the same invocation and preserves its final result.
func TestE2E_NormalPath_AsyncInvokeRetriesGuestServerError(t *testing.T) {
	f := newNormalPathFixture(t, "normal-async-guest-retry")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "guest-retry")
	f.vmmd.SetVersion(instance.ID, "guest-retry")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:guest-retry\n", 10*time.Second)
	f.vmmd.SetResponseSequence(instance.ID, []e2etest.FakeResponse{
		{
			Status:  http.StatusServiceUnavailable,
			Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/json"}},
			Body:    []byte(`{"error":"guest temporarily unavailable"}`),
		},
		{
			Status:  http.StatusOK,
			Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/json"}},
			Body:    []byte(`{"recovered":true}`),
		},
	})

	body, statusCode := doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/normal-async-guest-retry/invoke/async", api.InvokeRequest{
			Payload: json.RawMessage(`{"retry_guest":true}`),
		})
	if statusCode != http.StatusAccepted {
		t.Fatalf("POST /invoke/async: status=%d body=%s", statusCode, body)
	}
	var accepted api.AsyncInvokeResponse
	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatalf("decode async response: %v body=%s", err, body)
	}

	inv := pollUntilCompleted(t, f.h, f.key, accepted.ID, 15*time.Second)
	if inv.State != string(state.InvocationCompleted) {
		t.Fatalf("guest-retry invocation state=%q last_error=%q, want completed", inv.State, inv.LastError)
	}
	if inv.Attempts < 2 {
		t.Fatalf("attempts=%d, want at least 2 after guest 503", inv.Attempts)
	}
	if string(inv.Result) != `{"recovered":true}` {
		t.Fatalf("guest-retry result=%s, want recovered JSON", inv.Result)
	}
}

// TestE2E_NormalPath_GuestStatusAndHeadersPassThrough catches a common
// production-only mismatch: a fake/real guest response may be a valid
// non-2xx response, not a gateway failure. The status, retry hint, custom
// header, and body must cross the streaming bridge unchanged.
func TestE2E_NormalPath_GuestStatusAndHeadersPassThrough(t *testing.T) {
	f := newNormalPathFixture(t, "normal-status")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "status")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:status\n", 10*time.Second)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status: http.StatusTooManyRequests,
		Headers: []*vmmdpb.Header{
			{Name: "Content-Type", Value: "text/plain"},
			{Name: "Retry-After", Value: "7"},
			{Name: "X-Guest-Response", Value: "throttled"},
		},
		Body: []byte("guest-throttled\n"),
	})

	headers, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, "/", nil)
	if statusCode != http.StatusTooManyRequests {
		t.Fatalf("guest response status=%d body=%q, want 429", statusCode, body)
	}
	if string(body) != "guest-throttled\n" {
		t.Fatalf("guest response body=%q, want guest body", body)
	}
	if headers.Get("Retry-After") != "7" {
		t.Fatalf("Retry-After=%q, want 7", headers.Get("Retry-After"))
	}
	if headers.Get("X-Guest-Response") != "throttled" {
		t.Fatalf("X-Guest-Response=%q, want throttled", headers.Get("X-Guest-Response"))
	}
}

// TestE2E_NormalPath_GuestServerErrorPassesThrough proves that a guest HTTP
// 500 is not rewritten as the gateway's own upstream-unavailable response.
// Operators and customers need the distinction for debugging and retry policy.
func TestE2E_NormalPath_GuestServerErrorPassesThrough(t *testing.T) {
	f := newNormalPathFixture(t, "normal-guest-500")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "guest-500")
	f.vmmd.SetVersion(instance.ID, "guest-500")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:guest-500\n", 10*time.Second)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status: http.StatusInternalServerError,
		Headers: []*vmmdpb.Header{
			{Name: "Content-Type", Value: "application/json"},
			{Name: "X-Guest-Error", Value: "panic"},
		},
		Body: []byte(`{"error":"guest_failure"}`),
	})

	headers, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, "/", nil)
	if statusCode != http.StatusInternalServerError {
		t.Fatalf("guest 500 status=%d body=%q, want 500", statusCode, body)
	}
	if string(body) != `{"error":"guest_failure"}` {
		t.Fatalf("guest 500 body=%q, want guest body", body)
	}
	if headers.Get("X-Guest-Error") != "panic" {
		t.Fatalf("X-Guest-Error=%q, want panic", headers.Get("X-Guest-Error"))
	}
}

// TestE2E_NormalPath_BridgeUnavailableSurfaces503 pins the customer-facing
// failure boundary. A VMMD Unavailable must be surfaced as an upstream 503,
// not rewritten as a guest response or an unrelated routing error.
func TestE2E_NormalPath_BridgeUnavailableSurfaces503(t *testing.T) {
	f := newNormalPathFixture(t, "normal-recovery")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "v1")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:v1\n", 10*time.Second)
	f.vmmd.FailNext(instance.ID, status.Error(codes.Unavailable, "simulated vmmd outage"))

	_, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, "/", nil)
	if statusCode != http.StatusServiceUnavailable {
		t.Fatalf("outage response status=%d body=%q, want 503", statusCode, body)
	}
	if !strings.Contains(string(body), "upstream unavailable") {
		t.Fatalf("outage response body=%q, want upstream unavailable", body)
	}
	// Automatic replacement after stale-target eviction needs a deployable
	// rootfs artifact. This KVM-free fixture intentionally owns the transport
	// boundary only; native acceptance covers artifact-backed replacement.
}

// TestE2E_NormalPath_StoppedInstanceInvalidatesRoute catches stale-cache
// regressions where a route continues serving a stopped instance after the
// durable lifecycle state has changed.
func TestE2E_NormalPath_StoppedInstanceInvalidatesRoute(t *testing.T) {
	f := newNormalPathFixture(t, "normal-stop-invalidation")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "v1")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:v1\n", 10*time.Second)
	if err := f.store.UpdateInstanceState(f.ctx, instance.ID, string(state.StateStopped)); err != nil {
		t.Fatalf("stop instance: %v", err)
	}
	notifyNormalPathInstanceChanged(t, f, instance.ID, string(state.StateStopped))

	deadline := time.Now().Add(10 * time.Second)
	lastStatus := 0
	var lastBody []byte
	for time.Now().Before(deadline) {
		_, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet, "/after-stop", nil)
		lastBody, lastStatus = body, statusCode
		if statusCode != http.StatusOK {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("stopped instance remained routable; last status=%d body=%q", lastStatus, lastBody)
}

// TestE2E_NormalPath_GatewayRestartReloadsDurableRoute stops the real
// gateway process after a successful request, starts a fresh gateway process,
// and proves it can route from Postgres without relying on an in-memory cache.
func TestE2E_NormalPath_GatewayRestartReloadsDurableRoute(t *testing.T) {
	f := newNormalPathFixture(t, "normal-restart")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "v1")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:v1\n", 10*time.Second)

	// Keep the fake bridge alive. The fresh gateway and schedd pair must
	// rediscover the durable compute-node target and route without the old
	// process-local cache.
	f.h.Stop()
	h2 := e2etest.Start(t, f.h.Pool, e2etest.Schedd|e2etest.Gatewayd)
	waitForNormalPathResponse(t, h2, f.host, "normal-path:v1\n", 10*time.Second)
	if request := f.vmmd.LastRequest(); request == nil || request.Instance != instance.ID {
		t.Fatalf("post-restart request instance=%q, want %q", requestInstance(request), instance.ID)
	}
}

// TestE2E_NormalPath_GatewayRestartTerminatesInFlightResponse protects the
// process-lifecycle boundary for ordinary HTTP traffic. A gateway restart
// must not leave a client or the VMMD stream blocked forever, and the fresh
// gateway must still route the same durable instance immediately afterwards.
func TestE2E_NormalPath_GatewayRestartTerminatesInFlightResponse(t *testing.T) {
	f := newNormalPathFixture(t, "normal-restart-stream")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "restart-stream")
	f.vmmd.SetVersion(instance.ID, "restart-stream")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:restart-stream\n", 10*time.Second)

	probe := f.vmmd.InstallCancellationProbe(instance.ID, false, true)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status:  http.StatusOK,
		Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "text/plain"}},
		Chunks:  [][]byte{[]byte("partial-response\n"), []byte("must-not-arrive\n")},
	})

	requestCtx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, f.h.EdgeURL()+"/restart-stream", nil)
	if err != nil {
		t.Fatalf("new restart stream request: %v", err)
	}
	req.Host = f.host
	client := *f.h.HTTPClient()
	type streamOutcome struct {
		err    error
		status int
		body   string
	}
	requestDone := make(chan streamOutcome, 1)
	go func() {
		resp, err := client.Do(req)
		if err != nil {
			requestDone <- streamOutcome{err: err}
			return
		}
		body, bodyErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		requestDone <- streamOutcome{err: bodyErr, status: resp.StatusCode, body: string(body)}
	}()

	waitNormalPathProbe(t, probe.FirstResponseBody(), "restart first response body")

	// Stop every real daemon while the response is blocked. The fake VMMD
	// remains alive so the test can distinguish a gateway lifecycle failure
	// from a bridge outage.
	f.h.Stop()
	waitNormalPathProbe(t, probe.Canceled(), "restart bridge cancellation")
	select {
	case outcome := <-requestDone:
		// The old response must not terminate as a SUCCESS once its owning
		// gateway exits. What "terminate" looks like depends on the topology,
		// and both shapes are correct:
		//
		//   - direct to gatewayd-internal: the listener dies under the open
		//     connection, so the client gets a transport error;
		//   - through gatewayd-public: the public hop outlives the internal
		//     one and turns the failed round-trip into a 502, which is
		//     strictly better — the customer gets a status instead of a
		//     severed socket.
		//
		// The failure that actually matters is neither of those: a 2xx whose
		// body is the partial one the fake sent before blocking. That is a
		// truncated response presented as a complete one, which no client can
		// detect. Assert on that, not on the transport artifact.
		if outcome.err == nil && outcome.status/100 == 2 {
			t.Fatalf("in-flight response completed as a SUCCESS after gateway restart: status=%d body=%q (a truncated body under a 2xx is indistinguishable from a complete response)",
				outcome.status, outcome.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight response remained blocked after gateway restart")
	}

	f.vmmd.ReleaseProbe(probe)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status: http.StatusOK,
		Body:   []byte("restart-recovered\n"),
	})
	h2 := e2etest.Start(t, f.h.Pool, e2etest.Schedd|e2etest.Gatewayd)
	_, body, statusCode := doReqHeaders(t, h2, f.host, http.MethodGet, "/after-restart", nil)
	if statusCode != http.StatusOK || string(body) != "restart-recovered\n" {
		t.Fatalf("post-restart response: status=%d body=%q, want 200/restart-recovered", statusCode, body)
	}
	if request := f.vmmd.LastRequest(); request == nil || request.Instance != instance.ID {
		t.Fatalf("post-restart request instance=%q, want %q", requestInstance(request), instance.ID)
	}
}

// TestE2E_NormalPath_ScheddRestartReclaimsAbandonedDispatch protects the
// durable-worker recovery contract. If schedd disappears after claiming an
// invocation but before completion, a replacement schedd must reclaim the
// expired lease and complete the original row instead of losing or duplicating
// the customer's work.
func TestE2E_NormalPath_ScheddRestartReclaimsAbandonedDispatch(t *testing.T) {
	f := newNormalPathFixture(t, "normal-schedd-recovery")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "schedd-recovery")
	f.vmmd.SetVersion(instance.ID, "schedd-recovery")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:schedd-recovery\n", 10*time.Second)

	probe := f.vmmd.InstallCancellationProbe(instance.ID, false, true)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status:  http.StatusOK,
		Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/json"}},
		Body:    []byte(`{"recovered":true}`),
	})

	body, statusCode := doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/normal-schedd-recovery/invoke/async", api.InvokeRequest{
			Payload: json.RawMessage(`{"restart":true}`),
		})
	if statusCode != http.StatusAccepted {
		t.Fatalf("POST /invoke/async: status=%d body=%s", statusCode, body)
	}
	var accepted api.AsyncInvokeResponse
	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatalf("decode async response: %v body=%s", err, body)
	}

	waitNormalPathProbe(t, probe.FirstResponseBody(), "abandoned dispatch response")
	dispatching := waitForNormalPathInvocationState(t, f.store, accepted.ID, state.InvocationDispatching, 5*time.Second)
	if dispatching.Attempts != 1 || dispatching.LeaseExpiresAt == nil {
		t.Fatalf("in-flight invocation=(attempts=%d,lease=%v), want first leased dispatch", dispatching.Attempts, dispatching.LeaseExpiresAt)
	}

	f.h.Stop()
	waitNormalPathProbe(t, probe.Canceled(), "abandoned dispatch cancellation")
	if _, err := f.h.Pool.Exec(f.ctx, `
		update invocations
		   set lease_expires_at = now() - interval '1 second'
		 where id = $1 and state = 'dispatching'`, accepted.ID); err != nil {
		t.Fatalf("expire abandoned invocation lease: %v", err)
	}
	f.vmmd.ReleaseProbe(probe)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status:  http.StatusOK,
		Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/json"}},
		Body:    []byte(`{"recovered":true}`),
	})

	h2 := e2etest.Start(t, f.h.Pool, e2etest.APID|e2etest.Schedd|e2etest.Gatewayd)
	inv := pollUntilCompleted(t, h2, f.key, accepted.ID, 15*time.Second)
	if inv.State != string(state.InvocationCompleted) {
		t.Fatalf("recovered invocation state=%q last_error=%q, want completed", inv.State, inv.LastError)
	}
	if inv.Attempts != 2 {
		t.Fatalf("recovered invocation attempts=%d, want exactly 2", inv.Attempts)
	}
	if string(inv.Result) != `{"recovered":true}` {
		t.Fatalf("recovered invocation result=%s, want recovered JSON", inv.Result)
	}
	if got := f.vmmd.ForwardCount(); got < 2 {
		t.Fatalf("bridge forward count=%d, want initial abandoned attempt plus recovery", got)
	}
}

func notifyNormalPathDeploymentChanged(t *testing.T, f *normalPathFixture, deploymentID string) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{
		"app_id":        f.app.ID,
		"deployment_id": deploymentID,
		"status":        string(state.DeployLive),
	})
	if err != nil {
		t.Fatalf("encode deployment_changed payload: %v", err)
	}
	if err := db.Notify(f.ctx, f.h.Pool, db.NotifyDeploymentChanged, string(payload)); err != nil {
		t.Fatalf("notify deployment_changed: %v", err)
	}
}

func notifyNormalPathInstanceChanged(t *testing.T, f *normalPathFixture, instanceID, instanceState string) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{
		"app_id":      f.app.ID,
		"instance_id": instanceID,
		"state":       instanceState,
	})
	if err != nil {
		t.Fatalf("encode instance_changed payload: %v", err)
	}
	if err := db.Notify(f.ctx, f.h.Pool, db.NotifyInstanceChanged, string(payload)); err != nil {
		t.Fatalf("notify instance_changed: %v", err)
	}
}

// publishNormalPathLayer gives a deployment the readable, signed rootfs layer
// that every wake verifies before it boots. Without it a wake fails the layer
// check, the deployment is marked unavailable, and later requests 404 with
// "no live deployment to wake" — a symptom well removed from the cause.
func publishNormalPathLayer(t *testing.T, f *normalPathFixture, deploymentID string) {
	t.Helper()
	if f.artifacts == nil {
		t.Fatal("normal-path fixture has no artifact store")
	}
	layerKey := "layers/" + deploymentID + ".ext4"
	layer := []byte("gregale normal-path fixture rootfs layer\n")
	if err := f.artifacts.Put(f.ctx, layerKey, bytes.NewReader(layer)); err != nil {
		t.Fatalf("publish layer: %v", err)
	}
	// schedd verifies the signature too, so an unsigned layer fails the same
	// way a missing one does.
	signer, err := cosign.NewLocalSigner(f.h.SignKeyPath, f.artifacts, nil)
	if err != nil {
		t.Fatalf("create artifact signer: %v", err)
	}
	if err := signer.Sign(f.ctx, layerKey, cosign.SigKeyFor(layerKey)); err != nil {
		t.Fatalf("sign layer: %v", err)
	}
	if err := f.store.SetDeploymentRootfs(f.ctx, deploymentID, layerKey, layerKey, int64(len(layer))); err != nil {
		t.Fatalf("publish rootfs metadata: %v", err)
	}
}

func createNormalPathLiveDeployment(t *testing.T, f *normalPathFixture, appID, version string) (state.Deployment, state.Instance) {
	t.Helper()
	digestByte := "1"
	if version == "v2" {
		digestByte = "2"
	}
	dep, err := f.store.CreateDeployment(f.ctx, state.Deployment{
		AppID:       appID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat(digestByte, 64),
	})
	if err != nil {
		t.Fatalf("create %s deployment: %v", version, err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, dep.ID); err != nil {
		t.Fatalf("mark %s deployment live: %v", version, err)
	}
	publishNormalPathLayer(t, f, dep.ID)
	instance, err := f.store.CreateInstance(f.ctx, appID, dep.ID, string(state.StateRunning),
		e2etest.FakeSnapshotRAMMB, f.nodeID, "")
	if err != nil {
		t.Fatalf("create %s instance: %v", version, err)
	}
	return dep, instance
}

func normalPathJSONEqual(got, want []byte) bool {
	var gotCompact, wantCompact bytes.Buffer
	if err := json.Compact(&gotCompact, got); err != nil {
		return false
	}
	if err := json.Compact(&wantCompact, want); err != nil {
		return false
	}
	return bytes.Equal(gotCompact.Bytes(), wantCompact.Bytes())
}

func waitForNormalPathResponse(t *testing.T, h *e2etest.Harness, host, want string, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastBody []byte
	var lastStatus int
	for time.Now().Before(deadline) {
		_, body, status := doReqHeaders(t, h, host, http.MethodGet, "/", nil)
		lastBody, lastStatus = body, status
		if status == http.StatusOK && string(body) == want {
			return body
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("GET %s/ did not return %q within %s; last status=%d body=%q", host, want, timeout, lastStatus, lastBody)
	return nil
}

func waitForNormalPathInvocationState(t *testing.T, store *state.PgStore, id string, want state.InvocationState, timeout time.Duration) state.Invocation {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last state.Invocation
	for time.Now().Before(deadline) {
		inv, err := store.InvocationByID(context.Background(), id)
		if err != nil {
			t.Fatalf("read invocation %s: %v", id, err)
		}
		last = inv
		if inv.State == want {
			return inv
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("invocation %s state=%q, want %q within %s", id, last.State, want, timeout)
	return last
}

func hasNormalPathHeader(request *vmmdpb.ForwardHTTPRequestInit, name, value string) bool {
	for _, header := range request.GetHeaders() {
		if strings.EqualFold(header.GetName(), name) && header.GetValue() == value {
			return true
		}
	}
	return false
}

func hasNormalPathHeaderName(request *vmmdpb.ForwardHTTPRequestInit, name string) bool {
	for _, header := range request.GetHeaders() {
		if strings.EqualFold(header.GetName(), name) {
			return true
		}
	}
	return false
}

func requestInstance(request *vmmdpb.ForwardHTTPRequestInit) string {
	if request == nil {
		return "<nil>"
	}
	return request.Instance
}

func requestMethod(request *vmmdpb.ForwardHTTPRequestInit) string {
	if request == nil {
		return "<nil>"
	}
	return request.Method
}
