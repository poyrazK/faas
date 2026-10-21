package main_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
)

// Exercise the CLI schedule against real TCP streams and the production chaos
// wrapper. The local control API supplies independently observed counters; this
// portable test does not qualify VM routing or a deployed gateway.
func TestDeclaredTCPCampaignRealStreamsAndCleanup(t *testing.T) {
	runLocalScenarioTCPTransport(t, "../../examples/scenario-checkout/gregale-tcp-campaign-test.yaml", "tcp-campaign", false)
}

func TestRequestDeadlineRealTCPResponseAndDrain(t *testing.T) {
	runLocalScenarioTCPTransport(t, "../../examples/scenario-checkout/gregale-request-deadlines-test.yaml", "request-deadlines", true)
}

func TestPoolExhaustionRealTCPSequenceAndCleanup(t *testing.T) {
	runLocalScenarioTCPTransport(t, "../../examples/scenario-checkout/gregale-pool-exhaustion-test.yaml", "pool-exhaustion", true)
}

func TestNativeTransportNegativeAssertionsRealTCP(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	code := `import pathlib, runpy, sys
root = pathlib.Path('../..').resolve()
m = runpy.run_path(str(root / 'scripts/ci/scenario-restart-native.py'))
m['manifests'](root, pathlib.Path(sys.argv[1]), 'native-checkout')`
	if output, err := exec.Command(python, "-c", code, directory).CombinedOutput(); err != nil {
		t.Fatalf("generate negative manifests: %v\n%s", err, output)
	}
	for _, testCase := range []struct{ name, suite string }{{"pool-queue", "pool-exhaustion"}, {"req-body", "request-deadlines"}, {"conn-gauge", "pool-exhaustion"}} {
		t.Run(testCase.name, func(t *testing.T) {
			runLocalScenarioTCPTransport(t, filepath.Join(directory, testCase.name+".yaml"), testCase.suite, true, testCase.name)
		})
	}
}

func runLocalScenarioTCPTransport(t *testing.T, manifest, suite string, pool bool, negativeCase ...string) {
	t.Helper()
	nativeCase := map[string]string{"tcp-campaign": "tcp-seq", "pool-exhaustion": "pool-full", "request-deadlines": "req-timeout"}[suite]
	negative := len(negativeCase) > 0
	if negative {
		nativeCase = negativeCase[0]
	}
	prepared, err := main.PrepareTestSuite(manifest, suite, main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil {
		t.Fatal(err)
	}
	step := prepared[0].Scenario.Steps[1]
	transportBudget := 500 * time.Millisecond
	if suite == "request-deadlines" {
		transportBudget = 2 * time.Second
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var lease chaos.Lease
	var lastGeneration string
	generation := 0
	counts := map[string]map[string]int64{}
	connections := map[*chaos.TCPConn]bool{}
	var work sync.WaitGroup
	accepting := make(chan struct{})
	observe := func(rule chaos.Rule, _ string, gen string) {
		mu.Lock()
		defer mu.Unlock()
		counts[gen][chaos.RuleID(rule)]++
	}
	go func() {
		defer close(accepting)
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			work.Add(1)
			go func() {
				defer work.Done()
				ctx, cancel := context.WithTimeout(context.Background(), transportBudget)
				defer cancel()
				conn := chaos.NewTCPConnWithMatchObserver(ctx, raw, 6379, 1, nil, observe)
				defer conn.Close()
				mu.Lock()
				connections[conn] = true
				current := lease
				if len(current.Rules) > 0 {
					counts[current.Generation][chaos.TCPAttemptID(current.Rules[0])]++
				}
				if err := conn.Update(current); err != nil {
					mu.Unlock()
					return
				}
				mu.Unlock()
				defer func() { mu.Lock(); delete(connections, conn); mu.Unlock() }()
				_ = raw.SetDeadline(time.Now().Add(transportBudget))
				payload := make([]byte, 16384)
				if _, err := io.ReadFull(raw, payload); err == nil {
					_, _ = conn.Write(payload)
				}
			}()
		}
	}()
	defer func() { _ = listener.Close(); <-accepting; work.Wait() }()
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			var request api.InjectScenarioTestChaosRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			generation++
			lastGeneration = fmt.Sprintf("tcp-campaign-%d", generation)
			lease = chaos.Lease{Generation: lastGeneration, ExpiresAt: time.Now().Add(time.Duration(request.DurationMS) * time.Millisecond)}
			for _, rule := range request.Rules {
				lease.Rules = append(lease.Rules, chaos.Rule(rule))
			}
			counts[lastGeneration] = map[string]int64{}
			for conn := range connections {
				_ = conn.Update(lease)
			}
			_ = json.NewEncoder(w).Encode(api.InjectScenarioTestChaosResponse{Generation: lastGeneration, RulesInstalled: len(lease.Rules), ExpiresAt: lease.ExpiresAt})
		case http.MethodDelete:
			lease = chaos.Lease{}
			for conn := range connections {
				_ = conn.Update(lease)
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			matches := []api.ScenarioTestChaosMatch{}
			for id, count := range counts[lastGeneration] {
				matches = append(matches, api.ScenarioTestChaosMatch{RuleID: id, Count: count})
			}
			_ = json.NewEncoder(w).Encode(api.ScenarioTestChaosMatchesResponse{Generation: lastGeneration, Matches: matches})
		}
	}))
	defer control.Close()
	var activeConnections atomic.Int64
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/connections" {
			_ = json.NewEncoder(w).Encode(map[string]any{"instance_id": "01234567-89ab-4cde-8fab-0123456789ab", "active_connections": activeConnections.Load()})
			return
		}
		if r.URL.Path != "/dependency" {
			_, _ = io.WriteString(w, `{"ready":true}`)
			return
		}
		reachable := false
		conn, err := net.DialTimeout("tcp", listener.Addr().String(), 250*time.Millisecond)
		if err == nil {
			activeConnections.Add(1)
			defer func() { _ = conn.Close(); activeConnections.Add(-1) }()
			_ = conn.SetDeadline(time.Now().Add(250 * time.Millisecond))
			payload := make([]byte, 16384)
			_, err = conn.Write(payload)
			if err == nil {
				_, err = io.ReadFull(conn, payload)
			}
			reachable = err == nil
		}
		value := "unavailable"
		if reachable {
			value = "reachable"
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"dependency": value})
	}))
	defer app.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	baseURL := app.URL
	if pool {
		baseURL = startLocalPoolFixture(t, listener.Addr().(*net.TCPAddr).Port, suite == "request-deadlines")
	}
	report, _, err := main.RunTestScenarioStepLoad(ctx, main.NewClient(control.URL, "token"), baseURL, "0123456789abcdef0123456789abcdef", nil, step, nil, nil)
	if (err != nil) != negative || report == nil {
		t.Fatalf("TCP campaign: %v %+v", err, report)
	}
	if nativeCase != "conn-gauge" && (report.ConnectionLeaks == nil || !report.ConnectionLeaks.Passed || report.ConnectionLeaks.PostDrain == nil || !report.ConnectionLeaks.PostDrain.Passed) {
		t.Fatalf("connection resources: %+v", report.ConnectionLeaks)
	}
	if pool && suite != "request-deadlines" && nativeCase != "pool-queue" && (report.PoolExhaustion == nil || !report.PoolExhaustion.Passed || report.PoolExhaustion.PostDrain == nil || !report.PoolExhaustion.PostDrain.Passed) {
		t.Fatalf("pool resources: %+v", report.PoolExhaustion)
	}
	if suite == "request-deadlines" {
		for index, phase := range report.ChaosPhases {
			if d := phase.RequestDeadlines["dependency-probe"]; d == nil || d.Passed != !(nativeCase == "req-body" && index == 1) {
				t.Fatalf("overall deadline: %+v", d)
			}
		}
	}
	if !pool && (report.OutageCycles == nil || report.OutageCycles.Completed != 4 || report.OutageCycles.Status != "passed") {
		t.Fatalf("cycles: %+v", report.OutageCycles)
	}
	for i := 1; i < len(report.ChaosPhases); i += 2 {
		fault := report.ChaosPhases[i]
		if fault.ConnectionAttempts == nil || !fault.ConnectionAttempts.Passed || fault.ConnectionAttempts.Attempts < 3 || fault.Chaos.Matches[0].Matches < 1 {
			t.Fatalf("fault counters: %+v", fault)
		}
	}
	// Validate the native gate against actual serialized load evidence. The outer
	// VM identity is supplied only on native hosts and is checked separately there.
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("Python is required to verify native transport report contracts")
	}
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	code := `import json, runpy, sys
m = runpy.run_path('../../scripts/ci/scenario-restart-native.py')
p = {'passed': True, 'status': 200}
r = {'steps': [{'status': 'passed', 'requests': [dict(p, name='ready')]}, {'status': 'passed', 'load': json.load(sys.stdin)}, {'requests': [dict(p, name='dependency-restored')]}], 'service_hot': {'payment': {'no_new_wake': True}}}
m['verify_transport_report'](r, sys.argv[1])`
	command := exec.CommandContext(ctx, python, "-c", code, nativeCase)
	command.Stdin = bytes.NewReader(payload)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native gate rejected actual transport report: %v\n%s", err, output)
	}
	mu.Lock()
	active := len(lease.Rules)
	mu.Unlock()
	if active != 0 {
		t.Fatal("campaign left an active TCP plan")
	}
	// The listener stops accepting before Wait, then every stream and timer joins.
	_ = listener.Close()
	<-accepting
	work.Wait()
	mu.Lock()
	remaining := len(connections)
	mu.Unlock()
	if remaining != 0 {
		t.Fatalf("leaked %d TCP streams", remaining)
	}
}

func startLocalPoolFixture(t *testing.T, dependencyPort int, deadline bool, cancellationNamespace ...string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the local pool fixture")
	}
	source, err := filepath.Abs("../../examples/scenario-checkout/native-probe/pool-server.js")
	if err != nil {
		t.Fatal(err)
	}
	script := `const {createBoundedConnectionPool}=await import(process.argv[1]);
const fixture=createBoundedConnectionPool({dependencyHost:"127.0.0.1",dependencyPort:Number(process.argv[2]),overallDeadlineMS:Number(process.argv[3]),cancellationFixture:process.argv[4]!=="",namespace:process.argv[4]||undefined});
fixture.server.listen(0,"127.0.0.1",()=>console.log(fixture.server.address().port));
process.once("SIGTERM",async()=>{await fixture.stop();fixture.server.closeAllConnections();fixture.server.close(()=>process.exit(0));});`
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	overall := "0"
	if deadline {
		overall = "500"
	}
	namespace := ""
	if len(cancellationNamespace) > 0 {
		namespace = cancellationNamespace[0]
	}
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", script, source, strconv.Itoa(dependencyPort), overall, namespace)
	cmd.Env = main.TestCommandBaseEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("pool fixture exit: %v", err)
			}
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("pool fixture did not join shutdown")
		}
		cancel()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("pool fixture startup: %v", err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || port < 1 || port > 65535 {
		t.Fatalf("invalid pool fixture listener: %q", line)
	}
	return "http://127.0.0.1:" + strconv.Itoa(port)
}
