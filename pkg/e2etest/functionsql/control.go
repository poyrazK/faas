package functionsql

import (
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/e2etest/functionmigration"
)

// Control holds the native/portable checkpoint after local pools drained but
// before host capture. Its authenticated test peer never receives SQL or DSNs.
type Control struct {
	URL    string
	Token  string
	Port   uint16
	mu     sync.Mutex
	phases map[string]*controlPhase
	calls  map[int]functionmigration.Calls
}

type controlPhase struct {
	gate     chan struct{}
	arrivals map[int]WorkerWitness
}

type WorkerWitness struct {
	PID         int   `json:"pid"`
	Generations []int `json:"generations"`
}

func NewControl(t *testing.T, host string) *Control {
	t.Helper()
	ln, err := net.Listen("tcp4", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal("listen for isolated lifecycle control")
	}
	c := &Control{Token: uuid.NewString(), Port: uint16(ln.Addr().(*net.TCPAddr).Port), phases: make(map[string]*controlPhase), calls: make(map[int]functionmigration.Calls)}
	server := httptest.NewUnstartedServer(http.HandlerFunc(c.serve))
	server.Listener = ln
	server.Start()
	c.URL = server.URL
	t.Cleanup(func() { c.Release(); server.Close() })
	return c
}

func (c *Control) serve(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.Atoi(r.URL.Query().Get("pid"))
	if err != nil || pid < 1 || r.Header.Get("Authorization") != "Bearer "+c.Token || r.Method != http.MethodPost || (r.URL.Path != "/checkpoint" && r.URL.Path != "/recovery") {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	var generations []int
	if raw := r.URL.Query().Get("generations"); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			generation, err := strconv.Atoi(part)
			if err != nil || generation < 0 || len(generations) == 2 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			generations = append(generations, generation)
		}
		if len(generations) != 2 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}
	c.mu.Lock()
	c.recordCall(r.URL.Path, pid)
	phase := c.phases[r.URL.Path]
	var gate chan struct{}
	if phase != nil {
		gate = phase.gate
		phase.arrivals[pid] = WorkerWitness{PID: pid, Generations: generations}
	}
	c.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-r.Context().Done():
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// Calls survives Release so a duplicate callback after readiness is visible.
// Arm/Release controls waiting only; it never resets observation history.
func (c *Control) Calls() []functionmigration.Calls {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []functionmigration.Calls
	for _, call := range c.calls {
		result = append(result, call)
	}
	slices.SortFunc(result, func(a, b functionmigration.Calls) int { return a.PID - b.PID })
	return result
}

func (c *Control) recordCall(path string, pid int) {
	call := c.calls[pid]
	call.PID = pid
	if path == "/checkpoint" {
		call.Checkpoints++
	} else {
		call.Recoveries++
	}
	c.calls[pid] = call
}

func (c *Control) Arm() {
	c.arm("/checkpoint")
}

func (c *Control) ArmRecovery() {
	c.arm("/recovery")
}

func (c *Control) arm(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.phases[path] != nil {
		panic("lifecycle control already armed")
	}
	c.phases[path] = &controlPhase{gate: make(chan struct{}), arrivals: map[int]WorkerWitness{}}
}

func (c *Control) Arrivals() int         { return c.arrivals("/checkpoint") }
func (c *Control) RecoveryArrivals() int { return c.arrivals("/recovery") }

func (c *Control) CheckpointWorkers() []WorkerWitness { return c.workers("/checkpoint") }
func (c *Control) RecoveryWorkers() []WorkerWitness   { return c.workers("/recovery") }

func (c *Control) workers(path string) []WorkerWitness {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []WorkerWitness
	if phase := c.phases[path]; phase != nil {
		for _, worker := range phase.arrivals {
			worker.Generations = slices.Clone(worker.Generations)
			result = append(result, worker)
		}
	}
	slices.SortFunc(result, func(a, b WorkerWitness) int { return a.PID - b.PID })
	return result
}

func (c *Control) arrivals(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if phase := c.phases[path]; phase != nil {
		return len(phase.arrivals)
	}
	return 0
}

func (c *Control) ReleaseCheckpoint() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.release("/checkpoint")
}

func (c *Control) release(path string) {
	if phase := c.phases[path]; phase != nil {
		close(phase.gate)
		delete(c.phases, path)
	}
}

func (c *Control) Release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.release("/checkpoint")
	c.release("/recovery")
}
