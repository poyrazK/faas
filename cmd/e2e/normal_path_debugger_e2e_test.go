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
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

func newNormalPathDebuggerFixture(t *testing.T, slug string) *normalPathFixture {
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
	f := newNormalPathFixtureWithPlanAndEnv(t, slug, api.PlanPro,
		"FAAS_APP_ERRORS_ENABLED=false",
		"FAAS_OTEL_SPANS_WRITER_ENABLED=true",
		"FAAS_REQUEST_TELEMETRY_ENABLED=true",
		"FAAS_APID_REQUEST_TELEMETRY_SOCKET="+telemetrySocket,
		"FAAS_APID_OTEL_SPANS_WRITER_SOCKET="+spansWriterSocket,
		"FAAS_OTEL_FLUSH_INTERVAL=50ms",
		"FAAS_STORAGE_BACKEND=local",
		"FAAS_STORAGE_ROOT="+artifactDir,
	)
	if f != nil {
		f.artifacts = artifacts
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
	_, body, statusCode := doReqHeaders(t, f.h, f.host, http.MethodGet,
		"/debugger/telemetry", nil, map[string]string{
			"Authorization":     "Bearer " + f.key,
			"X-Customer-Secret": secret,
			"Traceparent":       traceparent,
		})
	if statusCode != http.StatusOK || string(body) != "normal-path:debugger-source\n" {
		t.Fatalf("debugger source request: status=%d body=%q", statusCode, body)
	}

	request := waitForNormalPathDebuggerRequest(t, f, sourceDeployment.ID, traceID, 20*time.Second)
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
		"/v1/apps/normal-debugger/debug/requests/"+request.ID, nil)
	if statusCode != http.StatusOK {
		t.Fatalf("debugger request detail: status=%d body=%s", statusCode, body)
	}
	var detail api.DebugTelemetryRequestItem
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode debugger request detail: %v body=%s", err, body)
	}
	if detail.ID != request.ID || detail.DeploymentID != sourceDeployment.ID {
		t.Fatalf("debugger request detail = %+v, want request %s on %s", detail, request.ID, sourceDeployment.ID)
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

	// The same gateway-recorded row that powers the debugger must feed the
	// route-centric analytics API after crossing the telemetry socket and
	// Postgres. Poll until the asynchronous retained-span writer has attached
	// the classified dependency evidence to the request row.
	var analytics api.RequestAnalyticsResponse
	dependencyFound := false
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
			for _, dependency := range route.Dependencies {
				if dependency.Type == "managed_binding" && dependency.Kind == "service_proxy" && dependency.Name == "service.analyticsdependency" && dependency.Samples >= 1 {
					dependencyFound = true
					break
				}
			}
		}
		if dependencyFound {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !dependencyFound {
		t.Fatalf("route analytics did not receive the correlated service dependency span: %+v", analytics.Routes)
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
	for _, capture := range f.vmmd.Requests() {
		// VMMD also records unrelated health probes; count only the replay call.
		if capture.Init.Instance != mirrorInstance.ID {
			continue
		}
		mirrorRequests++
		if capture.Init.Method != request.Method || capture.Init.RequestUri != wantReplayPath {
			t.Fatalf("debugger replay bridge request = method %q uri %q, want %q %q", capture.Init.Method, capture.Init.RequestUri, request.Method, wantReplayPath)
		}
		if len(capture.Body) != 0 {
			t.Fatalf("debugger replay forwarded body=%q, want empty metadata-only body", capture.Body)
		}
	}
	if mirrorRequests != 1 {
		t.Fatalf("debugger replay mirror forwards=%d, want exactly one after idempotent retry", mirrorRequests)
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

func waitForNormalPathDebuggerRequest(t *testing.T, f *normalPathFixture, deploymentID, expectedTraceID string, timeout time.Duration) api.DebugTelemetryRequestItem {
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
