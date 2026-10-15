package mcpmanagement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestLifecycleDrainCancelsObservationsAndRejectsWork(t *testing.T) {
	lifecycle := NewLifecycle()
	metrics := NewTelemetry()
	entered := make(chan struct{}, 2)
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-r.Context().Done() }))
	t.Cleanup(b.Close)
	h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL, Lifecycle: lifecycle, Telemetry: metrics}, "2026-07-28", true)
	done := make(chan struct{}, 2)
	for _, tool := range []string{"gregale_get_app", "gregale_watch_rollout"} {
		go func(tool string) {
			defer func() { done <- struct{}{} }()
			_, _ = h.client.CallTool(h.ctx, &mcp.CallToolParams{Name: tool, Arguments: func() map[string]any {
				args := map[string]any{"app": "alpha"}
				if tool == "gregale_watch_rollout" {
					args["deployment_id"] = "release-base"
				}
				return args
			}()})
		}(tool)
	}
	for range 2 {
		select {
		case <-entered:
		case <-h.ctx.Done():
			t.Fatal("observation not started")
		}
	}
	grace, cancel := context.WithTimeout(h.ctx, time.Second)
	defer cancel()
	if err := lifecycle.Shutdown(grace); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-done:
		case <-h.ctx.Done():
			t.Fatal("observation did not stop")
		}
	}
	if !lifecycle.Drained() {
		t.Fatal("coordinator did not finish")
	}
	awaitAdmissionEmpty(t, h.manager.admission)
	result, err := h.client.CallTool(h.ctx, &mcp.CallToolParams{Name: "gregale_get_app", Arguments: map[string]string{"app": "alpha"}})
	if err != nil || !result.IsError {
		t.Fatalf("new work accepted: %+v %v", result, err)
	}
	data, _ := json.Marshal(result.StructuredContent)
	if !strings.Contains(string(data), `"code":"mcp_draining"`) || !strings.Contains(string(data), `"retry_after_seconds":1`) {
		t.Fatalf("missing drain guidance: %s", data)
	}
	if _, err := h.client.Complete(h.ctx, completionParams("app", "", nil)); err == nil || !strings.Contains(err.Error(), "mcp_draining") {
		t.Fatalf("completion admitted while draining: %v", err)
	}
	if _, err := h.client.ListTools(h.ctx, nil); err != nil {
		t.Fatal("session control/discovery blocked")
	}
	expectMetric(t, metrics, `gregale_management_mcp_active{kind="short_requests"} 0`)
	expectMetric(t, metrics, `gregale_management_mcp_active{kind="rollout_watches"} 0`)
}

func TestLifecycleDrainStopsSubscriptionWorkers(t *testing.T) {
	lifecycle := NewLifecycle()
	metrics := NewTelemetry()
	f := progressFixture()
	b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(b.Close)
	h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL, Lifecycle: lifecycle, Telemetry: metrics}, "2026-07-28", true)
	h.subscribe(t, "gregale://default/apps/alpha/health")
	ctx, cancel := context.WithTimeout(h.ctx, time.Second)
	defer cancel()
	if err := lifecycle.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	awaitResourceGroups(t, h, 0)
	awaitAdmissionEmpty(t, h.manager.admission)
	for _, kind := range []string{"short_requests", "listen_streams", "resource_slots"} {
		expectMetric(t, metrics, `gregale_management_mcp_active{kind="`+kind+`"} 0`)
	}
	// Shutdown is idempotent and does not reopen admission.
	if err := lifecycle.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lifecycle.enter(h.ctx, false); err == nil {
		t.Fatal("drain reopened admission")
	}
}

func TestLifecycleMutationGraceAndDurableRetry(t *testing.T) {
	for _, forced := range []bool{false, true} {
		name := "grace"
		if forced {
			name = "forced_ambiguous"
		}
		t.Run(name, func(t *testing.T) {
			lifecycle := NewLifecycle()
			store, path, key := testStateStore(t)
			f := progressFixture()
			f.keys = map[string]api.DeploymentResponse{}
			entered := make(chan struct{})
			var replay atomic.Bool
			release := make(chan struct{})
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/v1/apps/alpha/deployments" && !replay.Load() {
					if forced {
						reply := httptest.NewRecorder()
						f.serveHTTP(reply, r)
						close(entered)
						<-r.Context().Done()
						return
					}
					close(entered)
					select {
					case <-release:
						f.serveHTTP(w, r)
					case <-r.Context().Done():
					}
					return
				}
				f.serveHTTP(w, r)
			}))
			t.Cleanup(b.Close)
			opts := Options{APIBase: b.URL, AllowWrites: true, Lifecycle: lifecycle, planStore: store, planNamespace: "drain-fixture"}
			h := connectSubscriptionHarness(t, b, opts, "2026-07-28", true)
			plan := callTool(t, h.ctx, h.client, "gregale_plan_deploy", map[string]any{"app": "alpha", "image": testImage, "reason": "drain test"}, false)
			id := planID(t, plan)
			done := make(chan *mcp.CallToolResult, 1)
			go func() {
				result, _ := h.client.CallTool(h.ctx, &mcp.CallToolParams{Name: "gregale_apply_deploy", Arguments: map[string]string{"plan_id": id}})
				done <- result
			}()
			select {
			case <-entered:
			case <-h.ctx.Done():
				t.Fatal("native mutation did not start")
			}
			lifecycle.BeginDrain()
			select {
			case <-done:
				t.Fatal("mutation canceled before grace")
			case <-time.After(20 * time.Millisecond):
			}
			grace := time.Second
			if forced {
				grace = 20 * time.Millisecond
			} else {
				close(release)
			}
			ctx, cancel := context.WithTimeout(h.ctx, grace)
			defer cancel()
			if err := lifecycle.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			result := <-done
			if result == nil {
				t.Fatal("no mutation result")
			}
			if !forced && result.IsError {
				t.Fatal("grace prevented successful mutation")
			}
			if !lifecycle.Drained() {
				t.Fatal("mutation outlived cleanup")
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			restored, err := OpenStateStore(path, key)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = restored.Close() })
			// Replay through a fresh server, preserving its endpoint binding and native key.
			replay.Store(true)
			opts.planStore = restored
			opts.Lifecycle = NewLifecycle()
			retry, retryCtx := connectConfiguredTestServer(t, b, opts, "2026-07-28")
			callTool(t, retryCtx, retry, "gregale_apply_deploy", map[string]string{"plan_id": id}, false)
			f.mu.Lock()
			writes := f.writes
			keys := len(f.keys)
			f.mu.Unlock()
			if writes != 1 || keys != 1 {
				t.Fatalf("native writes=%d keys=%d", writes, keys)
			}
		})
	}
}

func TestLifecycleHTTPWaitsForJournalCommit(t *testing.T) {
	lifecycle := NewLifecycle()
	store, _, _ := testStateStore(t)
	entered := make(chan struct{})
	proceed := make(chan struct{})
	r := &remoteServer{opts: HTTPOptions{StateStore: store}}
	handler := lifecycle.wrapHTTP(r.durableHandler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(204); close(entered); <-proceed })))
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	response := make(chan int, 1)
	go func() {
		res, err := http.Get(server.URL)
		if err != nil {
			response <- 0
			return
		}
		defer res.Body.Close()
		response <- res.StatusCode
	}()
	<-entered
	store.mu.Lock()
	close(proceed)
	lifecycle.BeginDrain()
	finished := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		finished <- lifecycle.Shutdown(ctx)
	}()
	select {
	case <-finished:
		store.mu.Unlock()
		t.Fatal("drained before durable response commit")
	case <-time.After(30 * time.Millisecond):
	}
	store.mu.Unlock()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	status := <-response
	if status != 204 {
		t.Fatalf("commit response status=%d", status)
	}
	res, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 503 || res.Header.Get("Retry-After") != "1" {
		t.Fatal("new HTTP work accepted during drain")
	}
	if !store.available() {
		t.Fatal("drain closed state before caller cleanup")
	}
}
