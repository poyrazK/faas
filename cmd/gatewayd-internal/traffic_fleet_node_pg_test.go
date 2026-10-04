//go:build !no_pg

// adr: 531
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficdeadline"
)

type fleetAdmissionNode struct {
	Target, Control string
	Port            int
}

type fleetAdmissionObservation struct {
	Instances map[string]struct {
		Enabled         bool
		Limit, Inflight int
		Generation      string
		Plan            string
	}
	RPCs, GuestCalls, Peak int32
	RPCInstances           map[string]int32
	Chain                  struct {
		Claims          map[string]trafficdeadline.Claims
		ClaimErrors     map[string]string
		ChildFinishedNS int64
		ChildStatus     int
		ChildError      bool
	}
}

func startFleetAdmissionNode(t *testing.T, apps []fleetDaemonApp) fleetAdmissionNode {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "vmmd-fixture")
	build := exec.CommandContext(t.Context(), "go", "test", "-c", "-p=1", "-vet=off", "-ldflags=-s -w", "-o", binary, "../../pkg/vmmdgrpc")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build vmmd fixture: %v: %s", err, out)
	}
	var instances []struct{ ID, Deployment, Plan string }
	for appIndex, app := range apps {
		plan := string(api.PlanFree)
		if appIndex == 1 {
			plan = "" // The second app has an untrusted wake plan.
		}
		for _, id := range app.Instances {
			instances = append(instances, struct{ ID, Deployment, Plan string }{id, app.Deployment, plan})
		}
	}
	encoded, err := json.Marshal(instances)
	if err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(dir, "instances.json")
	if err := os.WriteFile(specPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	c := exec.Command(binary, "-test.run=^TestNodeAdmissionVMMDProcess$")
	c.Dir, err = filepath.Abs("../../pkg/vmmdgrpc")
	if err != nil {
		t.Fatal(err)
	}
	c.Env = fleetDaemonEnvironment(map[string]string{"GREGALE_NODE_ADMISSION_VMMD_SPEC": specPath})
	p := &fleetDaemonProcess{cmd: c, done: make(chan error, 1)}
	p.stdin, err = c.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c.Stderr = &p.stderr
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.done <- c.Wait(); close(p.done) }()
	t.Cleanup(func() { p.stop(t) })
	var ready fleetAdmissionNode
	decoded := make(chan error, 1)
	go func() { decoded <- json.NewDecoder(stdout).Decode(&ready) }()
	select {
	case err := <-decoded:
		if err != nil {
			p.stop(t)
			t.Fatalf("vmmd fixture readiness: %v: %s", err, &p.stderr)
		}
	case <-time.After(30 * time.Second):
		p.stop(t)
		t.Fatal("vmmd fixture readiness timed out")
	}
	return ready
}

func observeFleetAdmission(t *testing.T, node fleetAdmissionNode) fleetAdmissionObservation {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(node.Control)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var observation fleetAdmissionObservation
	if err := json.NewDecoder(response.Body).Decode(&observation); err != nil {
		t.Fatal(err)
	}
	return observation
}

func prepareFleetAdmission(t *testing.T) (fleetDaemonFixture, fleetAdmissionNode) {
	return prepareFleetAdmissionReplicas(t, false)
}

func prepareFleetAdmissionReplicas(t *testing.T, retainReplicas bool) (fleetDaemonFixture, fleetAdmissionNode) {
	t.Helper()
	f := newFleetDaemonFixture(t)
	if !retainReplicas {
		f.apps[2].Instances = f.apps[2].Instances[:1]
	}
	node := startFleetAdmissionNode(t, f.apps)
	role, gatewayURL := "compute-only", "tcp://127.0.0.1:9090"
	f.nodes = nil
	for range 2 {
		peer, err := f.store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "admission-daemon-" + uuid.NewString(),
			TargetURL: node.Target, VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1,
			AdmissionCeilingMB: 512, VCPUBudget: 1, Active: true, Role: &role, GatewayTargetURL: &gatewayURL})
		if err != nil {
			t.Fatal(err)
		}
		f.nodes = append(f.nodes, peer)
	}
	for index := range f.apps {
		f.apps[index].Port = node.Port
		app, err := f.store.AppByID(t.Context(), f.apps[index].ID)
		if err != nil {
			t.Fatal(err)
		}
		app.Manifest.RequestTimeoutS = 300
		rps, burst, websocket := 1000, 1000, true
		if _, err := f.store.UpdateApp(t.Context(), app.ID, state.UpdateAppParams{Manifest: &app.Manifest,
			SetRequestRateLimitRPS: true, RequestRateLimitRPS: &rps, SetRequestRateLimitBurst: true, RequestRateLimitBurst: &burst,
			SetWebSocketEnabled: true, WebSocketEnabled: &websocket}); err != nil {
			t.Fatal(err)
		}
	}
	prepareFleetManagedCaller(t, f)
	caller, err := f.store.AppByID(t.Context(), f.apps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	caller.Manifest.ServiceBindings = append(caller.Manifest.ServiceBindings,
		api.AppServiceBinding{Binding: "GREGALE_SERVICE_CACHE_URL", Service: "fleet-cache"})
	caller.Manifest.ServiceReliability["fleet-cache"] = api.ServiceReliabilityPolicy{MaxAttempts: 2, RetryBudgetPercent: 10}
	if _, err := f.store.UpdateApp(t.Context(), caller.ID, state.UpdateAppParams{Manifest: &caller.Manifest}); err != nil {
		t.Fatal(err)
	}
	return f, node
}

func fleetAdmissionRequest(ctx context.Context, process *fleetDaemonProcess, app fleetDaemonApp, surface, path string, upgrade bool) (*http.Request, error) {
	endpoint := process.ready.Endpoint
	if surface == "managed" {
		endpoint = process.ready.ServiceEndpoint
		name := "fleet-retry"
		if app.Host == "fleet-cache.apps.gregale.dev" {
			name = "fleet-cache"
		}
		path = "/v1/internal/services/" + name + path
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+path, nil)
	if err != nil {
		return nil, err
	}
	request.Host = app.Host
	request.Header.Set(api.InstanceIDHeader, "forged-instance")
	request.Header.Set("X-Faas-Instance", "forged-instance")
	request.Header.Set("X-Faas-Plan", "scale")
	request.Header.Set("X-Faas-Concurrency-Limit", "100000")
	if upgrade {
		request.Header.Set("Connection", "Upgrade")
		request.Header.Set("Upgrade", "websocket")
	}
	return request, nil
}

func assertFleetAdmissionRefusal(t *testing.T, node fleetAdmissionNode, process *fleetDaemonProcess, app fleetDaemonApp, surface string, untrusted, upgrade bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	request, err := fleetAdmissionRequest(ctx, process, app, surface, "/work", upgrade)
	if err != nil {
		t.Fatal(err)
	}
	before := observeFleetAdmission(t, node)
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
		t.Fatalf("%s upgrade=%v untrusted=%v: status=%d problem=%+v", surface, upgrade, untrusted, response.StatusCode, problem)
	}
	if !untrusted && (problem.Limit == nil || *problem.Limit != 4 || problem.Observed == nil || *problem.Observed != 5) {
		t.Fatalf("trusted cap missing: %+v", problem)
	}
	after := observeFleetAdmission(t, node)
	if after.RPCs != before.RPCs+1 || after.GuestCalls != before.GuestCalls {
		t.Fatalf("refusal replayed or executed: before=%+v after=%+v", before, after)
	}
}

func TestTrafficFleetDaemonNodeAdmissionPublicAndManaged(t *testing.T) {
	f, node := prepareFleetAdmission(t)
	start := func(index int) *fleetDaemonProcess {
		return startFleetDaemonMode(t, f.pool, f.apps, f.nodes[0].ID, f.nodes[index].Name, f.usage, true)
	}
	first, second := start(0), start(1)
	var cancels []context.CancelFunc
	results := make(chan error, 4)
	for i := range 4 {
		ctx, cancel := context.WithCancel(t.Context())
		cancels = append(cancels, cancel)
		path := "/hold"
		if i >= 2 {
			path = "/body-hold"
		}
		request, err := fleetAdmissionRequest(ctx, first, f.apps[2], []string{"public", "managed"}[i%2], path, false)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			response, err := http.DefaultClient.Do(request)
			if response != nil {
				if response.StatusCode != http.StatusOK {
					body, _ := io.ReadAll(response.Body)
					err = fmt.Errorf("hold status=%d: %s", response.StatusCode, body)
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
	for until := time.Now().Add(8 * time.Second); ; {
		observed := observeFleetAdmission(t, node)
		if observed.Instances[f.apps[2].Instances[0]].Inflight == 4 && observed.GuestCalls == 4 {
			break
		}
		select {
		case err := <-results:
			results <- err
			t.Fatalf("configured hold failed before guest: %v", err)
		default:
		}
		if time.Now().After(until) {
			t.Fatalf("configured handlers did not fill node cap: %+v", observed)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, process := range []*fleetDaemonProcess{first, second} {
		for _, surface := range []string{"public", "managed"} {
			for _, upgrade := range []bool{false, true} {
				assertFleetAdmissionRefusal(t, node, process, f.apps[2], surface, false, upgrade)
				assertFleetAdmissionRefusal(t, node, process, f.apps[1], surface, true, upgrade)
			}
		}
	}
	second.stop(t)
	replacement := start(1)
	if replacement.ready.Generation <= second.ready.Generation {
		t.Fatal("replacement did not advance its serving generation")
	}
	for _, surface := range []string{"public", "managed"} {
		assertFleetAdmissionRefusal(t, node, replacement, f.apps[2], surface, false, false)
	}
	observed := observeFleetAdmission(t, node)
	trusted := observed.Instances[f.apps[2].Instances[0]]
	if !trusted.Enabled || trusted.Limit != 4 || trusted.Inflight != 4 || trusted.Plan != string(api.PlanFree) || observed.Peak != 4 || observed.GuestCalls != 4 {
		t.Fatalf("configured gateways escaped trusted cap: %+v", observed)
	}
	for _, cancel := range cancels {
		cancel()
	}
	for until := time.Now().Add(8 * time.Second); ; {
		observed = observeFleetAdmission(t, node)
		if observed.Instances[f.apps[2].Instances[0]].Inflight == 0 {
			break
		}
		if time.Now().After(until) {
			t.Fatalf("configured exchange cleanup retained capacity: %+v", observed)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, surface := range []string{"public", "managed"} {
		request, err := fleetAdmissionRequest(t.Context(), replacement, f.apps[2], surface, "/work", false)
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
			t.Fatalf("%s recovery status=%d body=%q err=%v", surface, response.StatusCode, body, err)
		}
	}
	// Caller spoofing is rejected by the configured source/declared-policy
	// chain, before the real node sees a forwarding RPC.
	before := observeFleetAdmission(t, node)
	fleetManagedCall(t, replacement, "denied-after-node-recovery", f.apps[1].ID, http.StatusForbidden)
	if after := observeFleetAdmission(t, node); after.RPCs != before.RPCs || after.GuestCalls != before.GuestCalls {
		t.Fatalf("forged caller reached node: before=%+v after=%+v", before, after)
	}
	t.Log("configured public/declared managed policies share a real node cap through bridge cleanup and gateway replacement")
}
