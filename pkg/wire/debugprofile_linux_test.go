//go:build linux

package wire_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

func debugProfileClient(addr string) *http.Client {
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", "@"+addr)
		},
	}}
}

// ADR-20261023200002: the endpoint listens on an abstract socket, accepts
// this process's own uid, and falls back to a per-pid name when another
// process of the same daemon holds the plain one.
func TestDebugProfileAbstractSocket(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	name := fmt.Sprintf("wiretest-%d-%d", os.Getpid(), time.Now().UnixNano()%100000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wire.StartDebugProfiling(ctx, name, log)

	addr := wire.DebugProfileSocketPrefix + name
	var resp *http.Response
	var err error
	for i := 0; i < 50; i++ {
		resp, err = debugProfileClient(addr).Get("http://faas-debug/debug/pprof/goroutine?debug=1")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("GET over @%s: %v", addr, err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "goroutine profile") {
		t.Fatalf("goroutine profile = %d %.120q", resp.StatusCode, body)
	}

	// The plain name is taken: a second listener uses <name>.<pid>.
	ln, second, err := wire.ListenDebugProfile(name, log)
	if err != nil {
		t.Fatalf("second listener: %v", err)
	}
	defer func() { _ = ln.Close() }()
	if want := fmt.Sprintf("%s.%d", addr, os.Getpid()); second != want {
		t.Fatalf("second listener at %s, want %s", second, want)
	}
}

// A peer whose uid is not allowed is disconnected before any request is read.
func TestDebugProfileRefusesOtherUIDs(t *testing.T) {
	addr := fmt.Sprintf("%swiretest-refuse-%d", wire.DebugProfileSocketPrefix, os.Getpid())
	ln, err := net.Listen("unix", "@"+addr)
	if err != nil {
		t.Fatal(err)
	}
	other := uint32(os.Getuid()) + 1
	srv := &http.Server{Handler: wire.DebugProfileHandler(), ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(wire.PeerCredListener(ln, map[uint32]bool{other: true})) }()
	defer func() { _ = srv.Close() }()

	resp, err := debugProfileClient(addr).Get("http://faas-debug/debug/pprof/heap")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("a refused peer got HTTP %d", resp.StatusCode)
	}
}
