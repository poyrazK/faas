//go:build metal

package runlifecycle_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A loopback-only relay crosses the fixture's guest boundary. It only exposes
// the workload-authenticated Agent operations and the pilot route. The
// scheduler and gateway deliver inbox events independently of this relay.
type hostedReportRelay struct {
	base     string
	mu       sync.Mutex
	faults   map[string]*hostedReportFault
	failures map[hostedReportFailure]int
}

type hostedReportFailure struct {
	route  string
	status int
}

type hostedReportFault struct {
	committed chan hostedReportAck
	release   chan struct{}
	replayed  chan hostedReportAck
	count     int // guarded by relay.mu
}

type hostedReportAck struct {
	intent [32]byte
	id     string
}

func startHostedReportRelay(t *testing.T, address, base string) *hostedReportRelay {
	t.Helper()
	origin, err := url.Parse(base)
	requireReport(t, err)
	if origin.Hostname() != "127.0.0.1" {
		t.Fatal("fixture APID must be bound to loopback")
	}
	listener, err := net.Listen("tcp4", address)
	requireReport(t, err)
	relay := &hostedReportRelay{base: base, faults: make(map[string]*hostedReportFault), failures: make(map[hostedReportFailure]int)}
	server := &http.Server{Handler: relay, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return relay
}

func (r *hostedReportRelay) arm(path string) *hostedReportFault {
	fault := &hostedReportFault{committed: make(chan hostedReportAck, 1), release: make(chan struct{}), replayed: make(chan hostedReportAck, 1)}
	r.mu.Lock()
	r.faults[path] = fault
	r.mu.Unlock()
	return fault
}

func (r *hostedReportRelay) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if !allowedHostedReportRoute(request.Method, request.URL.Path) || !strings.HasPrefix(request.Header.Get("Authorization"), "Bearer ") {
		http.NotFound(w, request)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, request.Body, 32<<10))
	if err != nil {
		http.Error(w, "invalid fixture request", http.StatusBadRequest)
		return
	}
	upstream, err := http.NewRequestWithContext(request.Context(), request.Method, r.base+request.URL.EscapedPath(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, "invalid fixture path", http.StatusBadRequest)
		return
	}
	upstream.Header.Set("Authorization", request.Header.Get("Authorization"))
	upstream.Header.Set("Content-Type", request.Header.Get("Content-Type"))
	response, err := http.DefaultClient.Do(upstream)
	if err != nil {
		http.Error(w, "fixture API unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		r.mu.Lock()
		r.failures[hostedReportFailure{hostedReportRouteFamily(request.URL.Path), response.StatusCode}]++
		r.mu.Unlock()
	}
	reply, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		http.Error(w, "fixture API read failed", http.StatusBadGateway)
		return
	}
	if response.StatusCode/100 == 2 && r.checkpoint(request, body, reply) {
		http.Error(w, "fixture dropped a committed acknowledgement", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(reply)
}

func hostedReportRouteFamily(path string) string {
	switch path {
	case "/v1/agent-callbacks", "/v1/agent-callbacks/revocations", hostedReportDispatch, hostedReportDelivery:
		return path
	default:
		return "Run or pipeline read"
	}
}

func allowedHostedReportRoute(method, path string) bool {
	switch method {
	case http.MethodPost:
		return path == "/v1/agent-callbacks" || path == "/v1/agent-callbacks/revocations" ||
			path == hostedReportDispatch || path == hostedReportDelivery
	case http.MethodGet:
		return strings.HasPrefix(path, "/v1/agent-tools/runs/") || strings.HasPrefix(path, "/v1/agent-tools/pipelines/")
	default:
		return false
	}
}

func (r *hostedReportRelay) checkpoint(request *http.Request, body, reply []byte) bool {
	// The Agent stores pending calls as JSONB, which may reorder keys.
	// Compare the full JSON intent and preserve the exact numeric values.
	var intent any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&intent); err != nil {
		return false
	}
	canonical, err := json.Marshal(intent)
	if err != nil {
		return false
	}
	r.mu.Lock()
	fault := r.faults[request.URL.Path]
	if fault == nil {
		r.mu.Unlock()
		return false
	}
	var receipt api.RunArtifactDelivery
	if request.URL.Path == hostedReportDelivery {
		if json.Unmarshal(reply, &receipt) != nil || receipt.Status != "completed" || receipt.ID == "" {
			r.mu.Unlock()
			return false
		}
	}
	fault.count++
	attempt := fault.count
	r.mu.Unlock()
	ack := hostedReportAck{intent: sha256.Sum256(canonical), id: receipt.ID}
	switch attempt {
	case 1:
		fault.committed <- ack
		select {
		case <-fault.release:
		case <-request.Context().Done():
		}
		return true
	case 2:
		fault.replayed <- ack
	}
	return false
}

func confirmHostedReplay(t *testing.T, f *runFixture, fault *hostedReportFault, committedJob func() string) string {
	t.Helper()
	const checkpointTimeout = hostedReportTimeoutS*time.Second + api.AsyncInvocationWakeAllowance + 30*time.Second
	var initial hostedReportAck
	select {
	case initial = <-fault.committed:
	case <-time.After(checkpointTimeout):
		t.Fatal("deployed trigger did not reach the first durable API write")
	}
	checkpoint := "dispatch"
	pipelineID := ""
	if committedJob != nil {
		pipelineID = committedJob()
	} else {
		checkpoint = "delivery"
	}
	t.Logf("committed %s acknowledged by APID; restarting before replying to the Agent", checkpoint)
	err := f.h.RestartAPID()
	close(fault.release)
	requireReport(t, err)
	select {
	case replayed := <-fault.replayed:
		if replayed != initial {
			t.Fatal("broker redelivery changed the committed request or its receipt")
		}
	case <-time.After(checkpointTimeout):
		t.Fatal("the real trigger did not retry the interrupted acknowledgement")
	}
	if committedJob != nil && committedJob() != pipelineID {
		t.Fatal("broker replay changed the durable pipeline identity")
	}
	t.Logf("broker replay preserved the committed %s", checkpoint)
	return initial.id
}

func setupHostedPostgresProxy(t *testing.T, address, socket string) *atomic.Int64 {
	t.Helper()
	listener, err := net.Listen("tcp4", address)
	requireReport(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	var connections atomic.Int64
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			go forwardHostedPostgres(client, socket)
		}
	}()
	return &connections
}

func forwardHostedPostgres(client net.Conn, socket string) {
	defer client.Close()
	upstream, err := net.DialTimeout("unix", socket, 5*time.Second)
	if err != nil {
		return
	}
	defer upstream.Close()
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(upstream, client)
		_ = upstream.Close()
		close(done)
	}()
	_, _ = io.Copy(client, upstream)
	_ = client.Close()
	<-done
}
