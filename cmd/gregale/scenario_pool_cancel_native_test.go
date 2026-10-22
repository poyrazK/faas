package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
)

type poolCancelMarkerWriter struct {
	marker string
	ready  chan struct{}
	once   sync.Once
}

func (w *poolCancelMarkerWriter) Write(p []byte) (int, error) {
	if strings.TrimSpace(string(p)) == w.marker {
		w.once.Do(func() { close(w.ready) })
	}
	return len(p), nil
}

func TestNativePoolCancellationRealCommandAndProxy(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	const runID = "0123456789abcdef0123456789abcdef"
	const marker = "abcdef0123456789abcdef0123456789"
	prepared, err := main.PrepareTestSuite("../../examples/scenario-checkout/gregale-pool-cancel-test.yaml", "pool-cancel", main.TestSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil {
		t.Fatal(err)
	}
	scenario := prepared[0].Scenario
	plan, err := scenario.Steps[1].Chaos.Plan()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var active atomic.Bool
	var matches atomic.Int64
	var work sync.WaitGroup
	var mu sync.Mutex
	connections := map[*chaos.TCPConn]bool{}
	accepted := make(chan struct{})
	go func() {
		defer close(accepted)
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			work.Add(1)
			go func() {
				defer work.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
				defer cancel()
				conn := chaos.NewTCPConnWithMatchObserver(ctx, raw, 6379, 1, nil, func(chaos.Rule, string, string) { matches.Add(1) })
				defer conn.Close()
				mu.Lock()
				connections[conn] = true
				if active.Load() {
					_ = conn.Update(chaos.Lease{Generation: "flight", ExpiresAt: time.Now().Add(time.Minute), Rules: plan.Rules})
				}
				mu.Unlock()
				defer func() { mu.Lock(); delete(connections, conn); mu.Unlock() }()
				payload := make([]byte, 16384)
				if _, err := io.ReadFull(raw, payload); err == nil {
					closed := make(chan struct{})
					go func() { _, _ = io.Copy(io.Discard, raw); cancel(); close(closed) }()
					_, _ = conn.Write(payload)
					_ = raw.Close()
					<-closed
				}
			}()
		}
	}()
	defer func() { _ = listener.Close(); <-accepted; work.Wait() }()
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPut:
			active.Store(true)
			_ = json.NewEncoder(w).Encode(api.InjectScenarioTestChaosResponse{Generation: "flight", RulesInstalled: 1, ExpiresAt: time.Now().Add(time.Minute)})
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.ScenarioTestChaosMatchesResponse{Generation: "flight", Matches: []api.ScenarioTestChaosMatch{{RuleID: chaos.RuleID(plan.Rules[0]), Count: matches.Load()}}})
		case http.MethodDelete:
			active.Store(false)
			mu.Lock()
			for conn := range connections {
				_ = conn.Update(chaos.Lease{})
			}
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer control.Close()
	base := startLocalPoolFixture(t, listener.Addr().(*net.TCPAddr).Port, false, runID)
	target, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	proxy, _ := main.NewTestProxy(target)
	defer proxy.Close()
	proofPath := filepath.Join(t.TempDir(), "pool-cancel-proof.json")
	env := append(main.TestCommandBaseEnv(), "GREGALE_TEST_URL="+proxy.URL, "GREGALE_TEST_RUN_ID="+runID, "GREGALE_NATIVE_CANCEL_MARKER="+marker, "GREGALE_NATIVE_POOL_CANCEL_PROOF="+proofPath)
	source, err := filepath.Abs("../../examples/scenario-checkout/native-probe")
	if err != nil {
		t.Fatal(err)
	}
	writer := &poolCancelMarkerWriter{marker: marker, ready: make(chan struct{})}
	old := (*main.OsStdout)
	(*main.OsStdout) = writer
	defer func() { (*main.OsStdout) = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	receipt := &main.TestRunReceipt{RunID: runID}
	done := make(chan error, 1)
	go func() {
		done <- main.RunTestScenarioSteps(ctx, main.NewClient(control.URL, "token"), source, proxy.URL, env, nil, runID, scenario.Steps, nil, receipt)
	}()
	select {
	case <-writer.ready:
	case err := <-done:
		t.Fatalf("checkpoint never reached: %v", err)
	case <-ctx.Done():
		cancel()
		<-done
		t.Fatal("active checkpoint timed out")
	}
	code := `import pathlib, runpy, sys
m=runpy.run_path('../../scripts/ci/scenario-restart-native.py')
m['confirm_pool_checkpoint'](pathlib.Path(sys.argv[1]),sys.argv[2])`
	if output, err := exec.CommandContext(ctx, python, "-c", code, proofPath, marker).CombinedOutput(); err != nil {
		cancel()
		<-done
		t.Fatalf("live checkpoint rejected: %v\n%s", err, output)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation cause: %v", err)
	}
	if active.Load() {
		t.Fatal("fault plan survived cancellation")
	}
	cleanupCtx, stop := context.WithTimeout(context.Background(), 7*time.Second)
	defer stop()
	if err := main.RunTestCommand(cleanupCtx, source, env, scenario.Cleanup[0]); err != nil {
		t.Fatalf("resource cleanup: %v", err)
	}
	data, err := os.ReadFile(proofPath)
	if err != nil {
		t.Fatal(err)
	}
	code = `import json, runpy, sys
m=runpy.run_path('../../scripts/ci/scenario-restart-native.py')
m['verify_pool_cancel_proof'](json.load(sys.stdin),{'run_id':sys.argv[1]},sys.argv[2])`
	command := exec.CommandContext(cleanupCtx, python, "-c", code, runID, marker)
	command.Stdin = bytes.NewReader(data)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("cleanup proof rejected: %v\n%s", err, output)
	}
	if len(receipt.Steps) != 2 || receipt.Steps[1].Chaos == nil || matches.Load() < 2 {
		t.Fatalf("fault evidence missing: %+v", receipt.Steps)
	}
}
