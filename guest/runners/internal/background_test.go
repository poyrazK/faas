package internal

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/guest/runners/internal/workerpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestBackgroundRuntimePlanLimits(t *testing.T) {
	for _, plan := range api.Plans {
		t.Run(string(plan), func(t *testing.T) {
			r, err := NewBackgroundRuntime(plan)
			if err != nil {
				t.Fatal(err)
			}
			if r.timeout != time.Duration(plan.TailTimeoutSeconds())*time.Second || r.maxTasks != plan.TailCapMax() || r.capacity != plan.ConcurrentTailsPerInstance() {
				t.Fatal("background limits differ from the central plan table")
			}
			// Invocations share one admission counter, across every worker.
			var wg sync.WaitGroup
			accepted := make(chan *invocationBackground, r.capacity+1)
			for n := range r.capacity + 1 {
				wg.Go(func() {
					b := r.invocation(fmt.Sprint(n))
					err := b.control(&BackgroundFrame{Task: 1, Event: "register"})
					if err == nil {
						accepted <- b
					} else if !errors.Is(err, errBackgroundCapacity) {
						t.Errorf("admit: %v", err)
					}
				})
			}
			wg.Wait()
			close(accepted)
			if r.Active() != r.capacity || len(accepted) != r.capacity {
				t.Fatalf("admitted %d tasks, active %d, cap %d", len(accepted), r.Active(), r.capacity)
			}
			for b := range accepted {
				b.interrupt()
				b.interrupt() // Cleanup is idempotent, never negative.
			}
			if r.Active() != 0 {
				t.Fatalf("leaked %d admissions", r.Active())
			}
		})
	}
	if _, err := NewBackgroundRuntime(api.Plan("unknown")); err == nil {
		t.Fatal("unknown plan enabled background work")
	}
}

func TestBackgroundTaskLifetimeAndProtocol(t *testing.T) {
	r, _ := NewBackgroundRuntime(api.PlanPro)
	b := r.invocation("inv")
	for id := 1; id <= r.maxTasks; id++ {
		if err := b.control(&BackgroundFrame{Task: id, Event: "register"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.control(&BackgroundFrame{Task: r.maxTasks + 1, Event: "register"}); !errors.Is(err, errBackgroundCapacity) {
		t.Fatalf("per-invocation limit: %v", err)
	}
	for _, frame := range []*BackgroundFrame{nil, {Task: 0, Event: "register"}, {Task: 1, Event: "register"}, {Task: 99, Event: "settled", Outcome: "completed"}, {Task: 1, Event: "settled", Outcome: "timeout"}, {Task: 1, Event: "settled", Outcome: "secret"}} {
		if err := b.control(frame); err == nil {
			t.Fatalf("accepted invalid control frame %+v", frame)
		}
	}
	if err := b.finish(); err == nil {
		t.Fatal("completed execution with pending promises")
	}
	if err := b.control(&BackgroundFrame{Task: 1, Event: "timeout"}); err != nil {
		t.Fatal(err)
	}
	if r.Active() != r.maxTasks {
		t.Fatal("timeout released a still-running task")
	}
	if err := b.control(&BackgroundFrame{Task: 1, Event: "settled", Outcome: "completed"}); err == nil {
		t.Fatal("accepted successful completion after timeout")
	}
	if err := b.control(&BackgroundFrame{Task: 1, Event: "settled", Outcome: "timeout"}); err != nil {
		t.Fatal(err)
	}
	if err := b.control(&BackgroundFrame{Task: 1, Event: "register"}); err == nil {
		t.Fatal("reused a task id after settlement")
	}
	b.interrupt()
	if err := b.finish(); err != nil {
		t.Fatal(err)
	}
	if b.finish() == nil || b.control(&BackgroundFrame{Task: 100, Event: "register"}) == nil || r.Active() != 0 {
		t.Fatal("completion did not fence later controls or leaked admissions")
	}
}

func TestBackgroundPolicyCannotBeEnabledByRequest(t *testing.T) {
	req := httptest.NewRequest("POST", "/?background=1", strings.NewReader(`{"background":{"timeout_ms":999999,"max_tasks":999999}}`))
	req.Header.Set("X-Faas-Background", "1")
	req.Header.Set("X-Faas-Wait-Until-Sec", "999999")
	env := requestEnvelope(req, ServeConfig{})
	if env.Background != nil || env.RequiresCompletion() || env.WaitUntilSec != 0 {
		t.Fatalf("request enabled trusted background policy: %+v", env)
	}
}

func TestBackgroundDeadlineCannotBeStarvedByControlFrames(t *testing.T) {
	r, _ := NewBackgroundRuntime(api.PlanFree)
	b := r.invocation("deadline")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := b.next(ctx, func(context.Context) ([]byte, error) {
		t.Fatal("read buffered frames after deadline")
		return nil, nil
	})
	if !errors.Is(err, context.Canceled) || r.Active() != 0 {
		t.Fatalf("completion ignored its bounded lifetime: %v", err)
	}
}

func TestBackgroundUnsupportedOneShotDoesNotExecute(t *testing.T) {
	dir := t.TempDir()
	handler, witness := filepath.Join(dir, "handler.sh"), filepath.Join(dir, "executed")
	if err := os.WriteFile(handler, []byte("touch \"$1\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, _ := NewBackgroundRuntime(api.PlanFree)
	env := Envelope{Background: &BackgroundPolicy{TimeoutMS: runtime.timeout.Milliseconds(), MaxTasks: api.PlanFree.TailCapMax()}}
	_, err := invoke(t.Context(), workerpool.Spec{Executable: "/bin/sh", Args: []string{handler, witness}, HandlerPath: handler}, env, runtime)
	if err == nil {
		t.Fatal("negotiated background execution with an unsupported handler")
	}
	if _, err := os.Stat(witness); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsupported handler had side effects: %v", err)
	}
}

// A plan change replaces the runtime while tasks admitted under the earlier
// plan still run. They keep counting against the replacement's instance cap.
func TestBackgroundRuntimeReplacementKeepsOutstandingTasks(t *testing.T) {
	old, err := NewBackgroundRuntime(api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	for range old.capacity {
		if !old.reserve() {
			t.Fatal("old runtime refused its own capacity")
		}
	}
	next, err := old.replace(api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	if next.Active() != old.capacity || next.reserve() {
		t.Fatalf("replacement admitted beyond the instance cap: active=%d", next.Active())
	}
	old.release()
	if next.Active() != old.capacity-1 || !next.reserve() {
		t.Fatal("old task settlement did not return capacity to the replacement")
	}
	if _, err := old.replace(api.Plan("unknown")); err == nil {
		t.Fatal("replacement accepted an unknown plan")
	}
}
