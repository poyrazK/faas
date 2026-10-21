//go:build unix

// Pre-traffic checks for the two-generation hand-off
// (docs/adr/20261020214632-automatic-unit-deploy-safety-rails.md, section 4).

package wire_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// checkPair is a connected socket pair: the old generation's end as a file,
// the successor's end as a descriptor.
func checkPair(t *testing.T) (old *os.File, successorFD int) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	old = os.NewFile(uintptr(fds[0]), "old")
	t.Cleanup(func() { _ = old.Close() })
	return old, fds[1]
}

// serveOne answers one request on the successor's end with handler.
func serveOne(t *testing.T, fd int, handler http.HandlerFunc) {
	t.Helper()
	f := os.NewFile(uintptr(fd), "successor")
	conn, err := net.FileConn(f)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		defer func() { _ = conn.Close() }()
		req, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			return
		}
		rec := &response{header: http.Header{}, status: http.StatusOK}
		handler(rec, req)
		resp := &http.Response{StatusCode: rec.status, ProtoMajor: 1, ProtoMinor: 1, Header: rec.header,
			Body: io.NopCloser(strings.NewReader(rec.body.String())), ContentLength: int64(rec.body.Len())}
		_ = resp.Write(conn)
	}()
}

type response struct {
	header http.Header
	status int
	body   strings.Builder
}

func (r *response) Header() http.Header         { return r.header }
func (r *response) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *response) WriteHeader(code int)        { r.status = code }

func TestHandoffCheckPassesOnAWantedStatus(t *testing.T) {
	old, fd := checkPair(t)
	var got *http.Request
	serveOne(t, fd, func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.WriteHeader(http.StatusUnauthorized)
	})
	check := wire.HandoffCheck{Name: "api", Network: "tcp", Address: "127.0.0.1:8080", Path: "/v1/apps",
		Want: []int{http.StatusUnauthorized}}
	if err := wire.RunHandoffCheckForTest(check, old); err != nil {
		t.Fatal(err)
	}
	if got.URL.Path != "/v1/apps" || got.Host != "localhost" || got.Header.Get("X-Gregale-Handoff-Check") != "api" {
		t.Fatalf("check request = %s %s host %q headers %v", got.Method, got.URL.Path, got.Host, got.Header)
	}
}

func TestHandoffCheckFailsOnAnotherStatusOrNoAnswer(t *testing.T) {
	old, fd := checkPair(t)
	serveOne(t, fd, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	check := wire.HandoffCheck{Name: "readyz", Path: "/readyz", Want: []int{http.StatusOK}}
	err := wire.RunHandoffCheckForTest(check, old)
	if err == nil || !strings.Contains(err.Error(), "GET /readyz answered 503, want [200]") {
		t.Fatalf("err = %v, want the status mismatch", err)
	}

	silent, silentFD := checkPair(t)
	t.Cleanup(func() { _ = syscall.Close(silentFD) })
	start := time.Now()
	check.Timeout = 200 * time.Millisecond
	if err := wire.RunHandoffCheckForTest(check, silent); err == nil {
		t.Fatal("a successor that never answered passed")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the check did not respect its timeout")
	}
}

func TestHandoffRejectedStatus(t *testing.T) {
	rejected := wire.HandoffStatusFailed + " 2026-10-20T10:00:00Z: wire: successor 42: " + wire.ErrHandoffRejected.Error() + " api: GET /v1/apps answered 500"
	if !wire.HandoffRejected(rejected) {
		t.Fatalf("%q not seen as a rejection", rejected)
	}
	for _, status := range []string{
		wire.HandoffStatusFailed + " 2026-10-20T10:00:00Z: wire: successor 42: not ready within 2m0s",
		wire.HandoffStatusStarted,
		"rejected by pre-traffic check", // without the failed prefix
	} {
		if wire.HandoffRejected(status) {
			t.Errorf("%q seen as a rejection", status)
		}
	}
}

// A successor's ready gate returns the predecessor's check connections from
// Accept before it is ready, and nothing else until it is.
func TestHandoffGateServesChecksBeforeReady(t *testing.T) {
	src, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	f, err := src.(*net.TCPListener).File()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	old, probeFD := checkPair(t)
	_, otherFD := checkPair(t) // meant for a listener this process does not serve
	ready := make(chan struct{})
	defer wire.UseHandoffReadyForTest(ready)()
	defer wire.UseHandoffProbesForTest([]int{dupFD(t, f)}, map[int][2]string{
		probeFD: {"tcp", src.Addr().String()},
		otherFD: {"tcp", "127.0.0.1:1"},
	})()
	l, err := wire.ListenInherited("tcp", src.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	client, err := net.Dial("tcp", src.Addr().String()) // ordinary traffic, queued
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	first, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if first.RemoteAddr().String() != "127.0.0.1:0" || first.LocalAddr().String() != src.Addr().String() {
		t.Fatalf("check connection addresses = %s -> %s", first.RemoteAddr(), first.LocalAddr())
	}
	if _, err := old.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(first, buf); err != nil || string(buf) != "ping" {
		t.Fatalf("first Accept is not the check connection: %q %v", buf, err)
	}
	_ = first.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		if c, err := l.Accept(); err == nil {
			accepted <- c
		}
	}()
	select {
	case c := <-accepted:
		_ = c.Close()
		t.Fatal("ordinary traffic reached the successor before it was released")
	case <-time.After(200 * time.Millisecond):
	}
	close(ready)
	select {
	case c := <-accepted:
		_ = c.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("the released successor did not accept")
	}
}

func TestPrepareHandoffChecks(t *testing.T) {
	defer wire.UseHandoffChecksSkipFileForTest(filepath.Join(t.TempDir(), "absent"))()
	serves := func(network, address string) bool { return network == "tcp" && address == "127.0.0.1:9101" }
	readyz := wire.HandoffCheck{Name: "readyz", Network: "tcp", Address: "127.0.0.1:9101", Path: "/readyz", Want: []int{200}}

	env, files, err := wire.PrepareHandoffChecksForTest([]wire.HandoffCheck{readyz, readyz}, serves, 7)
	if err != nil {
		t.Fatal(err)
	}
	if files != 3 { // two check connections and the go pipe
		t.Fatalf("files = %d, want 3", files)
	}
	var specs []map[string]any
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "FAAS_HANDOFF_CHECK_FDS="); ok {
			if err := json.Unmarshal([]byte(v), &specs); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(specs) != 2 || specs[0]["fd"] != float64(7) || specs[1]["fd"] != float64(8) || specs[0]["address"] != "127.0.0.1:9101" {
		t.Fatalf("check fds = %v", specs)
	}
	if !strings.Contains(strings.Join(env, " "), "FAAS_HANDOFF_GO_FD=9") {
		t.Fatalf("go pipe not after the checks: %v", env)
	}

	for name, check := range map[string]wire.HandoffCheck{
		"unserved listener": {Name: "x", Network: "tcp", Address: "127.0.0.1:1", Path: "/readyz", Want: []int{200}},
		"no path":           {Name: "x", Network: "tcp", Address: "127.0.0.1:9101", Want: []int{200}},
		"no wanted status":  {Name: "x", Network: "tcp", Address: "127.0.0.1:9101", Path: "/readyz"},
	} {
		if _, _, err := wire.PrepareHandoffChecksForTest([]wire.HandoffCheck{check}, serves, 7); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	skip := filepath.Join(t.TempDir(), "skip")
	if err := os.WriteFile(skip, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer wire.UseHandoffChecksSkipFileForTest(skip)()
	if env, _, err := wire.PrepareHandoffChecksForTest([]wire.HandoffCheck{readyz}, serves, 7); err != nil || env != nil {
		t.Fatalf("the skip file did not skip the checks: %v %v", env, err)
	}
}

func TestServesMatchesSharedListenersLikeInheritedSockets(t *testing.T) {
	defer wire.UseInheritedListenersForTest(nil, nil)()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	wire.ShareForTest(l)
	_, port, _ := net.SplitHostPort(l.Addr().String())
	for address, want := range map[string]bool{
		l.Addr().String():   true,
		":" + port:          true, // a wildcard configuration matches
		"127.0.0.2:" + port: false,
		"127.0.0.1:1":       false,
	} {
		if got := wire.ServesForTest("tcp", address); got != want {
			t.Errorf("serves(%s) = %v, want %v", address, got, want)
		}
	}
	if wire.ServesForTest("unix", "/run/faas/none.sock") {
		t.Error("a tcp listener served a unix address")
	}
}
