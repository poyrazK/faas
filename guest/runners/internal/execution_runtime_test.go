package internal

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
)

// The transport peer is injected here; host ledger, incarnation, real socket
// credentials and pause gates have separate tests in fcvm and guest/init.
type executionWireHost struct {
	mu         sync.Mutex
	active     map[uint64]bool
	beginSeen  chan struct{}
	beginAck   <-chan struct{}
	loseBegin  bool
	loseFinish bool
	beginCalls int
}

func (h *executionWireHost) count() int { h.mu.Lock(); defer h.mu.Unlock(); return len(h.active) }

func (h *executionWireHost) dial(context.Context) (net.Conn, error) {
	a, b := net.Pipe()
	go func() {
		defer func() { _ = b.Close() }()
		req, err := functionlifetime.ReadRequest(b)
		if err != nil {
			return
		}
		h.mu.Lock()
		if h.active == nil {
			h.active = map[uint64]bool{}
		}
		drop := false
		if req.Kind == functionlifetime.Begin {
			h.active[req.Sequence] = true
			h.beginCalls++
			if h.beginCalls == 1 && h.beginSeen != nil {
				close(h.beginSeen)
			}
			if h.loseBegin {
				h.loseBegin = false
				drop = true
			}
		} else {
			delete(h.active, req.Sequence)
			if h.loseFinish {
				h.loseFinish = false
				drop = true
			}
		}
		gate := h.beginAck
		h.mu.Unlock()
		if req.Kind == functionlifetime.Begin && gate != nil {
			<-gate
		}
		if drop {
			return
		}
		_ = functionlifetime.Write(b, functionlifetime.Reply{Version: functionlifetime.Version, Status: functionlifetime.OK, Plan: api.PlanFree})
	}()
	return a, nil
}

func executionWiring(host *executionWireHost) func(*ServeConfig) {
	return func(cfg *ServeConfig) {
		runtime := NewExecutionRuntime(host.dial)
		runtime.background, runtime.plan = cfg.Background, api.PlanFree
		cfg.Execution, cfg.Background = runtime, nil
	}
}

func TestExecutionOwnershipOutlivesHTTPAndLostAcknowledgements(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run("h2="+strconv.FormatBool(h2), func(t *testing.T) {
			host := &executionWireHost{loseBegin: true, loseFinish: true}
			f := startBackgroundFunction(t, api.PlanFree, 4, h2, backgroundFunctionSource, 0, executionWiring(host))
			first := f.get(t, "/tail", "tail")
			if host.count() != 1 || f.runtime.Active() != 1 {
				t.Fatal("HTTP completion released host execution ownership")
			}
			if probe := f.get(t, "/state", "neighbor"); probe.PID != first.PID || probe.SignalAborted {
				t.Fatal("lease held HTTP open or contaminated its neighbor")
			}
			awaitBackground(t, func() bool { return host.count() == 1 })
			f.get(t, "/release?key=tail", "release")
			awaitBackground(t, func() bool { return host.count() == 0 && f.runtime.Active() == 0 })
		})
	}
}

func TestExecutionDoesNotDispatchBeforeHostAcknowledgement(t *testing.T) {
	ack := make(chan struct{})
	host := &executionWireHost{beginSeen: make(chan struct{}), beginAck: ack}
	witness := filepath.Join(t.TempDir(), "called")
	source := strings.Replace(backgroundFunctionSource, "calls++;", "calls++; fs.appendFileSync(process.env.EXECUTION_FIXTURE_WITNESS, 'called\\n');", 1)
	f := startBackgroundFunction(t, api.PlanFree, 4, false, source, 0, executionWiring(host), func(cfg *ServeConfig) { cfg.Spec.Env = append(cfg.Spec.Env, "EXECUTION_FIXTURE_WITNESS="+witness) })
	done := make(chan error, 1)
	go func() {
		resp, err := f.client.Get(f.base + "/state")
		if err == nil {
			_, err = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		done <- err
	}()
	select {
	case <-host.beginSeen:
	case <-time.After(time.Second):
		t.Fatal("host did not receive reservation")
	}
	if _, err := os.Stat(witness); !os.IsNotExist(err) {
		t.Fatal("handler ran before acknowledged VM ownership")
	}
	close(ack)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("acknowledged handler did not finish")
	}
	awaitBackground(t, func() bool { return host.count() == 0 })
	data, err := os.ReadFile(witness)
	if err != nil || string(data) != "called\n" {
		t.Fatal("handler was lost or replayed")
	}
}

func TestExecutionRegistrationFailureRetainsHostCleanupOwnership(t *testing.T) {
	host := &executionWireHost{}
	f := startBackgroundFunction(t, api.PlanFree, 4, false, backgroundFunctionSource, 0, executionWiring(host))
	for n := range 4 {
		f.get(t, "/tail?mode=file", "tail-"+strconv.Itoa(n))
	}
	resp := f.request(t, "/tail?mode=file", "refused")
	_ = resp.Body.Close()
	if resp.StatusCode != 500 || host.count() != 5 {
		t.Fatalf("refusal released execution during cancellation: status=%d, leases=%d", resp.StatusCode, host.count())
	}
	if err := os.WriteFile(f.release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	awaitBackground(t, func() bool { return host.count() == 0 && f.runtime.Active() == 0 })
}

func TestExecutionCheckpointBroadcastDrainsEveryLoadedWorker(t *testing.T) {
	for _, forbidden := range []bool{false, true} {
		t.Run("register-background="+strconv.FormatBool(forbidden), func(t *testing.T) {
			host := &executionWireHost{}
			witness := filepath.Join(t.TempDir(), "hooks")
			hook := `if(event.path==="/checkpoint") {
  if(ctx.lifecycle_hook!=="before_checkpoint") return {statusCode:403};
  fs.appendFileSync(process.env.CHECKPOINT_FIXTURE_WITNESS,process.pid+"\n");
  FORBIDDEN
  return {statusCode:204};
}`
			registration := ""
			if forbidden {
				registration = "ctx.waitUntil(Promise.resolve());"
			}
			hook = strings.Replace(hook, "FORBIDDEN", registration, 1)
			source := strings.Replace(backgroundFunctionSource, "calls++;", "calls++;"+hook, 1)
			f := startBackgroundFunction(t, api.PlanFree, 1, false, source, 0, executionWiring(host), func(cfg *ServeConfig) { cfg.Spec.Env = append(cfg.Spec.Env, "CHECKPOINT_FIXTURE_WITNESS="+witness) })
			pids := map[int]bool{}
			for n := range 4 {
				reply := f.get(t, "/tail?mode=file", "tail-"+strconv.Itoa(n))
				pids[reply.PID] = true
			}
			if len(pids) != 4 {
				t.Fatal("fixture did not populate four interpreters")
			}
			if err := os.WriteFile(f.release, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			awaitBackground(t, func() bool { return host.count() == 0 && f.runtime.Active() == 0 })
			req := httptest.NewRequest(http.MethodPost, "/checkpoint", nil)
			req.RemoteAddr = "127.0.0.1:1234"
			req.Header.Set("X-Faas-Before-Checkpoint", "1")
			rec := httptest.NewRecorder()
			Serve(rec, req, f.cfg)
			if forbidden {
				if rec.Code < 500 {
					t.Fatal("checkpoint acknowledged detached lifecycle work")
				}
				awaitBackground(t, func() bool { return host.count() == 0 })
			} else if rec.Code != 204 || host.count() != 0 {
				t.Fatal("checkpoint acknowledged before group ownership was released")
			}
			data, err := os.ReadFile(witness)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[int]bool{}
			for _, line := range strings.Fields(string(data)) {
				pid, err := strconv.Atoi(line)
				if err != nil || !pids[pid] || seen[pid] {
					t.Fatal("callback crossed worker identity or repeated")
				}
				seen[pid] = true
			}
			if len(seen) != len(pids) {
				t.Fatal("checkpoint callback missed an interpreter")
			}
		})
	}
}
