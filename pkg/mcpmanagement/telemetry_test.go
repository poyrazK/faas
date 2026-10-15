package mcpmanagement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func telemetryText(t *testing.T, metrics *Telemetry) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != 200 {
		t.Fatalf("metrics status=%d", recorder.Code)
	}
	return recorder.Body.String()
}
func expectMetric(t *testing.T, metrics *Telemetry, want string) {
	t.Helper()
	if !strings.Contains(telemetryText(t, metrics), want) {
		t.Fatalf("missing metric %s", want)
	}
}

func TestTelemetryFixedLabelsOutcomesAndPrivacy(t *testing.T) {
	metrics := NewTelemetry()
	private := "private-app-secret-fp_test_do_not_publish"
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps" {
			_ = json.NewEncoder(w).Encode([]api.AppResponse{{ID: "private-id", Slug: private}})
			return
		}
		api.WriteProblem(w, api.NewProblem(403, private, "Private failure", private))
	}))
	t.Cleanup(b.Close)
	h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL, Profile: private, Telemetry: metrics, accountID: private, grantID: private}, "2026-07-28", true)
	if _, err := h.client.Complete(h.ctx, completionParams("app", "", nil)); err != nil {
		t.Fatal(err)
	}
	_, _ = h.client.ReadResource(h.ctx, &mcp.ReadResourceParams{URI: "gregale://default/apps/" + private + "/health"})
	_, _ = h.client.GetPrompt(h.ctx, &mcp.GetPromptParams{Name: "gregale_deployment_review", Arguments: map[string]string{"app": private}})
	_, _ = h.client.CallTool(h.ctx, &mcp.CallToolParams{Name: "gregale_get_app", Arguments: map[string]string{"app": private}})
	for i := range 20 {
		_, _ = h.client.CallTool(h.ctx, &mcp.CallToolParams{Name: fmt.Sprintf("%s-%d", private, i), Arguments: map[string]string{"token": private}})
	}
	expectMetric(t, metrics, `gregale_management_mcp_requests_total{operation="completion/complete",outcome="success",tool="none"} 1`)
	expectMetric(t, metrics, `gregale_management_mcp_requests_total{operation="tools/call",outcome="error",tool="gregale_get_app"} 1`)
	expectMetric(t, metrics, `gregale_management_mcp_requests_total{operation="tools/call",outcome="error",tool="unknown"} 20`)
	expectMetric(t, metrics, `gregale_management_mcp_request_duration_seconds_count{operation="tools/call",tool="unknown"} 20`)
	text := telemetryText(t, metrics)
	for _, secret := range []string{private, testToken, b.URL, "private-id", "grant_id", "account_id", "go_goroutines", "process_cpu"} {
		if strings.Contains(text, secret) {
			t.Fatalf("private or unrelated data in registry: %s", secret)
		}
	}
	other := NewTelemetry()
	if strings.Contains(telemetryText(t, other), `tool="unknown"`) {
		t.Fatal("private registries share state")
	}
}

func TestTelemetryBusyAndActiveCapacity(t *testing.T) {
	metrics := NewTelemetry()
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	t.Cleanup(b.Close)
	h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL, Telemetry: metrics}, "2026-07-28", false)
	releases := []func(){}
	for range maxShortRequests {
		release, err := h.manager.admission.acquire(h.ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	expectMetric(t, metrics, `gregale_management_mcp_active{kind="short_requests"} 8`)
	result, err := h.client.CallTool(h.ctx, &mcp.CallToolParams{Name: "gregale_get_app", Arguments: map[string]string{"app": "alpha"}})
	assertBusyTool(t, result, err)
	_, _ = h.client.Complete(h.ctx, completionParams("app", "", nil))
	expectMetric(t, metrics, `gregale_management_mcp_requests_total{operation="tools/call",outcome="busy",tool="gregale_get_app"} 1`)
	expectMetric(t, metrics, `gregale_management_mcp_requests_total{operation="completion/complete",outcome="busy",tool="none"} 1`)
	releaseWatch, err := h.manager.admission.acquire(h.ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	expectMetric(t, metrics, `gregale_management_mcp_active{kind="rollout_watches"} 1`)
	releaseWatch()
	for _, release := range releases {
		release()
	}
	expectMetric(t, metrics, `gregale_management_mcp_active{kind="short_requests"} 0`)
	expectMetric(t, metrics, `gregale_management_mcp_active{kind="rollout_watches"} 0`)
}

func TestTelemetrySubscriptionLifecycleAndNotifications(t *testing.T) {
	metrics := NewTelemetry()
	f := progressFixture()
	b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(b.Close)
	h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL, Telemetry: metrics}, "2026-07-28", true)
	uri := "gregale://default/apps/alpha/health"
	h.subscribe(t, uri)
	expectMetric(t, metrics, `gregale_management_mcp_active{kind="listen_streams"} 1`)
	expectMetric(t, metrics, `gregale_management_mcp_active{kind="resource_slots"} 1`)
	f.mu.Lock()
	f.current = "changed"
	f.mu.Unlock()
	select {
	case <-h.updates:
	case <-h.ctx.Done():
		t.Fatal("missing subscription update")
	}
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(telemetryText(t, metrics), "gregale_management_mcp_resource_updates_total 1") {
		if time.Now().After(deadline) {
			t.Fatal("sent notification was not counted")
		}
		time.Sleep(time.Millisecond)
	}
	if err := h.client.Unsubscribe(h.ctx, &mcp.UnsubscribeParams{URI: uri}); err != nil {
		t.Fatal(err)
	}
	awaitResourceGroups(t, h, 0)
	awaitAdmissionEmpty(t, h.manager.admission)
	expectMetric(t, metrics, `gregale_management_mcp_active{kind="listen_streams"} 0`)
	expectMetric(t, metrics, `gregale_management_mcp_active{kind="resource_slots"} 0`)
	if strings.Contains(telemetryText(t, metrics), uri) {
		t.Fatal("resource URI escaped into metrics")
	}
}

func TestTelemetryCancellationAndTimeout(t *testing.T) {
	for _, why := range []string{"cancelled", "timeout"} {
		t.Run(why, func(t *testing.T) {
			metrics := NewTelemetry()
			entered := make(chan struct{}, 1)
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-r.Context().Done() }))
			t.Cleanup(b.Close)
			timeout := time.Second
			if why == "timeout" {
				timeout = 50 * time.Millisecond
			}
			h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL, Timeout: timeout, Telemetry: metrics}, "2026-07-28", true)
			ctx, cancel := context.WithCancel(h.ctx)
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				_, _ = h.client.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_get_app", Arguments: map[string]string{"app": "alpha"}})
			}()
			select {
			case <-entered:
			case <-h.ctx.Done():
				t.Fatal("request not started")
			}
			expectMetric(t, metrics, `gregale_management_mcp_active{kind="short_requests"} 1`)
			if why == "cancelled" {
				cancel()
			}
			select {
			case <-done:
			case <-h.ctx.Done():
				t.Fatal("request did not finish")
			}
			awaitAdmissionEmpty(t, h.manager.admission)
			deadline := time.Now().Add(time.Second)
			want := fmt.Sprintf(`gregale_management_mcp_requests_total{operation="tools/call",outcome="%s",tool="gregale_get_app"} 1`, why)
			for !strings.Contains(telemetryText(t, metrics), want) {
				if time.Now().After(deadline) {
					t.Fatalf("outcome not counted: %s", want)
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}

func TestTelemetryOAuthGrantsAndRestoreShareRegistry(t *testing.T) {
	metrics := NewTelemetry()
	store, path, key := testStateStore(t)
	f := newRemoteFixtureWithOptions(t, false, HTTPOptions{Onboarding: "api-key", StateStore: store, Telemetry: metrics})
	code := f.authorizeAccount("fp_test_alice_remote_key", false)
	var tokens tokenResponse
	response := f.exchange(code, testVerifier, f.resource, f.clientID)
	decodeResponse(t, response, &tokens)
	_ = response.Body.Close()
	read := func(tokens tokenResponse) {
		cs, ctx := f.connect(tokens, "2026-07-28")
		callTool(t, ctx, cs, "gregale_context", map[string]any{}, false)
		_ = cs.Close()
	}
	read(tokens)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	options := f.opts
	options.StateStore = next
	handler, err := NewHTTPHandler(options)
	if err != nil {
		t.Fatal(err)
	}
	f.setHandler(handler)
	read(tokens)
	code = f.authorizeAccount("fp_test_bob_remote_key", false)
	var bob tokenResponse
	response = f.exchange(code, testVerifier, f.resource, f.clientID)
	decodeResponse(t, response, &bob)
	_ = response.Body.Close()
	read(bob)
	expectMetric(t, metrics, `gregale_management_mcp_requests_total{operation="tools/call",outcome="success",tool="gregale_context"} 3`)
	expectMetric(t, metrics, `gregale_management_mcp_active{kind="short_requests"} 0`)
	text := telemetryText(t, metrics)
	for _, private := range []string{"alice", "bob", tokens.AccessToken, bob.AccessToken, f.resource} {
		if strings.Contains(text, private) {
			t.Fatal("grant identity escaped into aggregate registry")
		}
	}
}
