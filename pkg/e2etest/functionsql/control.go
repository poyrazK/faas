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
	URL      string
	Token    string
	Port     uint16
	mu       sync.Mutex
	gate     chan struct{}
	arrivals map[int]bool
}

func NewControl(t *testing.T, host string) *Control {
	t.Helper()
	ln, err := net.Listen("tcp4", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal("listen for isolated lifecycle control")
	}
	c := &Control{Token: uuid.NewString(), Port: uint16(ln.Addr().(*net.TCPAddr).Port)}
	server := httptest.NewUnstartedServer(http.HandlerFunc(c.serve))
	server.Listener = ln
	server.Start()
	c.URL = server.URL
	t.Cleanup(func() { c.Release(); server.Close() })
	return c
}

func (c *Control) serve(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.Atoi(r.URL.Query().Get("pid"))
	if err != nil || pid < 1 || r.Header.Get("Authorization") != "Bearer "+c.Token || r.Method != http.MethodPost || r.URL.Path != "/checkpoint" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	c.mu.Lock()
	gate := c.gate
	if gate != nil {
		c.arrivals[pid] = true
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
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gate != nil {
		panic("checkpoint control already armed")
	}
	c.gate = make(chan struct{})
	c.arrivals = map[int]bool{}
}
func (c *Control) Arrivals() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.arrivals) }
func (c *Control) Release() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.gate != nil {
		close(c.gate)
		c.gate = nil
	}
}
