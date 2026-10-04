//go:build !no_pg

// adr: 531
package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trafficdeadline"
	"google.golang.org/grpc"
)

func TestRunWithDepsPublishesActualTrafficWiringAndRetires(t *testing.T) {
	t.Setenv("FAAS_CONSUMER_USAGE_OUTBOX_ROOT", t.TempDir())
	t.Setenv("FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL", "")
	t.Setenv("FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL_FILE", "")
	t.Setenv("FAAS_GATEWAY_CIRCUIT_BREAKER", "true")
	// The production startup gate requires a compatible usage receiver before
	// listeners bind. Keep that gate intact and serve its real gRPC protocol.
	dir, err := os.MkdirTemp("/tmp", "gregale-traffic-usage-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "usage.sock")
	usageListener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	usageServer := grpc.NewServer()
	apidpb.RegisterRequestTelemetryServer(usageServer, &usageReceiverForTest{events: make(map[string]int)})
	go func() { _ = usageServer.Serve(usageListener) }()
	t.Cleanup(usageServer.Stop)
	t.Setenv("FAAS_APID_REQUEST_TELEMETRY_TARGET", "")
	t.Setenv("FAAS_APID_REQUEST_TELEMETRY_SOCKET", socket)
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	role, gatewayURL := "compute-only", "tcp://127.0.0.1:9090"
	node, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: "traffic-daemon-" + uuid.NewString(), TargetURL: "unix:///run/vmmd.sock", VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512, VCPUBudget: 1, Active: true, Role: &role, GatewayTargetURL: &gatewayURL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.DeleteComputeNode(context.Background(), node.ID) })
	var previousGeneration int64
	for _, tc := range []struct {
		name, mode, flag, retryMode string
		retry, signing              bool
	}{
		{"shared counters with disabled retry", "central", "false", "shared", false, false},
		{"local counters with enabled retry", "local", "true", "local", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_GATEWAY_RETRY", tc.flag)
			deps := defaultDeps()
			deps.config = &Config{NodeName: node.Name, RateLimit: TOMLRateLimitConfig{Mode: tc.mode}}
			deps.pool, deps.pgStore = pool, store
			deps.syntheticDispatcher = &synthAdapter{store: store}
			policyDispatcher := &daemonIngressPolicyDispatcher{}
			deps.synth = gateway.NewSynthServer(filepath.Join(dir, "synth.sock"), policyDispatcher, discardLogger())
			deps.synth.WithInternalSvcVerifier(daemonIngressPolicyVerifier{})
			deps.capCheck = func() error { return nil }
			deps.backend = &fixedBackend{}
			deps.edgeRulesMatcher = newGatewaydEdgeRules(&fakeEdgeRuleStore{}, nil, nil, nil)
			if tc.signing {
				deps.trafficDeadlines, err = trafficdeadline.New(bytes.Repeat([]byte{1}, 32), nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			deps.listen = func(string, string) (net.Listener, error) { return listener, nil }
			deps.controlAddr = "127.0.0.1:0"
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			done := make(chan error, 1)
			go func() { done <- runWithDeps(ctx, discardLogger(), deps) }()
			var observed state.ServingGatewayTrafficRuntime
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				rows, err := store.ListServingGatewayTrafficRuntime(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for _, row := range rows {
					if row.NodeName == node.Name {
						observed = row
					}
				}
				if observed.Generation > previousGeneration && !observed.ReportedAt.IsZero() {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("daemon exited before report: %v", err)
				case <-time.After(20 * time.Millisecond):
				}
			}
			requestCtx, cancelRequest := context.WithTimeout(t.Context(), 2*time.Second)
			if deps.syntheticDispatcher.trafficRevocations == nil {
				t.Fatal("synthetic dispatch did not receive the startup security registry")
			}
			request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://"+listener.Addr().String()+"/anything", nil)
			if err != nil {
				cancelRequest()
				t.Fatal(err)
			}
			response, err := http.DefaultClient.Do(request)
			cancelRequest()
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusNotFound {
				t.Fatalf("wired listener response = %d", response.StatusCode)
			}
			assertDaemonSyntheticIngressPolicy(t, "http://"+listener.Addr().String(), pool, store, policyDispatcher)
			cancel()
			select {
			case err := <-done:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("daemon did not stop")
			}
			if observed.Generation <= previousGeneration || observed.ReportedAt.IsZero() || observed.RetryEnabled != tc.retry || observed.RateCounterMode != tc.mode || observed.RetryCounterMode != tc.retryMode || observed.DeadlineSigning != tc.signing || !observed.PolicySnapshot || !observed.SecurityRevocation || observed.ManagedHTTP || observed.ManagedCircuit {
				t.Fatalf("actual daemon wiring = %+v", observed)
			}
			previousGeneration = observed.Generation
			rows, err := store.ListServingGatewayTrafficRuntime(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				if row.NodeName == node.Name && !row.ReportedAt.IsZero() {
					t.Fatalf("shutdown retained fresh observation: %+v", row)
				}
			}
		})
	}
	// A failed listener bind must not allocate a new observation generation.
	before, err := store.ReadGatewayTrafficEpoch(t.Context(), node.Name)
	if err != nil {
		t.Fatal(err)
	}
	deps := defaultDeps()
	deps.config = &Config{NodeName: node.Name, RateLimit: TOMLRateLimitConfig{Mode: "local"}}
	deps.pool, deps.pgStore = pool, store
	deps.capCheck = func() error { return nil }
	bindError := errors.New("listener bind refused")
	deps.listen = func(string, string) (net.Listener, error) { return nil, bindError }
	if err := runWithDeps(t.Context(), discardLogger(), deps); !errors.Is(err, bindError) {
		t.Fatalf("failed bind = %v", err)
	}
	after, err := store.ReadGatewayTrafficEpoch(t.Context(), node.Name)
	if err != nil || after != before {
		t.Fatalf("failed bind changed generation: before=%+v after=%+v err=%v", before, after, err)
	}
}
