//go:build !no_pg

// adr: 375
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type fleetDaemonApp struct {
	ID, Deployment, Host string
	Instances            []string
}

type fleetDaemonSpec struct {
	Database, SearchPath, ConfigPath, NodeID string
	Apps                                     []fleetDaemonApp
}

type fleetDaemonReady struct {
	Endpoint string
	state.ServingGatewayTrafficRuntime
}

// The fixture supplies warm placement and local service endpoints. App,
// account, version and rule policy use real stores. runWithDeps constructs the
// rate/retry adapters, observation and HTTP wrapper chain, with production node
// dialing/forwarding. This does not run outer run() discovery or VM lifecycle.
func TestTrafficFleetDaemonProcess(t *testing.T) {
	path := os.Getenv("GREGALE_TRAFFIC_DAEMON_SPEC")
	if path == "" {
		t.Skip("subprocess helper")
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var spec fleetDaemonSpec
	if err := json.Unmarshal(encoded, &spec); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(spec.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.ConnConfig.Database = spec.Database
	if spec.SearchPath != "" {
		poolConfig.ConnConfig.RuntimeParams["search_path"] = spec.SearchPath
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store, log := state.NewPgStore(pool), discardLogger()
	router := pgRouter{store: store, appsSuffix: ".apps.gregale.dev", tenantSurfacesEnabled: func() bool { return false }}
	metrics, cache := gateway.NewMetrics(), gateway.NewResponseCache()
	matcher := newGatewaydEdgeRules(store, log, nil, metrics).withPublicHostRouter(router)
	backend := gateway.NewPGBackend(router, gateway.NewFakeScheduler(""), log).
		WithStore(weightsStoreAdapter{store: store}).WithEdgeRules(matcher).WithResponseCache(cache)
	for _, app := range spec.Apps {
		for _, instance := range app.Instances {
			backend.RecordTarget(app.ID, gateway.Target{AppID: app.ID, DeploymentID: app.Deployment,
				InstanceID: instance, NodeID: spec.NodeID, Port: 8080, AddedAt: time.Now()})
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	deps := defaultDeps()
	deps.config, deps.pool, deps.pgStore = config, pool, store
	deps.backend, deps.metrics, deps.edgeRulesMatcher, deps.responseCache = backend, metrics, matcher, cache
	deps.nodeCache = newNodeCache(store, nil, log, metrics)
	deps.capCheck = func() error { return nil }
	deps.listen = func(string, string) (net.Listener, error) { return listener, nil }
	deps.controlAddr = "127.0.0.1:0"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runWithDeps(ctx, log, deps) }()
	var observed state.ServingGatewayTrafficRuntime
	for until := time.Now().Add(8 * time.Second); time.Now().Before(until); {
		rows, err := store.ListServingGatewayTrafficRuntime(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.NodeName == config.NodeName && !row.ReportedAt.IsZero() {
				observed = row
			}
		}
		if observed.Generation != 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("daemon stopped before observation: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if observed.Generation == 0 {
		t.Fatal("daemon did not publish actual serving wiring")
	}
	if err := json.NewEncoder(os.Stdout).Encode(fleetDaemonReady{"http://" + listener.Addr().String(), observed}); err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop after stdin closed")
	}
}

type fleetDaemonProcess struct {
	ready  fleetDaemonReady
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	done   chan error
	stderr bytes.Buffer
	once   sync.Once
}

func (p *fleetDaemonProcess) stop(t *testing.T) {
	t.Helper()
	p.once.Do(func() {
		_ = p.stdin.Close()
		select {
		case err := <-p.done:
			if err != nil {
				t.Errorf("fleet daemon exit: %v: %s", err, &p.stderr)
			}
		case <-time.After(8 * time.Second):
			_ = p.cmd.Process.Kill()
			<-p.done
			t.Error("fleet daemon cleanup required a process kill")
		}
	})
}

func startFleetDaemon(t *testing.T, pool *pgxpool.Pool, apps []fleetDaemonApp, nodeID, nodeName, usageSocket string) *fleetDaemonProcess {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "gatewayd.toml")
	// Omit ratelimit.mode deliberately: LoadConfig's production default must
	// select shared counters. Policy stays in Postgres rather than this TOML.
	if err := os.WriteFile(configPath, []byte("node_name = \""+nodeName+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := fleetDaemonSpec{pool.Config().ConnConfig.Database, pool.Config().ConnConfig.RuntimeParams["search_path"], configPath, nodeID, apps}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(specPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	// Cleanup closes stdin and joins the daemon before killing a stuck child.
	// t.Context is canceled before Cleanup, so CommandContext would bypass
	// that graceful shutdown and turn every successful test into a kill.
	c := exec.Command(os.Args[0], "-test.run=^TestTrafficFleetDaemonProcess$")
	egressSocket := filepath.Join(filepath.Dir(usageSocket), "egress-"+nodeName+".sock")
	c.Env = fleetDaemonEnvironment(map[string]string{
		"GREGALE_TRAFFIC_DAEMON_SPEC": specPath, "FAAS_CONSUMER_USAGE_OUTBOX_ROOT": filepath.Join(dir, "usage"),
		"FAAS_APID_REQUEST_TELEMETRY_SOCKET": usageSocket, "FAAS_APID_REQUEST_TELEMETRY_TARGET": "",
		"FAAS_REQUEST_TELEMETRY_ENABLED": "true",
		"FAAS_GATEWAY_RETRY":             "true", "FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL": "", "FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL_FILE": "",
		"FAAS_GATEWAY_LISTEN": "127.0.0.1:0", "FAAS_GATEWAY_CIRCUIT_BREAKER": "false",
		"FAAS_GATEWAY_EGRESS_SOCKET": egressSocket,
		"FAAS_EGRESS_SOCKET":         egressSocket,
	})
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
	decoded := make(chan error, 1)
	go func() { decoded <- json.NewDecoder(stdout).Decode(&p.ready) }()
	select {
	case err := <-decoded:
		if err != nil {
			p.stop(t)
			t.Fatalf("fleet daemon readiness: %v: %s", err, &p.stderr)
		}
	case <-time.After(10 * time.Second):
		p.stop(t)
		t.Fatal("fleet daemon readiness timed out")
	}
	if p.ready.RateCounterMode != "central" || p.ready.RetryCounterMode != "shared" || !p.ready.RetryEnabled ||
		!p.ready.PolicySnapshot || !p.ready.SecurityRevocation || p.ready.RetryBackendID == "" {
		t.Fatalf("actual default daemon wiring = %+v", p.ready)
	}
	return p
}

func fleetDaemonEnvironment(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[key]; !replaced {
			environment = append(environment, entry)
		}
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}
