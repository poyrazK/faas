package functionsql

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// Control holds the native/portable checkpoint after local pools drained but
// before host capture. Its authenticated test peer never receives SQL or DSNs.
type Control struct {
	URL    string
	Token  string
	Port   uint16
	mu     sync.Mutex
	phases map[string]*controlPhase
}

type controlPhase struct {
	gate     chan struct{}
	arrivals map[int]bool
}

func NewControl(t *testing.T, host string) *Control {
	t.Helper()
	ln, err := net.Listen("tcp4", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal("listen for isolated lifecycle control")
	}
	c := &Control{Token: uuid.NewString(), Port: uint16(ln.Addr().(*net.TCPAddr).Port), phases: make(map[string]*controlPhase)}
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
	c.mu.Lock()
	phase := c.phases[r.URL.Path]
	var gate chan struct{}
	if phase != nil {
		gate = phase.gate
		phase.arrivals[pid] = true
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
	c.phases[path] = &controlPhase{gate: make(chan struct{}), arrivals: map[int]bool{}}
}

func (c *Control) Arrivals() int         { return c.arrivals("/checkpoint") }
func (c *Control) RecoveryArrivals() int { return c.arrivals("/recovery") }

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
