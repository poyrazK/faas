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
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	Port                 int
	ReadinessSources     []string
}

type fleetDaemonSpec struct {
	Database, SearchPath, ConfigPath, NodeID string
	Apps                                     []fleetDaemonApp
	Managed                                  bool
	DNSUpstream                              string
	RepairWithoutNotifications               bool
}

type fleetDaemonReady struct {
	Endpoint          string
	ServiceEndpoint   string
	DNSUDP            string
	DNSTCP            string
	ReadinessEndpoint string
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
	if spec.RepairWithoutNotifications {
		backend.WithTargetReadinessLoader(newTargetReadinessLoader(store))
	}
	readinessNotifications := false
	expectedReady := make(map[string]map[string]bool)
	for _, app := range spec.Apps {
		expectedReady[app.ID] = make(map[string]bool)
		port := app.Port
		if port == 0 {
			port = 8080
		}
		var states map[string]map[string]state.InstanceReadiness
		if len(app.ReadinessSources) > 0 {
			states, err = store.LatestInstanceReadinessBySource(t.Context(), app.Instances)
			if err != nil {
				t.Fatal(err)
			}
		}
		for _, instance := range app.Instances {
			ready := true
			target := gateway.Target{AppID: app.ID, DeploymentID: app.Deployment,
				InstanceID: instance, NodeID: spec.NodeID, Port: port, AddedAt: time.Now()}
			if len(app.ReadinessSources) > 0 {
				readinessNotifications = true
				target.RequiresReadiness = true
				target.ReadinessGates = &gateway.ReadinessGates{RequiredSources: app.ReadinessSources, States: make(map[string]gateway.ReadinessState)}
				for source, current := range states[instance] {
					target.ReadinessGates.States[source] = gateway.ReadinessState{Ready: current.Ready, UpdatedAt: current.At, EventID: current.EventID}
				}
				for _, source := range app.ReadinessSources {
					ready = ready && states[instance][source].Ready
				}
			}
			expectedReady[app.ID][instance] = ready
			backend.RecordTarget(app.ID, target)
		}
	}
	// Warm placement is a fixture boundary. Verify its real registry view
	// before announcing readiness, including the selected deployment and port.
	for _, app := range spec.Apps {
		want := 0
		for _, ready := range expectedReady[app.ID] {
			if ready {
				want++
			}
		}
		snapshot, err := backend.ServiceEndpoints(t.Context(), app.ID)
		if err != nil || len(snapshot.Endpoints) != want || backend.CapacityCount(app.ID) != len(app.Instances) {
			t.Fatalf("warm service registry for %s: %+v err=%v", app.ID, snapshot, err)
		}
		port := app.Port
		if port == 0 {
			port = 8080
		}
		for _, endpoint := range snapshot.Endpoints {
			if endpoint.NodeID != spec.NodeID || !slices.Contains(app.Instances, endpoint.InstanceID) || !expectedReady[app.ID][endpoint.InstanceID] || endpoint.DeploymentID != app.Deployment || endpoint.Port != port {
				t.Fatalf("warm service endpoint differs from fixture: %+v app=%+v", endpoint, app)
			}
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	deps := defaultDeps()
	deps.config, deps.pool, deps.pgStore = config, pool, store
	// Outer run() loads the operator key before runWithDeps. Exercise that
	// same loader here; a configured deadline must not use a fixture signer.
	deps.trafficDeadlines, err = loadTrafficDeadlineSigner(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	deps.backend, deps.metrics, deps.edgeRulesMatcher, deps.responseCache = backend, metrics, matcher, cache
	deps.nodeCache = newNodeCache(store, nil, log, metrics)
	deps.capCheck = func() error { return nil }
	if spec.DNSUpstream != "" {
		deps.serviceDNSUpstreams = func() []string { return []string{spec.DNSUpstream} }
	}
	serviceEndpoint := make(chan string, 1)
	dnsUDP, dnsTCP := make(chan string, 1), make(chan string, 1)
	deps.listen = func(network, address string) (net.Listener, error) {
		if address == "127.0.0.1:0" {
			return listener, nil
		}
		local, err := net.Listen(network, "127.0.0.1:0")
		if err == nil && address == config.ServiceProxyListen {
			serviceEndpoint <- "http://" + local.Addr().String()
			return fleetManagedSourceListener{Listener: local}, nil
		}
		if err == nil && address == "10.100.0.1:53" {
			dnsTCP <- local.Addr().String()
			return fleetManagedSourceListener{Listener: local}, nil
		}
		return local, err
	}
	deps.listenPacket = func(network, address string) (net.PacketConn, error) {
		local, err := net.ListenPacket(network, "127.0.0.1:0")
		if err == nil && address == "10.100.0.1:53" {
			dnsUDP <- local.LocalAddr().String()
			return &fleetManagedSourcePacketConn{PacketConn: local, peers: make(map[string]net.Addr)}, nil
		}
		return local, err
	}
	deps.controlAddr = "127.0.0.1:0"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var readinessEndpoint string
	if readinessNotifications {
		invalidations := &fleetReadinessInvalidator{PGBackend: backend, drop: spec.RepairWithoutNotifications}
		subscribed, watched := make(chan struct{}), make(chan struct{})
		deps.invalidationsReady = subscribed
		go func() {
			defer close(watched)
			watchInvalidations(ctx, pool, invalidations, log, subscribed, config.NodeName)
		}()
		defer func() {
			cancel()
			select {
			case <-watched:
			case <-time.After(5 * time.Second):
				t.Error("readiness subscriber did not stop")
			}
		}()
		select {
		case <-subscribed:
		case <-time.After(8 * time.Second):
			t.Fatal("readiness subscriber did not establish LISTEN")
		}
		control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			counts := make(map[string]fleetReadinessCounts)
			for _, app := range spec.Apps {
				counts[app.ID] = fleetReadinessCounts{Healthy: backend.HealthyCount(app.ID), Capacity: backend.CapacityCount(app.ID), LastEventID: invalidations.lastEvent.Load(),
					DroppedEventID: invalidations.droppedEvent.Load(), Refresh: backend.TargetReadinessRefreshStatus()}
			}
			_ = json.NewEncoder(w).Encode(counts)
		}))
		defer control.Close()
		readinessEndpoint = control.URL
	}
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
	ready := fleetDaemonReady{Endpoint: "http://" + listener.Addr().String(), ReadinessEndpoint: readinessEndpoint, ServingGatewayTrafficRuntime: observed}
	if spec.Managed {
		select {
		case ready.ServiceEndpoint = <-serviceEndpoint:
		default:
			t.Fatal("configured managed listener was not constructed")
		}
		select {
		case ready.DNSUDP = <-dnsUDP:
		default:
			t.Fatal("configured UDP DNS listener was not constructed")
		}
		select {
		case ready.DNSTCP = <-dnsTCP:
		default:
			t.Fatal("configured TCP DNS listener was not constructed")
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(ready); err != nil {
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
	stdout bytes.Buffer
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
	return startFleetDaemonMode(t, pool, apps, nodeID, nodeName, usageSocket, false)
}

func startFleetDaemonMode(t *testing.T, pool *pgxpool.Pool, apps []fleetDaemonApp, nodeID, nodeName, usageSocket string, managed bool) *fleetDaemonProcess {
	t.Helper()
	return startFleetDaemonDNS(t, pool, apps, nodeID, nodeName, usageSocket, managed, "")
}

func startFleetDaemonDNS(t *testing.T, pool *pgxpool.Pool, apps []fleetDaemonApp, nodeID, nodeName, usageSocket string, managed bool, dnsUpstream string) *fleetDaemonProcess {
	t.Helper()
	return startFleetDaemonOptions(t, pool, apps, nodeID, nodeName, usageSocket, managed, dnsUpstream, false)
}

func startFleetDaemonOptions(t *testing.T, pool *pgxpool.Pool, apps []fleetDaemonApp, nodeID, nodeName, usageSocket string, managed bool, dnsUpstream string, repairWithoutNotifications bool) *fleetDaemonProcess {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "gatewayd.toml")
	// Omit ratelimit.mode deliberately: LoadConfig's production default must
	// select shared counters. Policy stays in Postgres rather than this TOML.
	configuration := "node_name = \"" + nodeName + "\"\n"
	if managed {
		configuration += "service_proxy_listen = \"10.100.0.1:10080\"\n"
	}
	if err := os.WriteFile(configPath, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := fleetDaemonSpec{Database: pool.Config().ConnConfig.Database, SearchPath: pool.Config().ConnConfig.RuntimeParams["search_path"],
		ConfigPath: configPath, NodeID: nodeID, Apps: apps, Managed: managed, DNSUpstream: dnsUpstream, RepairWithoutNotifications: repairWithoutNotifications}
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
	go func() {
		reader := io.TeeReader(stdout, &p.stdout)
		err := json.NewDecoder(reader).Decode(&p.ready)
		if err != nil {
			_, _ = io.Copy(io.Discard, reader)
		}
		decoded <- err
	}()
	select {
	case err := <-decoded:
		if err != nil {
			p.stop(t)
			t.Fatalf("fleet daemon readiness: %v: stdout=%s stderr=%s", err, &p.stdout, &p.stderr)
		}
	case <-time.After(10 * time.Second):
		p.stop(t)
		t.Fatal("fleet daemon readiness timed out")
	}
	if p.ready.RateCounterMode != "central" || p.ready.RetryCounterMode != "shared" || !p.ready.RetryEnabled ||
		!p.ready.PolicySnapshot || !p.ready.SecurityRevocation || p.ready.RetryBackendID == "" || p.ready.ManagedHTTP != managed {
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
