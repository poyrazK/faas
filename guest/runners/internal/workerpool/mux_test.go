package workerpool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// muxLine is a shared helper worker's response line; the id goes first, as
// the generated adapters write it.
type muxLine struct {
	ID    string `json:"__faas_id"`
	Value string `json:"value,omitempty"`
	PID   int    `json:"pid,omitempty"`
	Seq   int    `json:"seq"`
}

// TestWorkerpoolMuxHelperProcess is a shared worker: it advertises
// HELPER_CONCURRENCY slots and answers requests concurrently. The request
// value picks the behaviour: "sleep:<ms>", "lines:<n>", "hang", "wedge"
// (stop reading, as a blocked event loop would), "exit", or an echo.
func TestWorkerpoolMuxHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_WORKERPOOL_MUX_HELPER") != "1" {
		return
	}
	slots, _ := strconv.Atoi(os.Getenv("HELPER_CONCURRENCY"))
	ackDelay, _ := strconv.Atoi(os.Getenv("HELPER_CANCEL_ACK_DELAY_MS"))
	var mu sync.Mutex
	write := func(v any) {
		body, _ := json.Marshal(v)
		mu.Lock()
		_, _ = os.Stdout.Write(append(body, '\n'))
		mu.Unlock()
	}
	write(map[string]any{"__faas_ready": true, "concurrency": slots})
	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			os.Exit(0)
		}
		var req struct {
			ID     string  `json:"__faas_id"`
			Cancel *string `json:"__faas_cancel"`
			Value  string  `json:"value"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			os.Exit(2)
		}
		if req.Cancel != nil {
			if ackDelay > 0 {
				go func(id string) {
					time.Sleep(time.Duration(ackDelay) * time.Millisecond)
					write(map[string]string{"__faas_cancelled": id})
				}(*req.Cancel)
			} else {
				write(map[string]string{"__faas_cancelled": *req.Cancel})
			}
			continue
		}
		switch value := req.Value; {
		case value == "wedge":
			time.Sleep(time.Hour)
		case value == "exit":
			os.Exit(3)
		case value == "hang":
		case strings.HasPrefix(value, "sleep:"):
			ms, _ := strconv.Atoi(strings.TrimPrefix(value, "sleep:"))
			go func() {
				time.Sleep(time.Duration(ms) * time.Millisecond)
				write(muxLine{ID: req.ID, Value: value, PID: os.Getpid()})
			}()
		case strings.HasPrefix(value, "lines:"):
			n, _ := strconv.Atoi(strings.TrimPrefix(value, "lines:"))
			go func() {
				for i := range n {
					write(muxLine{ID: req.ID, Seq: i, PID: os.Getpid()})
					time.Sleep(time.Millisecond)
				}
			}()
		default:
			write(muxLine{ID: req.ID, Value: value, PID: os.Getpid()})
		}
	}
}

func muxPool(t *testing.T, slots int, extraEnv ...string) (*pool, int) {
	t.Helper()
	p := newPool(Spec{
		Executable: os.Args[0],
		Args:       []string{"-test.run=^TestWorkerpoolMuxHelperProcess$", "--"},
		Env:        append(append(os.Environ(), "GO_WANT_WORKERPOOL_MUX_HELPER=1", "HELPER_CONCURRENCY="+strconv.Itoa(slots)), extraEnv...),
	})
	t.Cleanup(func() {
		p.mu.Lock()
		workers := append([]*worker(nil), p.shared...)
		p.mu.Unlock()
		for _, w := range workers {
			p.retire(w)
		}
	})
	// Prewarm, as the runner does before readiness: the pool learns the
	// worker's slots before traffic arrives.
	w, err := p.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if w.mux == nil || w.slots != slots {
		t.Fatalf("worker slots = %d (shared %v), want %d", w.slots, w.mux != nil, slots)
	}
	p.release(w, true)
	return p, w.cmd.Process.Pid
}

func (p *pool) liveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.live
}

func processRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	return err == nil && proc.Signal(syscall.Signal(0)) == nil
}

// waitLive waits for the pool to count want live workers; a retired worker
// leaves the pool from its reader goroutine.
func waitLive(t *testing.T, p *pool, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for p.liveCount() != want {
		if time.Now().After(deadline) {
			t.Fatalf("live workers = %d, want %d", p.liveCount(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ADR-1342: a worker that advertises N slots serves N requests at once in
// one process.
func TestSharedWorkerServesConcurrentRequests(t *testing.T) {
	p, pid := muxPool(t, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := time.Now()
	var wg sync.WaitGroup
	results := make(chan muxLine, 8)
	for range 8 {
		wg.Go(func() {
			var got muxLine
			if err := p.invoke(ctx, testRequest{Value: "sleep:400"}, &got); err != nil {
				t.Errorf("invoke: %v", err)
				return
			}
			results <- got
		})
	}
	wg.Wait()
	close(results)
	elapsed := time.Since(started)
	for got := range results {
		if got.PID != pid || got.Value != "sleep:400" {
			t.Fatalf("response %+v, want value sleep:400 from worker %d", got, pid)
		}
	}
	if elapsed > 2*time.Second {
		t.Fatalf("8 concurrent 400 ms requests took %s; they ran one at a time", elapsed)
	}
	if live := p.liveCount(); live != 1 {
		t.Fatalf("live workers = %d, want the one shared worker", live)
	}
}

// Once every slot is busy the pool starts another worker, up to its bound.
func TestSharedWorkersFillBeforeStarting(t *testing.T) {
	p, _ := muxPool(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var leases []*Lines
	for range 5 {
		lines, err := p.open(ctx, testRequest{Value: "hang"})
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lines)
	}
	if live := p.liveCount(); live != 3 {
		t.Fatalf("5 requests on 2-slot workers started %d workers, want 3", live)
	}
	for _, lines := range leases {
		lines.Close(true)
	}
	if live := p.liveCount(); live != 3 {
		t.Fatalf("idle shared workers were closed: live %d", live)
	}
}

// Response lines of concurrent requests interleave on stdout; each request
// reads only its own, in order.
func TestSharedWorkerRoutesInterleavedLines(t *testing.T) {
	p, _ := muxPool(t, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	const n = 50
	var wg sync.WaitGroup
	for range 3 {
		lines, err := p.open(ctx, testRequest{Value: "lines:" + strconv.Itoa(n)})
		if err != nil {
			t.Fatal(err)
		}
		wg.Go(func() {
			defer lines.Close(true)
			for i := range n {
				raw, err := lines.Next()
				if err != nil {
					t.Errorf("line %d: %v", i, err)
					return
				}
				var got muxLine
				if err := json.Unmarshal(raw, &got); err != nil || got.ID != lines.id || got.Seq != i {
					t.Errorf("request %s line %d = %s (%v)", lines.id, i, raw, err)
					return
				}
			}
		})
	}
	wg.Wait()
}

// An abandoned request is cancelled; the worker acknowledges and keeps
// serving.
func TestSharedWorkerCancelKeepsWorker(t *testing.T) {
	grace := cancelAckGrace
	cancelAckGrace = 300 * time.Millisecond
	t.Cleanup(func() { cancelAckGrace = grace })
	p, pid := muxPool(t, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	lines, err := p.open(ctx, testRequest{Value: "hang"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lines.Next(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Next = %v, want the request's deadline", err)
	}
	lines.Close(false)
	time.Sleep(2 * cancelAckGrace)
	var got muxLine
	if err := p.invoke(context.Background(), testRequest{Value: "after"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.PID != pid {
		t.Fatalf("worker %d replaced by %d after an acknowledged cancel", pid, got.PID)
	}
}

// Cleanup still consumes capacity even after the HTTP caller has gone away.
func TestSharedWorkerCancelRetainsSlotUntilCleanupAcknowledged(t *testing.T) {
	p, pid := muxPool(t, 2, "HELPER_CANCEL_ACK_DELAY_MS=300")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var requests []*Lines
	for range maxWorkers * 2 {
		lines, err := p.open(ctx, testRequest{Value: "hang"})
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, lines)
		defer lines.Close(true)
	}
	requests[0].Close(false)
	wait, stop := context.WithTimeout(ctx, 80*time.Millisecond)
	defer stop()
	if lines, err := p.open(wait, testRequest{Value: "too-early"}); !errors.Is(err, context.DeadlineExceeded) {
		if lines != nil {
			lines.Close(true)
		}
		t.Fatalf("occupied cancellation slot was reused before cleanup: %v", err)
	}
	var got muxLine
	if err := p.invoke(ctx, testRequest{Value: "after-cleanup"}, &got); err != nil || got.PID != pid {
		t.Fatalf("acknowledged slot was not reusable on its original worker: %+v, %v", got, err)
	}
}

// A worker that does not acknowledge a cancel is not running its event loop;
// it is restarted rather than handed more requests.
func TestSharedWorkerWedgedIsRestarted(t *testing.T) {
	grace := cancelAckGrace
	cancelAckGrace = 300 * time.Millisecond
	t.Cleanup(func() { cancelAckGrace = grace })
	p, pid := muxPool(t, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	lines, err := p.open(ctx, testRequest{Value: "wedge"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lines.Next(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Next = %v, want the request's deadline", err)
	}
	lines.Close(false)
	waitLive(t, p, 0)
	for deadline := time.Now().Add(5 * time.Second); processRunning(pid); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("wedged worker %d is still running", pid)
		}
	}
	var got muxLine
	if err := p.invoke(context.Background(), testRequest{Value: "after"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.PID == pid {
		t.Fatal("request reached the wedged worker")
	}
}

// A worker that exits fails its in-flight requests and leaves the pool.
func TestSharedWorkerExitFailsInFlightRequests(t *testing.T) {
	p, pid := muxPool(t, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	waiting, err := p.open(ctx, testRequest{Value: "hang"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.invoke(ctx, testRequest{Value: "exit"}, &muxLine{}); err == nil {
		t.Fatal("request to an exiting worker succeeded")
	}
	if _, err := waiting.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("in-flight request = %v, want the worker's EOF", err)
	}
	waiting.Close(false)
	waitLive(t, p, 0)
	var got muxLine
	if err := p.invoke(ctx, testRequest{Value: "after"}, &got); err != nil || got.PID == pid {
		t.Fatalf("after exit: %+v, %v", got, err)
	}
}

// ADR-687: Recycle closes an idle shared worker at once and a busy one when
// its last request leaves.
func TestRecycleClosesSharedWorkers(t *testing.T) {
	p, _ := muxPool(t, 4)
	if closed := p.recycle(); closed != 1 || p.liveCount() != 0 {
		t.Fatalf("idle shared worker: closed %d, live %d", closed, p.liveCount())
	}
	busy, err := p.open(context.Background(), testRequest{Value: "hang"})
	if err != nil {
		t.Fatal(err)
	}
	if closed := p.recycle(); closed != 0 || p.liveCount() != 1 {
		t.Fatalf("busy shared worker: closed %d, live %d", closed, p.liveCount())
	}
	var got muxLine
	if err := p.invoke(context.Background(), testRequest{Value: "fresh"}, &got); err != nil || got.PID == busy.w.cmd.Process.Pid {
		t.Fatalf("request after Recycle reached the busy pre-Recycle worker: %+v %v", got, err)
	}
	busy.Close(true)
	if live := p.liveCount(); live != 1 {
		t.Fatalf("pre-Recycle worker kept after its last request: live %d", live)
	}
}

func TestStreamBufferBound(t *testing.T) {
	s := newStream()
	big := bytes.Repeat([]byte{'x'}, streamBufferMax+1)
	s.push(big)
	if line, err := s.next(context.Background()); err != nil || len(line) != len(big) {
		t.Fatalf("a large line on an empty queue was refused: %d %v", len(line), err)
	}
	s.push([]byte("a"))
	s.push(big)
	if _, err := s.next(context.Background()); !errors.Is(err, errStreamOverflow) {
		t.Fatalf("overflow = %v, want errStreamOverflow", err)
	}
}

func TestLineIDAndWithID(t *testing.T) {
	for _, tc := range []struct {
		line    string
		id      string
		ack, ok bool
	}{
		{`{"__faas_id":"12","status":200}`, "12", false, true},
		{`{"status": 200, "__faas_id": "7"}`, "7", false, true},
		{`{"__faas_id": "3", "frame": "end"}`, "3", false, true},
		{`{"__faas_cancelled":"9"}`, "9", true, true},
		{`{"status":200}`, "", false, false},
		{`not json`, "", false, false},
	} {
		id, ack, ok := lineID([]byte(tc.line))
		if id != tc.id || ack != tc.ack || ok != tc.ok {
			t.Fatalf("lineID(%s) = %q %v %v", tc.line, id, ack, ok)
		}
	}
	for payload, want := range map[string]string{
		`{"value":"x"}`: `{"__faas_id":"4","value":"x"}`,
		`{}`:            `{"__faas_id":"4"}`,
	} {
		got, err := withID([]byte(payload), "4")
		if err != nil || string(got) != want+"\n" {
			t.Fatalf("withID(%s) = %q, %v", payload, got, err)
		}
	}
	if _, err := withID([]byte(`[1]`), "4"); err == nil {
		t.Fatal("withID accepted a non-object request")
	}
	previous := requestLineMax
	requestLineMax = 8
	t.Cleanup(func() { requestLineMax = previous })
	if _, err := withID([]byte(`{"value":"x"}`), "4"); err == nil {
		t.Fatal("withID accepted a request past the line limit")
	}
}
