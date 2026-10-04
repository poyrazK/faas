// adr: 531
package vmmdgrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// App policy, source identity and warm placement are fixture-owned. Both
// customer handlers and their transport to the production vmmd gate are real.
// The app's Scale plan deliberately differs from the VM's trusted Free plan.
type nodeAdmissionSurfaceBackend struct {
	port      int
	evictions atomic.Int32
}

func (*nodeAdmissionSurfaceBackend) Lookup(_ context.Context, host string) (gateway.App, bool) {
	id := "node-app"
	if host == "untrusted.node.test" {
		id = "node-untrusted"
	}
	return gateway.App{ID: id, AccountID: "node-account", Plan: api.PlanScale,
		PublicAuth: gateway.PublicAuthConfig{Mode: "open"}, WebSocketEnabled: true, RequestTimeoutS: 300}, true
}

func (b *nodeAdmissionSurfaceBackend) Pick(appID string) gateway.PickResult {
	instance := "vm"
	if appID == "node-untrusted" {
		instance = "untrusted"
	}
	return gateway.PickResult{OK: true, Target: gateway.Target{AppID: appID, DeploymentID: "dep", NodeID: "node", InstanceID: instance, Port: b.port, AddedAt: time.Now()}}
}
func (*nodeAdmissionSurfaceBackend) HealthyCount(string) int { return 1 }
func (*nodeAdmissionSurfaceBackend) Admit(context.Context, string, string, string, string, int) (string, gateway.WakeMethod, bool, error) {
	return "", gateway.WakeMethodUnspecified, false, errors.New("warm fixture must not wake")
}
func (*nodeAdmissionSurfaceBackend) LookupMirrorRules(context.Context, string) ([]gateway.MirrorRuleRow, bool) {
	return nil, false
}
func (*nodeAdmissionSurfaceBackend) ScheduleMirror(context.Context, string, string, string) (string, string, error) {
	return "", "", nil
}
func (b *nodeAdmissionSurfaceBackend) EvictInstance(string, string) { b.evictions.Add(1) }
func (b *nodeAdmissionSurfaceBackend) ServiceEndpoints(_ context.Context, appID string) (gateway.ServiceEndpointsSnapshot, error) {
	target := b.Pick(appID).Target
	return gateway.ServiceEndpointsSnapshot{AppID: appID, Endpoints: []gateway.ServiceEndpoint{{InstanceID: target.InstanceID,
		NodeID: target.NodeID, DeploymentID: target.DeploymentID, Port: target.Port}}}, nil
}

func nodeAdmissionSurfaceHandler(client nodeAdmissionClient, port int) http.Handler {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	backend := &nodeAdmissionSurfaceBackend{port: port}
	forward := gateway.ForwardingReverseProxy(client, log)
	raw := gateway.ForwardingRawReverseProxy(client, log, nil)
	public := gateway.NewHandlerWith(backend, gateway.NewMetrics(), log).WithForwarding(forward).WithRawForwarding(raw).
		WithRetryEnabled(true).WithRetryDefault(gateway.RetryPolicy{MaxAttempts: 3})
	service := gateway.NewServiceProxy(gateway.ServiceProxyConfig{Provider: backend, Forward: forward, RawForward: raw,
		RetryPolicy: gateway.RetryPolicy{MaxAttempts: 3}, Log: log,
		ResolveCaller: func(context.Context, string) (string, error) { return "node-caller", nil },
		Resolve: func(_ context.Context, caller, name string) (gateway.ServiceTarget, bool, error) {
			if caller != "node-caller" || (name != "service" && name != "untrusted") {
				return gateway.ServiceTarget{}, false, nil
			}
			id := "node-app"
			if name == "untrusted" {
				id = "node-untrusted"
			}
			return gateway.ServiceTarget{AppID: id, WebSocketEnabled: true}, true, nil
		},
		Authorize: func(_ context.Context, caller, target string) (gateway.ServiceCaller, error) {
			if caller != "node-caller" || (target != "node-app" && target != "node-untrusted") {
				return gateway.ServiceCaller{}, gateway.ErrServiceProxyDenied
			}
			return gateway.ServiceCaller{AppID: caller, AccountID: "node-account"}, nil
		}})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/fixture/placements":
			_ = json.NewEncoder(w).Encode(map[string]int32{"evictions": backend.evictions.Load()})
		case strings.HasPrefix(r.URL.Path, "/v1/internal/services/"):
			service.ServeHTTP(w, r)
		default:
			public.ServeHTTP(w, r)
		}
	})
}

func nodeAdmissionSurfaceRequest(ctx context.Context, process *nodeAdmissionProcess, surface, path string, untrusted, upgrade bool) (*http.Request, error) {
	host := "normal.node.test"
	if untrusted {
		host = "untrusted.node.test"
	}
	if surface == "managed" {
		service := "service"
		if untrusted {
			service = "untrusted"
		}
		path = "/v1/internal/services/" + service + path
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, process.endpoint+path, nil)
	if err != nil {
		return nil, err
	}
	request.Host = host
	request.Header.Set("X-Faas-Instance", "forged-instance")
	request.Header.Set(api.InstanceIDHeader, "forged-instance")
	request.Header.Set("X-Faas-Plan", "scale")
	request.Header.Set("X-Faas-Concurrency-Limit", "100000")
	if upgrade {
		request.Header.Set("Connection", "Upgrade")
		request.Header.Set("Upgrade", "websocket")
	}
	return request, nil
}

func nodeAdmissionSurfaceRefusal(t *testing.T, f nodeAdmissionProcessFixture, process *nodeAdmissionProcess, surface string, untrusted, upgrade bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	request, err := nodeAdmissionSurfaceRequest(ctx, process, surface, "/work", untrusted, upgrade)
	if err != nil {
		t.Fatal(err)
	}
	beforeRPC, beforeGuest := f.rpcCalls.Load(), f.guestCalls.Load()
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var problem api.Problem
	if err := json.NewDecoder(response.Body).Decode(&problem); err != nil {
		t.Fatal(err)
	}
	status, code := http.StatusTooManyRequests, api.CodeConcurrencyThrottled
	if untrusted {
		status, code = http.StatusServiceUnavailable, api.CodeHTTPAdmissionUnavailable
	}
	if response.StatusCode != status || problem.Code != code || response.Header.Get("Retry-After") != "1" {
		t.Fatalf("%s untrusted=%v upgrade=%v: response=%d problem=%+v", surface, untrusted, upgrade, response.StatusCode, problem)
	}
	if !untrusted && (problem.Limit == nil || *problem.Limit != 4 || problem.Observed == nil || *problem.Observed != 5) {
		t.Fatalf("%s refusal lost trusted capacity: %+v", surface, problem)
	}
	if f.rpcCalls.Load() != beforeRPC+1 || f.guestCalls.Load() != beforeGuest {
		t.Fatalf("%s refusal replayed or reached guest: RPCs=%d -> %d guests=%d -> %d", surface,
			beforeRPC, f.rpcCalls.Load(), beforeGuest, f.guestCalls.Load())
	}
}

func nodeAdmissionSurfacePlacements(t *testing.T, process *nodeAdmissionProcess) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(process.endpoint + "/fixture/placements")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var state struct{ Evictions int32 }
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil || state.Evictions != 0 {
		t.Fatalf("node refusal evicted placement: %+v %v", state, err)
	}
}

func TestNodeAdmissionPublicAndManagedSurfacesAcrossProcesses(t *testing.T) {
	f := newNodeAdmissionProcessFixture(t)
	start := func() *nodeAdmissionProcess {
		return startNodeAdmissionProcessMode(t, f.listener.Addr().String(), f.port, true)
	}
	first, second := start(), start()
	var cancels []context.CancelFunc
	results := make(chan error, 4)
	for i := range 4 {
		surface := []string{"public", "managed"}[i%2]
		path := "/hold"
		if i >= 2 {
			path = "/body-hold"
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancels = append(cancels, cancel)
		request, err := nodeAdmissionSurfaceRequest(ctx, first, surface, path, false, false)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			response, err := http.DefaultClient.Do(request)
			if response != nil {
				if response.StatusCode != http.StatusOK {
					body, _ := io.ReadAll(response.Body)
					err = fmt.Errorf("hold returned %d: %s", response.StatusCode, body)
				} else {
					_, err = io.Copy(io.Discard, response.Body)
				}
				_ = response.Body.Close()
			}
			results <- err
		}()
	}
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
		for range 4 {
			<-results
		}
	}()
	for range 4 {
		select {
		case <-f.started:
		case err := <-results:
			results <- err
			t.Fatalf("public/managed hold failed before guest: %v", err)
		case <-time.After(8 * time.Second):
			t.Fatal("four public/managed requests did not reach guest")
		}
	}
	for _, process := range []*nodeAdmissionProcess{first, second} {
		for _, surface := range []string{"public", "managed"} {
			for _, upgrade := range []bool{false, true} {
				nodeAdmissionSurfaceRefusal(t, f, process, surface, false, upgrade)
				nodeAdmissionSurfaceRefusal(t, f, process, surface, true, upgrade)
			}
		}
		nodeAdmissionSurfacePlacements(t, process)
	}
	second.stop()
	replacement := start()
	for _, surface := range []string{"public", "managed"} {
		nodeAdmissionSurfaceRefusal(t, f, replacement, surface, false, false)
	}
	nodeAdmissionSurfacePlacements(t, replacement)
	state, _ := f.owner.HTTPAdmissionStatus("vm")
	if state.Limit != 4 || state.Inflight != 4 || f.peak.Load() != 4 || f.guestCalls.Load() != 4 {
		t.Fatalf("mixed surfaces escaped trusted node cap: %+v peak=%d guest=%d", state, f.peak.Load(), f.guestCalls.Load())
	}
	for _, cancel := range cancels {
		cancel()
	}
	until := time.Now().Add(8 * time.Second)
	for {
		state, _ = f.owner.HTTPAdmissionStatus("vm")
		if state.Inflight == 0 {
			break
		}
		if time.Now().After(until) {
			t.Fatalf("mixed surface cleanup did not release node capacity: %+v", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, surface := range []string{"public", "managed"} {
		request, err := nodeAdmissionSurfaceRequest(t.Context(), replacement, surface, "/work", false, false)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || string(body) != "guest served" {
			t.Fatalf("%s recovery: status=%d body=%q err=%v", surface, response.StatusCode, body, err)
		}
	}
	t.Log("public/managed HTTP and Upgrade share trusted cap across two processes and replacement; refusals never replay, execute or evict")
}
