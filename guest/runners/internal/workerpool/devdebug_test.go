package workerpool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWorkerpoolSlowHelperProcess answers like TestWorkerpoolHelperProcess,
// but a "pause" request answers only after a delay, the way a worker stopped
// at a breakpoint resumes.
func TestWorkerpoolSlowHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_WORKERPOOL_SLOW_HELPER") != "1" {
		return
	}
	_, _ = fmt.Fprintln(os.Stdout, `{"__faas_ready":true}`)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request testRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		if request.Value == "pause" {
			time.Sleep(400 * time.Millisecond)
		}
		body, _ := json.Marshal(testResponse{Value: request.Value, PID: os.Getpid()})
		_, _ = fmt.Fprintln(os.Stdout, string(body))
	}
	os.Exit(0)
}

func stubDevDebugger(t *testing.T, debugging, attached bool) {
	t.Helper()
	oldDebugging, oldTables := devDebugging, devDebugTCPTables
	t.Cleanup(func() { devDebugging, devDebugTCPTables = oldDebugging, oldTables })
	devDebugging = func() bool { return debugging }
	table := filepath.Join(t.TempDir(), "tcp")
	body := "  sl  local_address rem_address   st\n"
	if attached {
		body += "   0: 0200000A:2405 0100000A:C350 01 00000000:00000000 00:00000000 00000000 0 0 1\n"
	}
	if err := os.WriteFile(table, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	devDebugTCPTables = []string{table}
}

func slowPool(t *testing.T) *pool {
	t.Helper()
	p := newPool(Spec{Executable: os.Args[0], Args: []string{"-test.run=TestWorkerpoolSlowHelperProcess", "--"}, Env: append(os.Environ(), "GO_WANT_WORKERPOOL_SLOW_HELPER=1")})
	t.Cleanup(func() {
		p.mu.Lock()
		idle := p.idle
		p.idle = nil
		p.mu.Unlock()
		for _, w := range idle {
			w.close()
		}
	})
	return p
}

// A debugged function keeps one worker, and a request that outlives its
// deadline while a debugger is attached (a breakpoint) does not kill it: the
// next request is served by the same, resumed process.
func TestDebuggedWorkerSurvivesAPausedRequest(t *testing.T) {
	stubDevDebugger(t, true, true)
	if workerLimit() != 1 {
		t.Fatalf("worker limit while debugging = %d, want 1", workerLimit())
	}
	p := slowPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var paused testResponse
	if err := p.invoke(ctx, testRequest{Value: "pause"}, &paused); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("paused invoke = %v, want deadline exceeded", err)
	}
	var first testResponse
	if err := p.invoke(context.Background(), testRequest{Value: "after"}, &first); err != nil {
		t.Fatalf("invoke after pause: %v", err)
	}
	var second testResponse
	if err := p.invoke(context.Background(), testRequest{Value: "again"}, &second); err != nil {
		t.Fatal(err)
	}
	if first.Value != "after" || first.PID != second.PID {
		t.Fatalf("responses %+v then %+v; want the resumed worker to keep serving", first, second)
	}
	p.mu.Lock()
	live := p.live
	p.mu.Unlock()
	if live != 1 {
		t.Fatalf("live workers = %d, want the one inspected worker", live)
	}
}

// Without a debugger attached a request past its deadline still replaces the
// worker, so a hung function is not kept forever.
func TestUndebuggedWorkerIsReplacedAfterDeadline(t *testing.T) {
	stubDevDebugger(t, true, false)
	p := slowPool(t)
	var before testResponse
	if err := p.invoke(context.Background(), testRequest{Value: "warm"}, &before); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := p.invoke(ctx, testRequest{Value: "pause"}, &testResponse{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("paused invoke = %v, want deadline exceeded", err)
	}
	var after testResponse
	if err := p.invoke(context.Background(), testRequest{Value: "after"}, &after); err != nil {
		t.Fatal(err)
	}
	if after.PID == before.PID {
		t.Fatal("a worker past its deadline with no debugger attached was kept")
	}
}

func TestWorkerLimitOutsideDebugging(t *testing.T) {
	stubDevDebugger(t, false, true)
	if workerLimit() != maxWorkers || debuggerHolds() {
		t.Fatalf("limit %d, holds %t outside debugging; want the normal pool", workerLimit(), debuggerHolds())
	}
}
