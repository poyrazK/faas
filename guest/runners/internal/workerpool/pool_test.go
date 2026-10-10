package workerpool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// chunkReader returns at most chunk bytes per Read so tests can place the
// marker across a read boundary.
type chunkReader struct {
	data  []byte
	chunk int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p[:min(len(p), r.chunk)], r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestContainsMarkerAcrossReadBoundaries(t *testing.T) {
	data := append(bytes.Repeat([]byte{0}, 64*1024-5), []byte(protocolMarker)...)
	data = append(data, bytes.Repeat([]byte{0}, 1024)...)
	for _, chunk := range []int{1, 7, 4096, 64 * 1024, len(data)} {
		got, err := containsMarker(&chunkReader{data: data, chunk: chunk})
		if err != nil || !got {
			t.Fatalf("chunk %d: found %v, err %v", chunk, got, err)
		}
	}
	partial := bytes.Repeat([]byte(protocolMarker[:len(protocolMarker)-1]+"_"), 5000)
	if got, err := containsMarker(bytes.NewReader(partial)); err != nil || got {
		t.Fatalf("partial markers: found %v, err %v", got, err)
	}
}

func TestMarkerAnywhereFindsCompiledHandlerMarker(t *testing.T) {
	dir := t.TempDir()
	compiled := dir + "/compiled"
	body := append(bytes.Repeat([]byte{0x7f}, 128*1024), []byte(protocolMarker)...)
	if err := os.WriteFile(compiled, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if supportsPersistentProtocol(Spec{HandlerPath: compiled}) {
		t.Fatal("prefix scan must not see a marker past 4 KiB")
	}
	supportCache.Delete(compiled)
	if !supportsPersistentProtocol(Spec{HandlerPath: compiled, MarkerAnywhere: true}) {
		t.Fatal("whole-file scan missed the marker")
	}
	legacy := dir + "/legacy"
	if err := os.WriteFile(legacy, bytes.Repeat([]byte{0x7f}, 128*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	if supportsPersistentProtocol(Spec{HandlerPath: legacy, MarkerAnywhere: true}) {
		t.Fatal("whole-file scan reported a marker in a legacy binary")
	}
}
