//go:build !no_pg

// adr: 570
package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// Only the namespace's post-MASQUERADE source address is supplied by this
// fixture. HTTP parsing, the fresh Postgres identity lookup, binding policy,
// retry admission, node dialing and forwarding remain production paths.
type fleetManagedSourceListener struct{ net.Listener }

func (l fleetManagedSourceListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return fleetManagedSourceConn{Conn: conn}, nil
}

type fleetManagedSourceConn struct{ net.Conn }

func (c fleetManagedSourceConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(10, 100, 0, 5), Port: c.Conn.RemoteAddr().(*net.TCPAddr).Port}
}

func prepareFleetManagedCaller(t *testing.T, f fleetDaemonFixture) {
	t.Helper()
	caller, err := f.store.AppByID(t.Context(), f.apps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	caller.Manifest.ServiceBindingPolicy = api.ServiceBindingPolicyDeclared
	caller.Manifest.ServiceBindings = []api.AppServiceBinding{{Binding: "GREGALE_SERVICE_RETRY_URL", Service: "fleet-retry"}}
	caller.Manifest.ServiceReliability = map[string]api.ServiceReliabilityPolicy{"fleet-retry": {MaxAttempts: 2, RetryBudgetPercent: 10}}
	if _, err := f.store.UpdateApp(t.Context(), caller.ID, state.UpdateAppParams{Manifest: &caller.Manifest}); err != nil {
		t.Fatal(err)
	}
	for index, node := range f.nodes {
		instance, err := f.store.CreateInstance(t.Context(), caller.ID, f.apps[0].Deployment, string(state.StateRunning), 128, node.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.SetInstanceRuntime(t.Context(), instance.ID, "fixture-"+instance.ID, "10.100.0.5", 20000+index); err != nil {
			t.Fatal(err)
		}
	}
}

func fleetManagedCall(t *testing.T, p *fleetDaemonProcess, requestID, claimedCaller string, want int) fleetDaemonResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.ready.ServiceEndpoint+"/v1/internal/services/fleet-retry/retry", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(api.RequestIDHeader, requestID)
	request.Header.Set(api.InstanceIDHeader, "guest-claimed-instance")
	request.Header.Set(api.TenantIDHeader, "guest-claimed-account")
	request.Header.Set(api.ImageDigestHeader, "guest-claimed-image")
	if claimedCaller != "" {
		request.Header.Set(gateway.ServiceProxyCallerAppHeader, claimedCaller)
	}
	started := time.Now()
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != want {
		t.Fatalf("managed status=%d want=%d body=%q err=%v", response.StatusCode, want, body, err)
	}
	return fleetDaemonResponse{status: response.StatusCode, body: strings.TrimSpace(string(body)), headers: response.Header.Clone(), duration: time.Since(started)}
}

func TestTrafficFleetDaemonManagedRetryIdentityAndReplacement(t *testing.T) {
	f := newFleetDaemonFixture(t)
	prepareFleetManagedCaller(t, f)
	// Service endpoint sorting is deterministic; inject the first transport
	// failure before either process starts, rather than changing a live picker.
	sort.Strings(f.apps[2].Instances)
	f.vm.badInstance = f.apps[2].Instances[0]
	start := func(node int) *fleetDaemonProcess {
		return startFleetDaemonMode(t, f.pool, f.apps, f.nodes[0].ID, f.nodes[node].Name, f.usage, true)
	}
	first, second := start(0), start(1)
	if first.ready.RetryBackendID != second.ready.RetryBackendID || first.ready.NodeName == second.ready.NodeName {
		t.Fatal("managed gateways did not report one shared retry backend")
	}
	fleetManagedCall(t, first, "denied", f.apps[1].ID, http.StatusForbidden)
	if len(f.vm.calls("/retry")) != 0 {
		t.Fatal("a spoofed caller reached forwarding")
	}
	fleetManagedCall(t, first, "first", "", http.StatusOK)
	initial := fleetDaemonRetries(t, f, 1, 1)
	fleetManagedCall(t, second, "second", "", http.StatusServiceUnavailable)
	shared := fleetDaemonRetries(t, f, 2, 1)
	first.stop(t)
	retired, err := f.store.ReadGatewayTrafficEpoch(t.Context(), first.ready.NodeName)
	if err != nil || retired.Generation != first.ready.Generation {
		t.Fatalf("managed retirement=%+v err=%v", retired, err)
	}
	third := start(0)
	if third.ready.Generation <= retired.Generation || third.ready.RetryBackendID != first.ready.RetryBackendID {
		t.Fatal("replacement did not advance ownership with the same retry backend")
	}
	fleetManagedCall(t, third, "third", "", http.StatusServiceUnavailable)
	replaced := fleetDaemonRetries(t, f, 3, 1)
	fleetManagedCall(t, second, "fourth", "", http.StatusOK)
	healthy := fleetDaemonRetries(t, f, 4, 1)
	if !initial.expires.Equal(shared.expires) || !initial.expires.Equal(replaced.expires) || !initial.expires.Equal(healthy.expires) {
		t.Fatal("managed retry verification crossed the database-owned live window")
	}
	calls, fields := f.vm.calls("/retry"), f.vm.correlations("/retry")
	wantIDs := []string{"first", "first", "second", "third", "fourth"}
	if len(calls) != 5 || len(fields) != len(calls) || f.vm.guestCalls("/retry") != 2 {
		t.Fatalf("managed RPCs=%v envelopes=%d guest=%d", calls, len(fields), f.vm.guestCalls("/retry"))
	}
	for index, envelope := range fields {
		if envelope.RequestID != wantIDs[index] || envelope.InstanceID != calls[index] || envelope.AppID != f.apps[2].ID ||
			envelope.TenantID != f.app.AccountID || envelope.DeploymentID != f.apps[2].Deployment || envelope.NodeID != f.nodes[0].ID || envelope.ImageDigest == "guest-claimed-image" {
			t.Fatalf("managed RPC #%d identity=%+v target=%s", index, envelope, calls[index])
		}
	}
	if calls[0] != f.vm.badInstance || calls[1] == f.vm.badInstance || calls[2] != f.vm.badInstance || calls[3] != f.vm.badInstance || calls[4] == f.vm.badInstance {
		t.Fatalf("managed retry targets=%v", calls)
	}
	f.vm.mu.Lock()
	headers := append([]http.Header(nil), f.vm.guestHeaders["/retry"]...)
	f.vm.mu.Unlock()
	for index, header := range headers {
		envelope := fields[[]int{1, 4}[index]]
		if header.Get(api.InstanceIDHeader) != envelope.InstanceID || header.Get(api.AppIDHeader) != envelope.AppID ||
			header.Get(api.TenantIDHeader) != envelope.TenantID || header.Get(api.ImageDigestHeader) != envelope.ImageDigest || header.Get(api.RequestIDHeader) != envelope.RequestID {
			t.Fatal("served guest headers disagree with the verified RPC target")
		}
	}
	var publicDebits int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM pg_ratelimit_counters").Scan(&publicDebits); err != nil || publicDebits != 0 {
		t.Fatalf("managed calls charged the public rate limiter: rows=%d err=%v", publicDebits, err)
	}
	t.Logf("configured managed listeners: four originals, one shared retry, five RPCs, two guest executions across three generations")
}
