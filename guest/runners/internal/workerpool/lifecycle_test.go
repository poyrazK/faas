package workerpool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

type lifecycleRequest struct {
	Value           string `json:"value"`
	ResponseBytes   int    `json:"response_bytes"`
	ExitAfterReply  bool   `json:"exit_after_reply"`
	ExitBeforeReply bool   `json:"exit_before_reply"`
	DelayMillis     int    `json:"delay_millis"`
	InvocationDir   string `json:"invocation_dir"`
}

func lifecyclePool(t *testing.T) *pool {
	t.Helper()
	p := newPool(Spec{
		Executable: os.Args[0],
		Args:       []string{"-test.run=^TestWorkerpoolLifecycleHelper$", "--"},
		Env:        append(os.Environ(), "GO_WANT_WORKERPOOL_LIFECYCLE_HELPER=1"),
	})
	t.Cleanup(func() {
		p.mu.Lock()
		idle := append([]*worker(nil), p.idle...)
		p.mu.Unlock()
		for _, w := range idle {
			w.close()
		}
	})
	return p
}

func TestPoolSkipsExitedIdleInterpreter(t *testing.T) {
	p := lifecyclePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var first testResponse
	if err := p.invoke(ctx, lifecycleRequest{Value: "first"}, &first); err != nil {
		t.Fatal(err)
	}
	// Complete the cached interpreter's teardown while leaving its idle entry.
	// The next invocation must discard this known-dead entry before writing.
	p.mu.Lock()
	dead := p.idle[0]
	p.mu.Unlock()
	dead.close()
	var next testResponse
	if err := p.invoke(ctx, lifecycleRequest{Value: "next"}, &next); err != nil {
		t.Fatalf("reused an exited idle interpreter: %v", err)
	}
	if next.Value != "next" || next.Count != 1 || next.PID == first.PID {
		t.Fatalf("replacement response = %+v, previous PID = %d", next, first.PID)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.live != 1 || len(p.idle) != 1 {
		t.Fatalf("replacement accounting: live=%d idle=%d", p.live, len(p.idle))
	}
}

func TestPoolReapsIdleExitBeforeNextRequest(t *testing.T) {
	p := lifecyclePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	w, err := p.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p.release(w, true)
	// EOF makes the idle fixture exit itself, without calling worker.close.
	if err := w.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.done:
	case <-ctx.Done():
		t.Fatal("idle interpreter was not reaped")
	}
	if w.cmd.ProcessState == nil || !w.cmd.ProcessState.Exited() {
		t.Fatal("exit notification preceded process reaping")
	}
	var got testResponse
	if err := p.invoke(ctx, lifecycleRequest{Value: "after idle exit"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Value != "after idle exit" || got.Count != 1 || got.PID == w.cmd.Process.Pid {
		t.Fatalf("replacement response = %+v", got)
	}
}

func TestWorkerKeepsCompleteFrameReadableAfterReaping(t *testing.T) {
	p := lifecyclePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	w, err := startWorker(ctx, p.spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.close)
	if err := json.NewEncoder(w.stdin).Encode(lifecycleRequest{Value: "buffered reply", ExitAfterReply: true}); err != nil {
		t.Fatal(err)
	}
	// Wait deliberately before reading: Cmd.Wait must leave the owned stdout
	// pipe readable even when a complete frame remains in its kernel buffer.
	select {
	case <-w.done:
	case <-ctx.Done():
		t.Fatal("replying interpreter was not reaped")
	}
	var got testResponse
	if err := json.NewDecoder(w.stdout).Decode(&got); err != nil {
		t.Fatalf("lost buffered reply after process exit: %v", err)
	}
	if got.Value != "buffered reply" || got.Count != 1 {
		t.Fatalf("buffered response = %+v", got)
	}
}

func TestPoolPreservesBufferedResponseWhenInterpreterExits(t *testing.T) {
	p := lifecyclePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	want := strings.Repeat("x", 256<<10)
	for i := 0; i < 20; i++ {
		var got testResponse
		if err := p.invoke(ctx, lifecycleRequest{ResponseBytes: len(want), ExitAfterReply: true}, &got); err != nil {
			t.Fatalf("response %d truncated during process exit: %v", i, err)
		}
		if got.Value != want || got.Count != 1 {
			t.Fatalf("response %d: bytes=%d count=%d", i, len(got.Value), got.Count)
		}
		// If exit notification has not arrived yet, finish the helper teardown
		// before the next iteration. The response above must already be intact.
		p.mu.Lock()
		idle := append([]*worker(nil), p.idle...)
		p.mu.Unlock()
		for _, w := range idle {
			w.close()
		}
	}
}

func TestPoolDoesNotReplayInvocationAfterInterpreterExit(t *testing.T) {
	p := lifecyclePool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	auditDir := t.TempDir()
	if err := p.invoke(ctx, lifecycleRequest{ExitBeforeReply: true, InvocationDir: auditDir}, &testResponse{}); err == nil {
		t.Fatal("an invocation whose interpreter exited must report an error")
	}
	invocations, err := os.ReadDir(auditDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(invocations) != 1 {
		t.Fatalf("failed invocation executed %d times, want exactly once", len(invocations))
	}
	var got testResponse
	if err := p.invoke(ctx, lifecycleRequest{Value: "new invocation"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Value != "new invocation" || got.Count != 1 {
		t.Fatalf("replacement response = %+v", got)
	}
}

func TestPoolCanceledReadReleasesInterpreterSlot(t *testing.T) {
	p := lifecyclePool(t)
	warmCtx, cancelWarm := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelWarm()
	if err := p.invoke(warmCtx, lifecycleRequest{Value: "warm"}, &testResponse{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := p.invoke(ctx, lifecycleRequest{DelayMillis: 5000}, &testResponse{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled invocation: %v", err)
	}
	p.mu.Lock()
	live, idle := p.live, len(p.idle)
	p.mu.Unlock()
	if live != 0 || idle != 0 {
		t.Fatalf("canceled interpreter retained: live=%d idle=%d", live, idle)
	}
	ctx, cancelNext := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelNext()
	if err := p.invoke(ctx, lifecycleRequest{Value: "after cancellation"}, &testResponse{}); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerpoolLifecycleHelper(t *testing.T) {
	if os.Getenv("GO_WANT_WORKERPOOL_LIFECYCLE_HELPER") != "1" {
		return
	}
	encoder, decoder := json.NewEncoder(os.Stdout), json.NewDecoder(os.Stdin)
	if err := encoder.Encode(map[string]bool{"__faas_ready": true}); err != nil {
		os.Exit(2)
	}
	count := 0
	for {
		var request lifecycleRequest
		if err := decoder.Decode(&request); err != nil {
			os.Exit(0)
		}
		count++
		if request.InvocationDir != "" {
			// The parent test supplies its own TempDir. Each interpreter
			// execution leaves one marker, including failed invocations.
			if _, err := os.MkdirTemp(request.InvocationDir, "invoked-"); err != nil {
				os.Exit(2)
			}
		}
		if request.ExitBeforeReply {
			os.Exit(0)
		}
		time.Sleep(time.Duration(request.DelayMillis) * time.Millisecond)
		value := request.Value
		if request.ResponseBytes > 0 {
			value = strings.Repeat("x", request.ResponseBytes)
		}
		if err := encoder.Encode(testResponse{Value: value, Count: count, PID: os.Getpid()}); err != nil {
			os.Exit(2)
		}
		if request.ExitAfterReply {
			os.Exit(0)
		}
	}
}
