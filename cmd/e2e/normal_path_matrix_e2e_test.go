package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestE2E_NormalPath_ConcurrentRequestsPreserveIsolation closes the gap
// between single-request bridge checks and the production burst shape. Every
// request must keep its own path, body, and customer header while multiple
// ForwardHTTPStream calls are active on the same instance.
func TestE2E_NormalPath_ConcurrentRequestsPreserveIsolation(t *testing.T) {
	f := newNormalPathFixture(t, "normal-concurrent")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f.ctx, f.store, f.app.ID, f.nodeID, "concurrent")
	f.vmmd.SetVersion(instance.ID, "concurrent")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:concurrent\n", 10*time.Second)

	const requestCount = 2 // Two held streams are enough to prove overlap while keeping this shard fast.
	gate := f.vmmd.InstallRequestGate(instance.ID, requestCount)
	defer gate.Release()
	for i := 0; i < requestCount; i++ {
		path := fmt.Sprintf("/concurrent/%d?case=%d", i, i)
		f.vmmd.SetResponseForPath(instance.ID, path, normalPathResponse{
			status:  http.StatusOK,
			headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "text/plain"}},
			body:    []byte(fmt.Sprintf("response-%d\n", i)),
		})
	}

	type result struct {
		index  int
		status int
		body   []byte
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, requestCount)
	client := *f.h.HTTPClient()
	for i := 0; i < requestCount; i++ {
		i := i
		go func() {
			<-start
			path := fmt.Sprintf("/concurrent/%d?case=%d", i, i)
			payload := []byte(fmt.Sprintf(`{"request":%d,"body":"%s"}`, i, strings.Repeat("x", i+1)))
			ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.h.GatewayURL+path, bytes.NewReader(payload))
			if err != nil {
				results <- result{index: i, err: err}
				return
			}
			req.Host = f.host
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Request-Case", fmt.Sprintf("case-%d", i))
			resp, err := client.Do(req)
			if err != nil {
				results <- result{index: i, err: err}
				return
			}
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			results <- result{index: i, status: resp.StatusCode, body: body, err: readErr}
		}()
	}
	close(start)
	if !gate.WaitArrived(5 * time.Second) {
		t.Fatal("concurrent requests did not reach the bridge together")
	}

	captures := f.vmmd.Requests()
	seen := make(map[string]normalPathRequestCapture, requestCount)
	for _, capture := range captures {
		if strings.HasPrefix(capture.Init.GetRequestUri(), "/concurrent/") {
			seen[capture.Init.GetRequestUri()] = capture
		}
	}
	if len(seen) != requestCount {
		t.Fatalf("bridge captured %d concurrent requests, want %d: %#v", len(seen), requestCount, seen)
	}
	for i := 0; i < requestCount; i++ {
		path := fmt.Sprintf("/concurrent/%d?case=%d", i, i)
		capture, ok := seen[path]
		if !ok {
			t.Fatalf("bridge did not capture request %q", path)
		}
		if capture.Init.GetMethod() != http.MethodPost {
			t.Errorf("%s method=%q, want POST", path, capture.Init.GetMethod())
		}
		if !hasNormalPathHeader(capture.Init, "X-Request-Case", fmt.Sprintf("case-%d", i)) {
			t.Errorf("%s lost its customer header", path)
		}
		wantBody := []byte(fmt.Sprintf(`{"request":%d,"body":"%s"}`, i, strings.Repeat("x", i+1)))
		if !bytes.Equal(capture.Body, wantBody) {
			t.Errorf("%s body=%q, want %q", path, capture.Body, wantBody)
		}
	}

	gate.Release()
	for i := 0; i < requestCount; i++ {
		select {
		case got := <-results:
			if got.err != nil {
				t.Errorf("request %d failed: %v", got.index, got.err)
				continue
			}
			if got.status != http.StatusOK || string(got.body) != fmt.Sprintf("response-%d\n", got.index) {
				t.Errorf("request %d response=(%d,%q), want 200/isolated body", got.index, got.status, got.body)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent request did not complete after bridge release")
		}
	}
}

// TestE2E_NormalPath_PerInstanceBackpressureReleasesSlot pins the gateway's
// per-instance concurrency boundary. A full Free-plan instance must not
// receive a fifth bridge call, and the waiting request must proceed once the
// active responses release their slots.
func TestE2E_NormalPath_PerInstanceBackpressureReleasesSlot(t *testing.T) {
	f := newNormalPathFixtureWithPlan(t, "normal-backpressure", api.PlanFree)
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f.ctx, f.store, f.app.ID, f.nodeID, "backpressure")
	f.vmmd.SetVersion(instance.ID, "backpressure")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:backpressure\n", 10*time.Second)

	const slotCount = 4 // Free's published per-instance limit.
	gate := f.vmmd.InstallRequestGate(instance.ID, slotCount)
	defer gate.Release()
	client := *f.h.HTTPClient()
	request := func(path string) <-chan normalPathHTTPResult {
		result := make(chan normalPathHTTPResult, 1)
		go func() {
			ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.h.GatewayURL+path, nil)
			if err != nil {
				result <- normalPathHTTPResult{err: err}
				return
			}
			req.Host = f.host
			resp, err := client.Do(req)
			if err != nil {
				result <- normalPathHTTPResult{err: err}
				return
			}
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			result <- normalPathHTTPResult{status: resp.StatusCode, body: body, err: readErr}
		}()
		return result
	}

	active := make([]<-chan normalPathHTTPResult, 0, slotCount)
	for i := 0; i < slotCount; i++ {
		active = append(active, request(fmt.Sprintf("/backpressure/active/%d", i)))
	}
	if !gate.WaitArrived(5 * time.Second) {
		t.Fatal("active requests did not fill the bridge slots")
	}
	waiter := request("/backpressure/waiter")
	deadline := time.Now().Add(750 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, capture := range f.vmmd.Requests() {
			if strings.HasPrefix(capture.Init.GetRequestUri(), "/backpressure/waiter") {
				t.Fatal("waiter reached the bridge while all instance slots were full")
			}
		}
		time.Sleep(25 * time.Millisecond)
	}

	gate.Release()
	for i, resultCh := range active {
		select {
		case got := <-resultCh:
			if got.err != nil || got.status != http.StatusOK || string(got.body) != "normal-path:backpressure\n" {
				t.Errorf("active request %d response=(status=%d,body=%q,err=%v), want 200/backpressure", i, got.status, got.body, got.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("active request %d did not complete after releasing the instance slots", i)
		}
	}
	select {
	case got := <-waiter:
		if got.err != nil || got.status != http.StatusOK || string(got.body) != "normal-path:backpressure\n" {
			t.Errorf("waiter response=(status=%d,body=%q,err=%v), want 200/backpressure", got.status, got.body, got.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter did not complete after releasing the instance slots")
	}
}

type normalPathHTTPResult struct {
	status int
	body   []byte
	err    error
}

// TestE2E_NormalPath_AppProtocolMatrix proves that the app-level protocol
// selector survives the real apid → Postgres → gateway → VMMD path for all
// supported values. The grpc case also checks that trailer metadata remains a
// trailer rather than being flattened into the initial response headers.
func TestE2E_NormalPath_AppProtocolMatrix(t *testing.T) {
	f := newNormalPathFixture(t, "normal-protocol-matrix")
	if f == nil {
		return
	}
	for _, tc := range []struct {
		name     string
		protocol string
	}{
		{name: "http1", protocol: api.AppProtocolHTTP1},
		{name: "http2", protocol: api.AppProtocolHTTP2},
		{name: "grpc", protocol: api.AppProtocolGRPC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			protocol := tc.protocol
			app := createNormalPathApp(t, f, "normal-protocol-"+tc.name, &protocol)
			_, instance := createNormalPathLiveDeployment(t, f.ctx, f.store, app.ID, f.nodeID, tc.name)
			f.vmmd.SetVersion(instance.ID, tc.name)
			host := app.Slug + ".apps.test.example"
			waitForNormalPathResponse(t, f.h, host, "normal-path:"+tc.name+"\n", 10*time.Second)
			path := "/protocol/" + tc.name
			f.vmmd.SetResponseForPath(instance.ID, path, normalPathResponse{
				status:  http.StatusOK,
				headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "application/grpc+proto"}, {Name: "X-Protocol-Response", Value: tc.protocol}},
				trailers: func() []*vmmdpb.Header {
					if tc.protocol == api.AppProtocolGRPC {
						return []*vmmdpb.Header{{Name: "grpc-status", Value: "0"}}
					}
					return nil
				}(),
				body: []byte("protocol-ok\n"),
			})

			req, err := http.NewRequestWithContext(f.ctx, http.MethodPost, f.h.GatewayURL+path, strings.NewReader("request-body"))
			if err != nil {
				t.Fatalf("new %s request: %v", tc.protocol, err)
			}
			req.Host = host
			req.Header.Set("Content-Type", "application/grpc+proto")
			req.Header.Set("X-Protocol-Case", tc.protocol)
			resp, err := f.h.HTTPClient().Do(req)
			if err != nil {
				t.Fatalf("%s request: %v", tc.protocol, err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatalf("read %s response: %v", tc.protocol, err)
			}
			if resp.StatusCode != http.StatusOK || string(body) != "protocol-ok\n" {
				t.Fatalf("%s response=(%d,%q), want 200/protocol-ok", tc.protocol, resp.StatusCode, body)
			}
			if tc.protocol == api.AppProtocolGRPC && resp.Trailer.Get("grpc-status") != "0" {
				t.Fatalf("grpc-status trailer=%q, want 0", resp.Trailer.Get("grpc-status"))
			}

			var capture *normalPathRequestCapture
			for _, candidate := range f.vmmd.Requests() {
				if candidate.Init.GetInstance() == instance.ID && candidate.Init.GetRequestUri() == path {
					candidateCopy := candidate
					capture = &candidateCopy
				}
			}
			if capture == nil {
				t.Fatalf("no %s bridge capture for %s", tc.protocol, path)
			}
			if capture.Init.GetAppProtocol() != tc.protocol {
				t.Fatalf("bridge app_protocol=%q, want %q", capture.Init.GetAppProtocol(), tc.protocol)
			}
			if !hasNormalPathHeader(capture.Init, "X-Protocol-Case", tc.protocol) {
				t.Fatalf("%s customer header did not reach bridge", tc.protocol)
			}
		})
	}
}

// TestE2E_NormalPath_GuestHopByHopHeadersAreNotExposed pins the response
// boundary in the real daemon path. Guest connection-management headers are
// meaningful only on the bridge hop and must not leak to customers.
func TestE2E_NormalPath_GuestHopByHopHeadersAreNotExposed(t *testing.T) {
	f := newNormalPathFixture(t, "normal-response-headers")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f.ctx, f.store, f.app.ID, f.nodeID, "response-headers")
	f.vmmd.SetVersion(instance.ID, "response-headers")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:response-headers\n", 10*time.Second)
	f.vmmd.SetResponseForPath(instance.ID, "/response-headers", normalPathResponse{
		status: http.StatusOK,
		headers: []*vmmdpb.Header{
			{Name: "Connection", Value: "keep-alive"},
			{Name: "Keep-Alive", Value: "timeout=5"},
			{Name: "Proxy-Authenticate", Value: "Basic realm=guest"},
			{Name: "Proxy-Authorization", Value: "guest-secret"},
			{Name: "TE", Value: "trailers"},
			{Name: "Transfer-Encoding", Value: "chunked"},
			{Name: "Upgrade", Value: "h2c"},
			{Name: "X-Guest-Visible", Value: "yes"},
		},
		body: []byte("safe-response\n"),
	})

	req, err := http.NewRequestWithContext(f.ctx, http.MethodGet, f.h.GatewayURL+"/response-headers", nil)
	if err != nil {
		t.Fatalf("new response-header request: %v", err)
	}
	req.Host = f.host
	resp, err := f.h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("response-header request: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("read response-header body: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "safe-response\n" {
		t.Fatalf("response=(%d,%q), want 200/safe-response", resp.StatusCode, body)
	}
	for _, name := range []string{"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		if value := resp.Header.Get(name); value != "" {
			t.Errorf("hop-by-hop response header %s=%q leaked to customer", name, value)
		}
	}
	if got := resp.Header.Get("X-Guest-Visible"); got != "yes" {
		t.Fatalf("X-Guest-Visible=%q, want yes", got)
	}
}

func createNormalPathApp(t *testing.T, f *normalPathFixture, slug string, protocol *string) api.AppResponse {
	t.Helper()
	request := api.CreateAppRequest{
		Slug:         slug,
		Type:         string(state.AppTypeApp),
		RequireAuthn: boolPtr(false),
		AppProtocol:  protocol,
	}
	body, statusCode := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps", request)
	if statusCode != http.StatusCreated {
		t.Fatalf("create protocol app %q: status=%d body=%s", slug, statusCode, body)
	}
	var app api.AppResponse
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode protocol app %q: %v", slug, err)
	}
	return app
}
