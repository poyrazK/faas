// normal_path_debugger_e2e_test.go — KVM-free debugger acceptance.
//
// The debugger's existing tests mostly seed request_telemetry rows directly
// into PgStore. This test keeps the real process boundary in the loop:
// gatewayd-internal records a customer request, publishes it to apid over
// the request-telemetry Unix socket, and the customer debugger reads the
// resulting row back through the HTTP API and route analytics. It also
// exercises the metadata-only replay through schedd and the real
// gatewayd-internal bridge.

//go:build !no_pg

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/trace"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func newNormalPathDebuggerFixture(t *testing.T, slug string) *normalPathFixture {
	return newNormalPathDebuggerFixtureWithTelemetry(t, slug, true)
}

func newNormalPathDebuggerFixtureWithTelemetry(t *testing.T, slug string, telemetryEnabled bool) *normalPathFixture {
	t.Helper()
	telemetryDir, err := os.MkdirTemp("", "faas-e2e-request-telemetry-*")
	if err != nil {
		t.Fatalf("create request telemetry socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(telemetryDir) })
	telemetrySocket := filepath.Join(telemetryDir, "request-telemetry.sock")
	spansWriterSocket := filepath.Join(telemetryDir, "otel-spans-writer.sock")
	artifactDir, err := os.MkdirTemp("", "faas-e2e-debugger-artifacts-*")
	if err != nil {
		t.Fatalf("create debugger artifact dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(artifactDir) })
	artifacts, err := storage.NewLocalStorageBackend(artifactDir)
	if err != nil {
		t.Fatalf("create debugger artifact store: %v", err)
	}
	telemetrySetting := "false"
	if telemetryEnabled {
		telemetrySetting = "true"
	}
	f := newNormalPathFixtureWithPlanAndEnv(t, slug, api.PlanPro,
		"FAAS_APP_ERRORS_ENABLED=false",
		"FAAS_OTEL_SPANS_WRITER_ENABLED=true",
		"FAAS_REQUEST_TELEMETRY_ENABLED="+telemetrySetting,
		"FAAS_APID_REQUEST_TELEMETRY_SOCKET="+telemetrySocket,
		"FAAS_APID_OTEL_SPANS_WRITER_SOCKET="+spansWriterSocket,
		"FAAS_OTEL_FLUSH_INTERVAL=50ms",
		"FAAS_STORAGE_BACKEND=local",
		"FAAS_STORAGE_ROOT="+artifactDir,
	)
	if f != nil {
		f.artifacts = artifacts
		f.spansWriterSocket = spansWriterSocket
	}
	return f
}

// TestE2E_NormalPath_DebuggerTelemetryAnalyticsAndReplay pins the missing cross-
// process contract between gatewayd-internal's hot path and the debugger's
// customer-facing API. A successful request must survive the telemetry gRPC
// socket and Postgres boundary, appear in customer-facing route analytics,
// expose safe metadata on every read surface, and support one idempotent
// mirror-only replay without waking the source.
func TestE2E_NormalPath_DebuggerTelemetryAnalyticsAndReplay(t *testing.T) {
	f := newNormalPathDebuggerFixture(t, "normal-debugger")
	if f == nil {
		return
	}

	sourceDeployment, sourceInstance := createNormalPathLiveDeployment(t, f, f.app.ID, "debugger-source")
	f.vmmd.SetVersion(sourceInstance.ID, "debugger-source")
	waitForNormalPathDebuggerResponse(t, f, "normal-path:debugger-source\n", 10*time.Second)

	const secret = "customer-secret-must-not-cross-debugger"
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	requestHeaders, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet,
		"/debugger/telemetry", nil, map[string]string{
			"Authorization":     "Bearer " + f.key,
			"X-Customer-Secret": secret,
			"Traceparent":       traceparent,
		})
	if statusCode != http.StatusOK || string(body) != "normal-path:debugger-source\n" {
		t.Fatalf("debugger source request: status=%d body=%q", statusCode, body)
	}
	publicRequestID := requestHeaders.Get(api.RequestIDHeader)
	responseTraceID := requestHeaders.Get(api.TraceIDHeader)
	if publicRequestID == "" || responseTraceID == "" {
		t.Fatalf("origin response IDs: request=%q trace=%q; both response headers are required", publicRequestID, responseTraceID)
	}
	if publicRequestID == responseTraceID {
		t.Fatalf("public request ID and W3C trace ID unexpectedly share a value: %q", publicRequestID)
	}
	if responseTraceID != traceID {
		t.Fatalf("origin response trace ID=%q, want propagated W3C trace %q", responseTraceID, traceID)
	}

	request := waitForNormalPathDebuggerRequestByPublicID(t, f, publicRequestID, 20*time.Second)
	listedRequest := waitForNormalPathDebuggerRequestByTraceID(t, f, sourceDeployment.ID, traceID, 20*time.Second)
	if listedRequest.ID != request.ID {
		t.Fatalf("public-ID lookup row=%q, trace-list row=%q; want the same request", request.ID, listedRequest.ID)
	}
	if request.Status != http.StatusOK || request.Method != http.MethodGet {
		t.Fatalf("debugger request = %+v, want GET/200", request)
	}
	if request.InstanceID != sourceInstance.ID {
		t.Fatalf("debugger request instance_id=%q, want source %q", request.InstanceID, sourceInstance.ID)
	}
	if request.TraceID == nil || *request.TraceID != traceID {
		t.Fatalf("debugger request trace_id=%v, want propagated W3C trace %s", request.TraceID, traceID)
	}

	body, statusCode = doReq(t, f.h, f.key, http.MethodGet,
		"/v1/apps/normal-debugger/debug/requests/"+url.PathEscape(publicRequestID), nil)
	if statusCode != http.StatusOK {
		t.Fatalf("debugger request detail: status=%d body=%s", statusCode, body)
	}
	var detail api.DebugTelemetryRequestItem
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode debugger request detail: %v body=%s", err, body)
	}
	if detail.ID != request.ID || detail.RequestID != publicRequestID || detail.DeploymentID != sourceDeployment.ID {
		t.Fatalf("debugger request detail = %+v, want public request %s (row %s) on %s", detail, publicRequestID, request.ID, sourceDeployment.ID)
	}
	if detail.TraceID == nil || *detail.TraceID != traceID {
		t.Fatalf("debugger request trace_id = %v, want response %s", detail.TraceID, traceID)
	}

	// The public CLI flows must consume the customer-visible request ID, not
	// silently reinterpret it as the telemetry row UUID or W3C trace ID.
	gregale := buildGregale(t)
	stdout, stderr, exit := runGregaleAgainstHarness(t, gregale, f, "debug", "requests", "get", "normal-debugger", publicRequestID)
	if exit != 0 || !strings.Contains(stdout, "Public ID:  "+publicRequestID) || !strings.Contains(stdout, "Trace ID:   "+traceID) {
		t.Fatalf("gregale debug requests get: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	stdout, stderr, exit = runGregaleAgainstHarness(t, gregale, f, "logs", "--source", "http", "--request", publicRequestID, "normal-debugger")
	if exit != 0 || !strings.Contains(stdout, "request="+publicRequestID) || !strings.Contains(stdout, "trace="+traceID) || !strings.Contains(stdout, "status=200") {
		t.Fatalf("gregale logs by public request ID: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}

	body, statusCode = doReq(t, f.h, f.key, http.MethodGet,
		"/v1/apps/normal-debugger/debug/requests/"+request.ID+"/evidence", nil)
	if statusCode != http.StatusOK {
		t.Fatalf("debugger evidence: status=%d body=%s", statusCode, body)
	}
	var evidence api.DebugRequestEvidenceResponse
	if err := json.Unmarshal(body, &evidence); err != nil {
		t.Fatalf("decode debugger evidence: %v body=%s", err, body)
	}
	if evidence.Request.ID != request.ID || len(evidence.Correlation.Stages) == 0 {
		t.Fatalf("debugger evidence = %+v, want request and correlation stages", evidence)
	}
	if strings.Contains(string(body), secret) {
		t.Fatalf("debugger evidence leaked customer header value %q", secret)
	}

	body, statusCode = doReq(t, f.h, f.key, http.MethodGet,
		"/v1/apps/normal-debugger/debug/coverage?since=24h", nil)
	if statusCode != http.StatusOK {
		t.Fatalf("debugger coverage: status=%d body=%s", statusCode, body)
	}
	var coverage api.DebugCoverageResponse
	if err := json.Unmarshal(body, &coverage); err != nil {
		t.Fatalf("decode debugger coverage: %v body=%s", err, body)
	}
	if coverage.TelemetryRows < 1 || coverage.RepresentedRequests < 1 {
		t.Fatalf("debugger coverage = %+v, want at least one persisted request", coverage)
	}

	body, statusCode = doReq(t, f.h, f.key, http.MethodGet,
		"/v1/apps/normal-debugger/debug/requests/export?since=24h&format=ndjson&limit=20", nil)
	if statusCode != http.StatusOK {
		t.Fatalf("debugger export: status=%d body=%s", statusCode, body)
	}
	export := string(body)
	if !strings.Contains(export, request.DeploymentID) ||
		!strings.Contains(export, request.Route) ||
		!strings.Contains(export, `"method":"GET"`) {
		t.Fatalf("debugger export=%q, want persisted deployment %s route %q", body, request.DeploymentID, request.Route)
	}
	if strings.Contains(export, secret) {
		t.Fatalf("debugger export leaked customer header value %q", secret)
	}

	// Exercise a platform-owned dependency span on the same W3C trace as the
	// persisted app request. The service-mesh call emits the span; gatewayd's
	// retained-span accumulator flushes it over the spans-writer socket, and
	// apid correlates it back to this request row by trace ID and account.
	dependencyApp := createServiceApp(t, f, "analyticsdependency", nil)
	_, dependencyInstance := createNormalPathLiveDeployment(t, f, dependencyApp.ID, "dependency-v1")
	f.vmmd.SetVersion(dependencyInstance.ID, "dependency-v1")
	warmStatus, _, warmBody := pollServiceCall(t, f.h, f.app.ID, "analyticsdependency", "/health", http.StatusOK, 15*time.Second)
	if warmStatus != http.StatusOK {
		t.Fatalf("warm dependency service call: status=%d body=%s", warmStatus, warmBody)
	}
	dependencyStatus, _, dependencyBody := serviceCall(t, f.h, f.app.ID, "analyticsdependency", "/health",
		http.Header{"Traceparent": []string{traceparent}})
	if dependencyStatus != http.StatusOK {
		t.Fatalf("correlated dependency service call: status=%d body=%s", dependencyStatus, dependencyBody)
	}
	exerciseOutboundDependencySpan(t, f, traceparent)

	// The same gateway-recorded row that powers the debugger must feed the
	// route-centric analytics API after crossing the telemetry socket and
	// Postgres. Poll until the asynchronous retained-span writer has attached
	// the classified dependency evidence to the request row.
	var analytics api.RequestAnalyticsResponse
	serviceDependencyFound := false
	outboundDependencyFound := false
	dependencyRevisionFound := false
	routeDeploymentFound := false
	analyticsDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(analyticsDeadline) {
		body, statusCode = doReq(t, f.h, f.key, http.MethodGet,
			"/v1/apps/normal-debugger/analytics?since=24h", nil)
		if statusCode != http.StatusOK {
			t.Fatalf("request analytics: status=%d body=%s", statusCode, body)
		}
		if err := json.Unmarshal(body, &analytics); err != nil {
			t.Fatalf("decode request analytics: %v body=%s", err, body)
		}
		for _, route := range analytics.Routes {
			if route.Route != request.Route || route.Method != request.Method {
				continue
			}
			for _, observation := range route.DeploymentObservations {
				if observation.DeploymentID != sourceDeployment.ID {
					continue
				}
				if observation.Requests < 1 || observation.RequestSharePct <= 0 {
					t.Fatalf("route deployment observation = %+v, want positive request count and app share", observation)
				}
				routeDeploymentFound = true
			}
			for _, dependency := range route.Dependencies {
				if dependency.Type == "managed_binding" && dependency.Kind == "service_proxy" && dependency.Name == "service.analyticsdependency" && dependency.Samples >= 1 {
					serviceDependencyFound = true
				}
				if dependency.Type == "outbound_integration" && dependency.Kind == "https" && dependency.Name == "outbound.stripe" && dependency.Samples >= 1 {
					outboundDependencyFound = true
				}
				for _, observation := range dependency.DeploymentObservations {
					if observation.DeploymentID != sourceDeployment.ID {
						continue
					}
					if observation.Samples < 1 || observation.Calls < 1 || observation.P50MS > observation.P95MS || observation.P95MS > observation.P99MS {
						t.Fatalf("dependency deployment observation = %+v, want sampled metrics with ordered p50/p95/p99", observation)
					}
					dependencyRevisionFound = true
				}
			}
		}
		if serviceDependencyFound && outboundDependencyFound && dependencyRevisionFound && routeDeploymentFound {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !serviceDependencyFound || !outboundDependencyFound || !dependencyRevisionFound || !routeDeploymentFound {
		t.Fatalf("route analytics: service=%t outbound=%t dependency-deployment=%t route-deployment=%t routes=%+v", serviceDependencyFound, outboundDependencyFound, dependencyRevisionFound, routeDeploymentFound, analytics.Routes)
	}
	if analytics.Requests < 1 {
		t.Fatalf("request analytics = %+v, want at least one persisted request", analytics)
	}
	var analyticsRoute *api.RequestAnalyticsRoute
	for i := range analytics.Routes {
		if analytics.Routes[i].Route == request.Route && analytics.Routes[i].Method == request.Method {
			analyticsRoute = &analytics.Routes[i]
			break
		}
	}
	if analyticsRoute == nil {
		t.Fatalf("route analytics = %+v, want %s %s with at least one request", analytics.Routes, request.Method, request.Route)
	}
	if analyticsRoute.Requests < 1 || analyticsRoute.ErrorRequests != 0 || analyticsRoute.P95MS < analyticsRoute.P50MS || analyticsRoute.P99MS < analyticsRoute.P95MS {
		t.Fatalf("route analytics = %+v, want successful requests and ordered latency percentiles", *analyticsRoute)
	}
	if analytics.ComputeCost == nil || analytics.ComputeCost.RequestCount < 1 {
		t.Fatalf("route compute cost = %+v, want an estimate allocated over persisted requests", analytics.ComputeCost)
	}
	if analytics.DeploymentCosts == nil {
		t.Fatal("deployment cost breakdown is missing from route analytics")
	}
	var deploymentCost *api.RequestAnalyticsDeploymentCost
	for i := range analytics.DeploymentCosts.Deployments {
		if analytics.DeploymentCosts.Deployments[i].DeploymentID == sourceDeployment.ID {
			deploymentCost = &analytics.DeploymentCosts.Deployments[i]
			break
		}
	}
	if deploymentCost == nil || deploymentCost.Requests < 1 {
		t.Fatalf("deployment cost rows = %+v, want deployment %s with at least one request", analytics.DeploymentCosts.Deployments, sourceDeployment.ID)
	}

	timeseriesPath := "/v1/apps/normal-debugger/analytics/timeseries?since=24h&route=" +
		url.QueryEscape(request.Route) + "&method=" + url.QueryEscape(request.Method)
	body, statusCode = doReq(t, f.h, f.key, http.MethodGet, timeseriesPath, nil)
	if statusCode != http.StatusOK {
		t.Fatalf("route analytics timeseries: status=%d body=%s", statusCode, body)
	}
	var timeseries api.RequestAnalyticsTimeseriesResponse
	if err := json.Unmarshal(body, &timeseries); err != nil {
		t.Fatalf("decode route analytics timeseries: %v body=%s", err, body)
	}
	var timeseriesRequests int64
	for _, point := range timeseries.Points {
		timeseriesRequests += point.Requests
	}
	if timeseries.Route != request.Route || timeseries.Method != request.Method || timeseriesRequests < 1 {
		t.Fatalf("route analytics timeseries = %+v, want requests for %s %s", timeseries, request.Method, request.Route)
	}

	mirrorDeployment, err := f.store.CreateDeployment(f.ctx, state.Deployment{
		AppID:       f.app.ID,
		Scope:       "debugger-mirror",
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatalf("create debugger mirror deployment: %v", err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, mirrorDeployment.ID); err != nil {
		t.Fatalf("mark debugger mirror deployment live: %v", err)
	}
	mirrorInstance, err := f.store.CreateInstance(f.ctx, f.app.ID, mirrorDeployment.ID,
		string(state.StateRunning), 256, f.nodeID, "")
	if err != nil {
		t.Fatalf("create debugger mirror instance: %v", err)
	}
	f.vmmd.SetVersion(mirrorInstance.ID, "debugger-replay")
	f.vmmd.SetDefaultVersion("debugger-replay")
	if f.artifacts == nil {
		t.Fatal("debugger artifact store is not configured")
	}
	mirrorLayerKey := "layers/" + mirrorDeployment.ID + ".ext4"
	mirrorLayer := []byte("gregale debugger replay fixture artifact\n")
	if err := f.artifacts.Put(f.ctx, mirrorLayerKey, strings.NewReader(string(mirrorLayer))); err != nil {
		t.Fatalf("publish debugger mirror layer: %v", err)
	}
	signer, err := cosign.NewLocalSigner(f.h.SignKeyPath, f.artifacts, nil)
	if err != nil {
		t.Fatalf("create debugger artifact signer: %v", err)
	}
	if err := signer.Sign(f.ctx, mirrorLayerKey, cosign.SigKeyFor(mirrorLayerKey)); err != nil {
		t.Fatalf("sign debugger mirror layer: %v", err)
	}
	if err := f.store.SetDeploymentRootfs(f.ctx, mirrorDeployment.ID, mirrorLayerKey,
		mirrorLayerKey, int64(len(mirrorLayer))); err != nil {
		t.Fatalf("publish debugger mirror rootfs metadata: %v", err)
	}

	percent := 100
	body, statusCode = doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/normal-debugger/mirrors", api.CreateMirrorRuleRequest{
			SourceDeploymentID: sourceDeployment.ID,
			MirrorDeploymentID: mirrorDeployment.ID,
			Percent:            &percent,
		})
	if statusCode != http.StatusCreated {
		t.Fatalf("create debugger mirror rule: status=%d body=%s", statusCode, body)
	}
	var rule api.MirrorRuleResponse
	if err := json.Unmarshal(body, &rule); err != nil {
		t.Fatalf("decode debugger mirror rule: %v body=%s", err, body)
	}

	replayHeaders := map[string]string{"Idempotency-Key": "debugger-replay-once"}
	body, statusCode = doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/normal-debugger/debug/requests/"+request.ID+"/replay", nil, replayHeaders)
	if statusCode != http.StatusAccepted {
		t.Fatalf("debugger replay: status=%d body=%s", statusCode, body)
	}
	var firstReplay api.DebugReplayResponse
	if err := json.Unmarshal(body, &firstReplay); err != nil {
		t.Fatalf("decode debugger replay: %v body=%s", err, body)
	}
	if firstReplay.MirrorInvocationID == "" || firstReplay.Status != "queued" {
		t.Fatalf("debugger replay = %+v, want queued invocation", firstReplay)
	}

	body, statusCode = doReq(t, f.h, f.key, http.MethodPost,
		"/v1/apps/normal-debugger/debug/requests/"+request.ID+"/replay", nil, replayHeaders)
	if statusCode != http.StatusAccepted {
		t.Fatalf("idempotent debugger replay: status=%d body=%s", statusCode, body)
	}
	var secondReplay api.DebugReplayResponse
	if err := json.Unmarshal(body, &secondReplay); err != nil {
		t.Fatalf("decode idempotent debugger replay: %v body=%s", err, body)
	}
	if secondReplay.MirrorInvocationID != firstReplay.MirrorInvocationID {
		t.Fatalf("idempotent replay ids = %q and %q, want same invocation", firstReplay.MirrorInvocationID, secondReplay.MirrorInvocationID)
	}

	invocation := pollUntilCompleted(t, f.h, f.key, firstReplay.MirrorInvocationID, 20*time.Second)
	if invocation.State != string(state.InvocationCompleted) || invocation.Source != string(state.InvocationReplay) {
		t.Fatalf("debugger replay invocation = state %q source %q error %q, want completed/replay", invocation.State, invocation.Source, invocation.LastError)
	}
	var replayResult struct {
		SourceStatusCode int  `json:"source_status_code"`
		MirrorStatusCode int  `json:"mirror_status_code"`
		StatusDiff       bool `json:"status_diff"`
	}
	if err := json.Unmarshal(invocation.Result, &replayResult); err != nil {
		t.Fatalf("decode debugger replay result: %v result=%s", err, invocation.Result)
	}
	if replayResult.SourceStatusCode != http.StatusOK || replayResult.MirrorStatusCode != http.StatusOK || replayResult.StatusDiff {
		t.Fatalf("debugger replay result = %+v, want matching 200 statuses", replayResult)
	}

	mirrorRequests := 0
	wantReplayPath := strings.TrimPrefix(request.Route, request.Method+" ")
	var observedBridgeRequests []string
	for _, capture := range f.vmmd.Requests() {
		if capture.Init.Instance != sourceInstance.ID {
			observedBridgeRequests = append(observedBridgeRequests,
				fmt.Sprintf("%s %s instance=%s", capture.Init.Method, capture.Init.RequestUri, capture.Init.Instance))
		}
		// VMMD also records health and setup traffic; count only the replay's
		// cross-instance request with the expected method and route.
		if capture.Init.Instance == sourceInstance.ID || capture.Init.Method != request.Method || capture.Init.RequestUri != wantReplayPath {
			continue
		}
		mirrorRequests++
		if len(capture.Body) != 0 {
			t.Fatalf("debugger replay forwarded body=%q, want empty metadata-only body", capture.Body)
		}
	}
	if mirrorRequests != 1 {
		t.Fatalf("debugger replay mirror forwards=%d, want exactly one after idempotent retry; cross-instance VMMD requests=%v", mirrorRequests, observedBridgeRequests)
	}

	body, statusCode = doReq(t, f.h, f.key, http.MethodGet,
		"/v1/apps/normal-debugger/mirrors/"+rule.ID+"/summary?window=1h", nil)
	if statusCode != http.StatusOK {
		t.Fatalf("debugger replay mirror summary: status=%d body=%s", statusCode, body)
	}
	var summary api.MirrorSummaryResponse
	if err := json.Unmarshal(body, &summary); err != nil {
		t.Fatalf("decode debugger replay mirror summary: %v body=%s", err, body)
	}
	if summary.TotalInvocations != 1 || summary.StatusDiffCount != 0 {
		t.Fatalf("debugger replay mirror summary = %+v, want one matching invocation", summary)
	}
}

// exerciseOutboundDependencySpan sends a real request through the platform's
// outbound handler, then retains its classified dependency span through the
// same apid writer socket used by gatewayd. The caller's trace ID ties the
// outbound span to the earlier request_telemetry row.
func exerciseOutboundDependencySpan(t *testing.T, f *normalPathFixture, traceparent string) {
	t.Helper()
	if f.spansWriterSocket == "" {
		t.Fatal("normal-path fixture has no spans-writer socket")
	}
	app, err := f.store.AppByID(f.ctx, f.app.ID)
	if err != nil {
		t.Fatalf("load outbound test app identity: %v", err)
	}

	providerServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(25 * time.Millisecond)
		_, _ = io.WriteString(w, "provider-ok\n")
	}))
	defer providerServer.Close()

	integration, err := outbound.NewIntegration("analytics-stripe", providerServer.URL, "gateway-token",
		[]string{f.app.ID}, 100, 100, 10, 5*time.Second)
	if err != nil {
		t.Fatalf("create outbound test integration: %v", err)
	}
	integration.AccountID = app.AccountID
	integration.Name = "stripe"
	resolver, err := outbound.NewStaticResolver([]outbound.Integration{integration})
	if err != nil {
		t.Fatalf("create outbound test resolver: %v", err)
	}
	handler, err := outbound.NewHandler(resolver, outbound.NewMemoryBackend(), providerServer.Client())
	if err != nil {
		t.Fatalf("create outbound test handler: %v", err)
	}
	metrics, err := outbound.NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("create outbound test metrics: %v", err)
	}
	handler.Metrics = metrics

	acc := gateway.NewSpansAccumulator()
	writer, err := apidgrpc.DialSpansWriter(f.ctx, f.spansWriterSocket, nil)
	if err != nil {
		t.Fatalf("dial apid spans-writer socket: %v", err)
	}
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSyncer(gateway.NewRetainedServiceSpansExporter(acc, nil)),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	flushCtx, cancelFlush := context.WithCancel(context.Background())
	flushDone := make(chan error, 1)
	go func() {
		flushDone <- acc.RunFlushLoop(flushCtx, gateway.FlushLoopConfig{
			Interval: 50 * time.Millisecond,
			MaxSpansPerTrace: func(string) int {
				return 1000
			},
			WriteFn: func(ctx context.Context, traceID string, summaryJSON []byte, accountID string) (string, int64, error) {
				return writer.WriteSpansSummary(ctx, traceID, summaryJSON, accountID)
			},
		})
	}()
	finished := false
	finish := func() {
		if finished {
			return
		}
		finished = true
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
		if err := provider.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shut down outbound test tracer: %v", err)
		}
		cancelShutdown()
		cancelFlush()
		select {
		case err := <-flushDone:
			if err != nil {
				t.Errorf("drain outbound test spans: %v", err)
			}
		case <-time.After(6 * time.Second):
			t.Error("timed out draining outbound test spans")
		}
		if err := writer.Close(); err != nil {
			t.Errorf("close apid spans-writer client: %v", err)
		}
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	}
	t.Cleanup(finish)

	server := httptest.NewServer(trace.HTTPHandler("outboundd", handler))
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL+outbound.Prefix+integration.ID+"/v1/charges", nil)
	if err != nil {
		t.Fatalf("create outbound test request: %v", err)
	}
	request.Header.Set(outbound.TokenHeader, "gateway-token")
	request.Header.Set(outbound.AppHeader, f.app.ID)
	request.Header.Set("Traceparent", traceparent)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("send outbound test request: %v", err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		t.Fatalf("read outbound test response: %v", readErr)
	}
	if response.StatusCode != http.StatusOK || string(body) != "provider-ok\n" {
		t.Fatalf("outbound test response = status %d body %q, want 200/provider-ok", response.StatusCode, body)
	}
	finish()
}

// TestE2E_NormalPath_PublicRequestIDJournalWithoutDetailedTelemetry pins the
// request-ID journal's independent retention path across the real gateway →
// apid → Postgres boundary. A request without a detailed telemetry row must
// still resolve by the ID returned by its origin response, with an explicit
// journal-only result rather than a 404 or fabricated telemetry fields.
func TestE2E_NormalPath_PublicRequestIDJournalWithoutDetailedTelemetry(t *testing.T) {
	f := newNormalPathDebuggerFixtureWithTelemetry(t, "normal-request-id-only", false)
	if f == nil {
		return
	}

	_, sourceInstance := createNormalPathLiveDeployment(t, f, f.app.ID, "request-id-only-source")
	f.vmmd.SetVersion(sourceInstance.ID, "request-id-only-source")
	waitForNormalPathDebuggerResponse(t, f, "normal-path:request-id-only-source\n", 10*time.Second)

	requestHeaders, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet,
		"/request-id-only", nil, map[string]string{"Authorization": "Bearer " + f.key})
	if statusCode != http.StatusOK || string(body) != "normal-path:request-id-only-source\n" {
		t.Fatalf("request-ID-only origin request: status=%d body=%q", statusCode, body)
	}
	publicRequestID := requestHeaders.Get(api.RequestIDHeader)
	traceID := requestHeaders.Get(api.TraceIDHeader)
	if publicRequestID == "" || traceID == "" || publicRequestID == traceID {
		t.Fatalf("origin response IDs: request=%q trace=%q; want distinct public and W3C IDs", publicRequestID, traceID)
	}

	body, statusCode = doReq(t, f.h, f.key, http.MethodGet,
		"/v1/apps/normal-request-id-only/debug/requests/"+url.PathEscape(publicRequestID), nil)
	if statusCode != http.StatusOK {
		t.Fatalf("request-ID-only debugger lookup: status=%d body=%s", statusCode, body)
	}
	var detail api.DebugTelemetryRequestItem
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode request-ID-only debugger detail: %v body=%s", err, body)
	}
	if detail.RequestID != publicRequestID || detail.EvidenceStatus != "request_id_only" || detail.ID != "" {
		t.Fatalf("request-ID-only debugger detail = %+v, want exact ID and no telemetry row", detail)
	}
	if detail.TraceID == nil || *detail.TraceID != traceID {
		t.Fatalf("request-ID-only trace_id = %v, want response %s", detail.TraceID, traceID)
	}
	if _, err := time.Parse(time.RFC3339Nano, detail.ReceivedAt); err != nil {
		t.Fatalf("request-ID-only received_at = %q: %v", detail.ReceivedAt, err)
	}

	body, statusCode = doReq(t, f.h, f.key, http.MethodGet,
		"/v1/apps/normal-request-id-only/debug/requests?since=24h&limit=200", nil)
	if statusCode != http.StatusOK {
		t.Fatalf("list with detailed telemetry disabled: status=%d body=%s", statusCode, body)
	}
	var page api.DebugTelemetryListResponse
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode list with detailed telemetry disabled: %v body=%s", err, body)
	}
	if len(page.Requests) != 0 {
		t.Fatalf("detailed telemetry list = %+v, want empty while telemetry is disabled", page.Requests)
	}

	gregale := buildGregale(t)
	stdout, stderr, exit := runGregaleAgainstHarness(t, gregale, f, "debug", "requests", "get", "normal-request-id-only", publicRequestID)
	if exit != 0 || !strings.Contains(stdout, "Public ID:  "+publicRequestID) ||
		!strings.Contains(stdout, "request ID is retained, but detailed request telemetry is unavailable") ||
		!strings.Contains(stdout, "Trace ID:   "+traceID) {
		t.Fatalf("gregale debug get for journal-only ID: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}

// adr: 127 pins exact public-ID durability and the fail-closed write boundary.
// TestE2E_NormalPath_RequestIDJournalSurvivesAPIDRestartAndBackpressure pins
// the failure boundary for the synchronous exact-ID journal. An ID accepted
// before an apid restart must remain queryable, and a slow journal commit must
// fail closed before the guest is reached; once the database recovers, retrying
// the same public ID must be served and remain queryable.
func TestE2E_NormalPath_RequestIDJournalSurvivesAPIDRestartAndBackpressure(t *testing.T) {
	f := newNormalPathDebuggerFixtureWithTelemetry(t, "normal-request-id-restart", false)
	if f == nil {
		return
	}

	_, sourceInstance := createNormalPathLiveDeployment(t, f, f.app.ID, "request-id-restart-source")
	f.vmmd.SetVersion(sourceInstance.ID, "request-id-restart-source")
	waitForNormalPathDebuggerResponse(t, f, "normal-path:request-id-restart-source\n", 10*time.Second)

	const durableRequestID = "customer-request-before-apid-restart"
	requestHeaders, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet,
		"/request-id-restart", nil, map[string]string{
			"Authorization":     "Bearer " + f.key,
			api.RequestIDHeader: durableRequestID,
		})
	if statusCode != http.StatusOK || string(body) != "normal-path:request-id-restart-source\n" {
		t.Fatalf("pre-restart request: status=%d body=%q", statusCode, body)
	}
	if got := requestHeaders.Get(api.RequestIDHeader); got != durableRequestID {
		t.Fatalf("pre-restart public request ID=%q, want %q", got, durableRequestID)
	}

	if err := f.h.RestartAPID(); err != nil {
		t.Fatalf("restart apid: %v", err)
	}
	lookup := "/v1/apps/normal-request-id-restart/debug/requests/" + url.PathEscape(durableRequestID)
	body, statusCode = doReq(t, f.h, f.key, http.MethodGet, lookup, nil)
	if statusCode != http.StatusOK {
		t.Fatalf("post-restart request-ID lookup: status=%d body=%s", statusCode, body)
	}
	var detail api.DebugTelemetryRequestItem
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode post-restart request-ID lookup: %v body=%s", err, body)
	}
	if detail.RequestID != durableRequestID || detail.EvidenceStatus != "request_id_only" || detail.ID != "" {
		t.Fatalf("post-restart request-ID detail = %+v, want exact journal-only mapping", detail)
	}
	// Exercise the gateway's existing gRPC client after the apid listener has
	// been recreated, not just the restarted HTTP debugger read path.
	requestHeaders, body, statusCode = doReqHeaders(t, f.h, f.host, http.MethodGet,
		"/request-id-after-restart", nil, map[string]string{
			"Authorization":     "Bearer " + f.key,
			api.RequestIDHeader: "customer-request-after-apid-restart",
		})
	if statusCode != http.StatusOK || string(body) != "normal-path:request-id-restart-source\n" {
		t.Fatalf("post-restart journal write: status=%d body=%q", statusCode, body)
	}
	if got := requestHeaders.Get(api.RequestIDHeader); got != "customer-request-after-apid-restart" {
		t.Fatalf("post-restart public request ID=%q, want customer-request-after-apid-restart", got)
	}

	// Hold the journal table so the real apid gRPC writer cannot commit. The
	// gateway's bounded journal RPC must return 503 without forwarding to vmmd.
	lockCtx, cancelLock := context.WithTimeout(f.ctx, 5*time.Second)
	defer cancelLock()
	tx, err := f.h.Pool.Begin(lockCtx)
	if err != nil {
		t.Fatalf("begin request-ID backpressure transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(lockCtx, "LOCK TABLE request_id_journal IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatalf("lock request-ID journal: %v", err)
	}
	requestsBeforeBackpressure := len(f.vmmd.Requests())

	const backpressuredRequestID = "customer-request-during-journal-backpressure"
	requestHeaders, body, statusCode = doReqHeaders(t, f.h, f.host, http.MethodGet,
		"/request-id-backpressure", nil, map[string]string{
			"Authorization":     "Bearer " + f.key,
			api.RequestIDHeader: backpressuredRequestID,
		})
	if statusCode != http.StatusServiceUnavailable {
		t.Fatalf("backpressured journal request: status=%d body=%q, want 503", statusCode, body)
	}
	if got := requestHeaders.Get(api.RequestIDHeader); got != backpressuredRequestID {
		t.Fatalf("backpressured response public request ID=%q, want %q", got, backpressuredRequestID)
	}
	if got := len(f.vmmd.Requests()); got != requestsBeforeBackpressure {
		t.Fatalf("backpressured request reached guest: vmmd requests=%d, want unchanged %d", got, requestsBeforeBackpressure)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("release request-ID journal lock: %v", err)
	}

	requestHeaders, body, statusCode = doReqHeaders(t, f.h, f.host, http.MethodGet,
		"/request-id-backpressure", nil, map[string]string{
			"Authorization":     "Bearer " + f.key,
			api.RequestIDHeader: backpressuredRequestID,
		})
	if statusCode != http.StatusOK || string(body) != "normal-path:request-id-restart-source\n" {
		t.Fatalf("request after journal recovery: status=%d body=%q", statusCode, body)
	}
	if got := requestHeaders.Get(api.RequestIDHeader); got != backpressuredRequestID {
		t.Fatalf("recovered response public request ID=%q, want %q", got, backpressuredRequestID)
	}
	body, statusCode = doReq(t, f.h, f.key, http.MethodGet,
		"/v1/apps/normal-request-id-restart/debug/requests/"+url.PathEscape(backpressuredRequestID), nil)
	if statusCode != http.StatusOK {
		t.Fatalf("request-ID lookup after backpressure recovery: status=%d body=%s", statusCode, body)
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode request-ID lookup after backpressure recovery: %v body=%s", err, body)
	}
	if detail.RequestID != backpressuredRequestID || detail.EvidenceStatus != "request_id_only" {
		t.Fatalf("post-recovery request-ID detail = %+v, want exact journal-only mapping", detail)
	}
}

func waitForNormalPathDebuggerRequestByPublicID(t *testing.T, f *normalPathFixture, publicRequestID string, timeout time.Duration) api.DebugTelemetryRequestItem {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastBody []byte
	var lastStatus int
	for time.Now().Before(deadline) {
		lastBody, lastStatus = doReq(t, f.h, f.key, http.MethodGet,
			"/v1/apps/normal-debugger/debug/requests/"+url.PathEscape(publicRequestID), nil)
		if lastStatus == http.StatusOK {
			var request api.DebugTelemetryRequestItem
			if err := json.Unmarshal(lastBody, &request); err != nil {
				t.Fatalf("decode polled public request-ID lookup: %v body=%s", err, lastBody)
			}
			if request.RequestID == publicRequestID && request.ID != "" {
				return request
			}
		} else if lastStatus != http.StatusNotFound {
			t.Fatalf("poll public request-ID lookup: status=%d body=%s", lastStatus, lastBody)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("debugger request %s did not arrive within %s; last status=%d body=%s", publicRequestID, timeout, lastStatus, lastBody)
	return api.DebugTelemetryRequestItem{}
}

func waitForNormalPathDebuggerRequestByTraceID(t *testing.T, f *normalPathFixture, deploymentID, expectedTraceID string, timeout time.Duration) api.DebugTelemetryRequestItem {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last api.DebugTelemetryListResponse
	for time.Now().Before(deadline) {
		body, statusCode := doReq(t, f.h, f.key, http.MethodGet,
			"/v1/apps/normal-debugger/debug/requests?since=24h&limit=200", nil)
		if statusCode != http.StatusOK {
			t.Fatalf("poll debugger requests: status=%d body=%s", statusCode, body)
		}
		if err := json.Unmarshal(body, &last); err != nil {
			t.Fatalf("decode polled debugger requests: %v body=%s", err, body)
		}
		for _, request := range last.Requests {
			if request.DeploymentID == deploymentID && request.TraceID != nil && *request.TraceID == expectedTraceID {
				return request
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("debugger request for deployment %s did not arrive within %s; last page=%+v", deploymentID, timeout, last)
	return api.DebugTelemetryRequestItem{}
}

func runGregaleAgainstHarness(t *testing.T, bin string, f *normalPathFixture, args ...string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmdEnv := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "FAAS_API=") || strings.HasPrefix(entry, "FAAS_TOKEN=") {
			continue
		}
		cmdEnv = append(cmdEnv, entry)
	}
	cmd.Env = append(cmdEnv, "FAAS_API="+f.h.APIDURL, "FAAS_TOKEN="+f.key)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return stdout.String(), stderr.String(), exitError.ExitCode()
	}
	t.Fatalf("run gregale %v: %v", args, err)
	return stdout.String(), stderr.String(), -1
}

func waitForNormalPathDebuggerResponse(t *testing.T, f *normalPathFixture, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastStatus int
	var lastBody []byte
	for time.Now().Before(deadline) {
		_, lastBody, lastStatus = doReqHeaders(t, f.h, f.host, http.MethodGet, "/", nil, map[string]string{
			"Authorization": "Bearer " + f.key,
		})
		if lastStatus == http.StatusOK && string(lastBody) == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("GET %s/ did not return %q within %s; last status=%d body=%s", f.host, want, timeout, lastStatus, lastBody)
}
