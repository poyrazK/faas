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

func assertBusyTool(t *testing.T, result *mcp.CallToolResult, err error) {
	t.Helper()
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("overload result=%+v error=%v", result, err)
	}
	data, _ := json.Marshal(result.StructuredContent)
	for _, want := range []string{`"code":"mcp_busy"`, `"retry_after_seconds":1`, `"status":429`, `same plan ID`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing retry guidance: %s", data)
		}
	}
}

func awaitAdmissionEmpty(t *testing.T, a *requestAdmission) {
	t.Helper()
	deadline := time.After(time.Second)
	for len(a.short) != 0 || len(a.watches) != 0 {
		select {
		case <-deadline:
			t.Fatalf("capacity leaked: short=%d watch=%d", len(a.short), len(a.watches))
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestAdmissionSharedMethodsSessionsAndCancellation(t *testing.T) {
	for _, useHTTP := range []bool{false, true} {
		t.Run(fmt.Sprintf("http_%v", useHTTP), func(t *testing.T) {
			entered := make(chan struct{}, maxShortRequests)
			b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/alpha" {
					entered <- struct{}{}
					<-r.Context().Done()
					return
				}
				if r.URL.Path == "/v1/apps" {
					_ = json.NewEncoder(w).Encode([]api.AppResponse{})
					return
				}
				if r.URL.Path == "/v1/account" {
					_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: "account-one"})
					return
				}
				w.WriteHeader(404)
			}))
			t.Cleanup(b.Close)
			h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL}, "2026-07-28", useHTTP)
			ctx, cancel := context.WithCancel(h.ctx)
			done := make(chan struct{}, maxShortRequests)
			for range maxShortRequests {
				go func() {
					defer func() { done <- struct{}{} }()
					_, _ = h.client.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_get_app", Arguments: map[string]string{"app": "alpha"}})
				}()
			}
			for range maxShortRequests {
				select {
				case <-entered:
				case <-h.ctx.Done():
					t.Fatal("short requests did not occupy capacity")
				}
			}
			// A separate SDK session on the same management instance shares admission.
			ct, st := mcp.NewInMemoryTransports()
			ss, err := h.manager.resourceServer.Connect(h.ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ss.Close() })
			other, err := mcp.NewClient(&mcp.Implementation{Name: "admission-second-session", Version: "1"}, nil).Connect(h.ctx, ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = other.Close() })
			result, err := other.CallTool(h.ctx, &mcp.CallToolParams{Name: "gregale_get_app", Arguments: map[string]string{"app": "alpha"}})
			assertBusyTool(t, result, err)
			if _, err := other.ReadResource(h.ctx, &mcp.ReadResourceParams{URI: "gregale://context"}); err == nil || !strings.Contains(err.Error(), "mcp_busy") {
				t.Fatalf("resource escaped shared bound: %v", err)
			}
			if _, err := other.GetPrompt(h.ctx, &mcp.GetPromptParams{Name: "gregale_deployment_review", Arguments: map[string]string{"app": "alpha"}}); err == nil || !strings.Contains(err.Error(), "mcp_busy") {
				t.Fatalf("prompt escaped shared bound: %v", err)
			}
			if _, err := other.Complete(h.ctx, completionParams("app", "", nil)); err == nil || !strings.Contains(err.Error(), "mcp_busy") {
				t.Fatalf("completion escaped shared bound: %v", err)
			}
			if _, err := other.ListTools(h.ctx, nil); err != nil {
				t.Fatalf("discovery blocked: %v", err)
			}
			if err := other.Ping(h.ctx, nil); err != nil {
				t.Fatalf("ping blocked: %v", err)
			}
			cancel()
			for range maxShortRequests {
				select {
				case <-done:
				case <-h.ctx.Done():
					t.Fatal("canceled request did not finish")
				}
			}
			awaitAdmissionEmpty(t, h.manager.admission)
			if _, err := other.Complete(h.ctx, completionParams("app", "", nil)); err != nil {
				t.Fatalf("capacity was not reusable: %v", err)
			}
		})
	}
}

func TestAdmissionWatchCapacityAndIndependentConnections(t *testing.T) {
	entered := make(chan struct{}, maxRolloutWatches)
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/deployments/release-base":
			entered <- struct{}{}
			<-r.Context().Done()
		case "/v1/apps/alpha":
			_ = json.NewEncoder(w).Encode(api.AppResponse{ID: "app-one", Slug: "alpha"})
		case "/v1/apps":
			_ = json.NewEncoder(w).Encode([]api.AppResponse{})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(b.Close)
	h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL}, "2026-07-28", true)
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	done := make(chan struct{}, maxRolloutWatches)
	for range maxRolloutWatches {
		go func() {
			defer func() { done <- struct{}{} }()
			_, _ = h.client.CallTool(ctx, &mcp.CallToolParams{Name: "gregale_watch_rollout", Arguments: map[string]any{"app": "alpha", "deployment_id": "release-base", "wait_seconds": 5}})
		}()
	}
	for range maxRolloutWatches {
		select {
		case <-entered:
		case <-h.ctx.Done():
			t.Fatal("watch did not reserve capacity")
		}
	}
	result, err := h.client.CallTool(h.ctx, &mcp.CallToolParams{Name: "gregale_watch_rollout", Arguments: map[string]any{"app": "alpha", "deployment_id": "release-base"}})
	assertBusyTool(t, result, err)
	if _, err := h.client.Complete(h.ctx, completionParams("app", "", nil)); err != nil {
		t.Fatalf("watches starved short reads: %v", err)
	}
	releases := []func(){}
	for range maxShortRequests {
		release, err := h.manager.admission.acquire(h.ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	for _, release := range releases {
		defer release()
	}
	separate := connectSubscriptionHarness(t, b, Options{APIBase: b.URL}, "2026-07-28", true)
	if _, err := separate.client.Complete(separate.ctx, completionParams("app", "", nil)); err != nil {
		t.Fatalf("connection shared another connection's capacity: %v", err)
	}
	cancel()
	for range maxRolloutWatches {
		select {
		case <-done:
		case <-h.ctx.Done():
			t.Fatal("watch cancellation did not finish")
		}
	}
	deadline := time.After(time.Second)
	for len(h.manager.admission.watches) > 0 {
		select {
		case <-deadline:
			t.Fatal("watch capacity leaked")
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestAdmissionErrorAndDeadlineRelease(t *testing.T) {
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	t.Cleanup(b.Close)
	h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL}, "2026-07-28", false)
	for range maxShortRequests + 1 {
		_, _ = h.client.CallTool(h.ctx, &mcp.CallToolParams{Name: "gregale_get_app", Arguments: map[string]string{"app": "alpha"}})
	}
	awaitAdmissionEmpty(t, h.manager.admission)
	ctx, cancel := context.WithCancel(h.ctx)
	cancel()
	if _, err := h.manager.admission.acquire(ctx, false); err == nil {
		t.Fatal("canceled request admitted")
	}
	awaitAdmissionEmpty(t, h.manager.admission)
}

func TestAdmissionSubscriptionSkipsBusyPollAndRecovers(t *testing.T) {
	f := progressFixture()
	b := httptest.NewServer(http.HandlerFunc(f.serveHTTP))
	t.Cleanup(b.Close)
	h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL}, "2026-07-28", true)
	uri := "gregale://default/apps/alpha/health"
	h.subscribe(t, uri)
	releases := []func(){}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	// Wait for any completed baseline poll before filling all short slots.
	deadline := time.Now().Add(time.Second)
	for len(releases) < maxShortRequests {
		release, err := h.manager.admission.acquire(h.ctx, false)
		if err == nil {
			releases = append(releases, release)
			continue
		}
		if time.Now().After(deadline) {
			t.Fatal("could not saturate read capacity")
		}
		time.Sleep(time.Millisecond)
	}
	f.mu.Lock()
	f.current = "changed-while-busy"
	f.mu.Unlock()
	select {
	case <-h.updates:
		t.Fatal("busy read produced notification")
	case <-time.After(80 * time.Millisecond):
	}
	if activeResourceGroups(h.manager) != 1 {
		t.Fatal("busy poll closed subscription")
	}
	for _, release := range releases {
		release()
	}
	releases = nil
	select {
	case update := <-h.updates:
		if update.URI != uri {
			t.Fatal("wrong invalidation")
		}
	case <-h.ctx.Done():
		t.Fatal("subscription did not recover after overload")
	}
	if err := h.client.Unsubscribe(h.ctx, &mcp.UnsubscribeParams{URI: uri}); err != nil {
		t.Fatal(err)
	}
	awaitResourceGroups(t, h, 0)
	awaitAdmissionEmpty(t, h.manager.admission)
}

func TestAdmissionToolDeadlineCancelsNativeRead(t *testing.T) {
	canceled := make(chan struct{}, 1)
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done(); canceled <- struct{}{} }))
	t.Cleanup(b.Close)
	h := connectSubscriptionHarness(t, b, Options{APIBase: b.URL, Timeout: 50 * time.Millisecond}, "2026-07-28", true)
	result, err := h.client.CallTool(h.ctx, &mcp.CallToolParams{Name: "gregale_get_app", Arguments: map[string]string{"app": "alpha"}})
	if err != nil || !result.IsError {
		t.Fatalf("deadline result=%+v %v", result, err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("deadline did not cancel native request")
	}
	awaitAdmissionEmpty(t, h.manager.admission)
}
