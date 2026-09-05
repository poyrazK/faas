package workerpool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

type testRequest struct {
	Value string `json:"value"`
}
type testResponse struct {
	Value string `json:"value"`
	Count int    `json:"count"`
	PID   int    `json:"pid"`
}

func TestInvokeIfSupportedReusesMarkedWorker(t *testing.T) {
	marker := t.TempDir() + "/handler"
	if err := os.WriteFile(marker, []byte("# "+protocolMarker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec := Spec{
		Executable:  os.Args[0],
		Args:        []string{"-test.run=TestWorkerpoolHelperProcess", "--"},
		Env:         append(os.Environ(), "GO_WANT_WORKERPOOL_HELPER=1"),
		HandlerPath: marker,
	}
	for i, value := range []string{"first", "second"} {
		var got testResponse
		handled, err := InvokeIfSupported(context.Background(), spec, testRequest{Value: value}, &got)
		if err != nil {
			t.Fatalf("invoke %d: %v", i, err)
		}
		if !handled {
			t.Fatalf("invoke %d was not handled", i)
		}
		if got.Value != value || got.Count != i+1 {
			t.Fatalf("invoke %d response = %+v", i, got)
		}
	}
}

func TestInvokeIfSupportedLeavesLegacyHandlerAlone(t *testing.T) {
	handler := t.TempDir() + "/handler"
	if err := os.WriteFile(handler, []byte("legacy protocol"), 0o600); err != nil {
		t.Fatal(err)
	}
	handled, err := InvokeIfSupported(context.Background(), Spec{HandlerPath: handler}, testRequest{}, &testResponse{})
	if err != nil || handled {
		t.Fatalf("legacy handler = handled %v, err %v", handled, err)
	}
}

func TestWorkerpoolHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_WORKERPOOL_HELPER") != "1" {
		return
	}
	_, _ = fmt.Fprintln(os.Stdout, `{"__faas_ready":true}`)
	scanner := bufio.NewScanner(os.Stdin)
	count := 0
	for scanner.Scan() {
		var request testRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			os.Exit(2)
		}
		count++
		body, _ := json.Marshal(testResponse{Value: request.Value, Count: count, PID: os.Getpid()})
		_, _ = fmt.Fprintln(os.Stdout, string(body))
	}
	os.Exit(0)
}

func TestPoolBoundsConcurrentInterpreterStarts(t *testing.T) {
	p := newPool(Spec{Executable: os.Args[0], Args: []string{"-test.run=TestWorkerpoolHelperProcess", "--"}, Env: append(os.Environ(), "GO_WANT_WORKERPOOL_HELPER=1")})
	t.Cleanup(func() {
		for _, w := range p.idle {
			w.close()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan testResponse, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var r testResponse
			if err := p.invoke(ctx, testRequest{Value: "burst"}, &r); err != nil {
				t.Errorf("invoke: %v", err)
				return
			}
			results <- r
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	pids := map[int]bool{}
	count := 0
	for r := range results {
		pids[r.PID] = true
		count++
	}
	if count != 100 {
		t.Fatalf("completed %d requests, want 100", count)
	}
	if len(pids) > maxWorkers {
		t.Fatalf("started %d interpreters, limit %d", len(pids), maxWorkers)
	}
	if p.live != len(pids) || len(p.idle) != p.live {
		t.Fatalf("unbalanced pool: live=%d idle=%d pids=%d", p.live, len(p.idle), len(pids))
	}
}

func TestPoolWaitCancellationAndFailedStartReleaseSlot(t *testing.T) {
	p := newPool(Spec{Executable: "/nonexistent/workerpool-test"})
	p.live = maxWorkers
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := p.acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire = %v", err)
	}
	p.live = 0
	for i := 0; i < maxWorkers+1; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := p.acquire(ctx)
		cancel()
		if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("failed start didn't release slot: %v", err)
		}
		if p.live != 0 {
			t.Fatalf("live=%d after failed start", p.live)
		}
	}
}
