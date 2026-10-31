package main

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/http2"

	"github.com/onebox-faas/faas/pkg/bridgecompletion"
)

// countingConn counts the reads and writes on one side of a hop; each is a
// syscall and usually a goroutine wakeup, the bulk of a small request's cost.
type countingConn struct {
	net.Conn
	reads, writes *atomic.Int64
}

func (c countingConn) Read(p []byte) (int, error) {
	c.reads.Add(1)
	return c.Conn.Read(p)
}

func (c countingConn) Write(p []byte) (int, error) {
	c.writes.Add(1)
	return c.Conn.Write(p)
}

type countingListener struct {
	net.Listener
	reads, writes *atomic.Int64
}

func (l countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return countingConn{conn, l.reads, l.writes}, nil
}

func processCPU() time.Duration {
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

// BenchmarkBridgeExchange drives vmmd's side of a persistent-bridge exchange
// (an H2C request, then the completion wait when one is needed) through the
// real bridge server to an in-process H1 guest. "wait" is every exchange
// before completion retention; "retained" skips the wait once the response
// stream ended. CPU covers the whole process: client, bridge and guest.
func BenchmarkBridgeExchange(b *testing.B) {
	guest := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello from app"))
	}))
	guest.Start()
	defer guest.Close()
	guestHost, guestPort, _ := net.SplitHostPort(guest.Listener.Addr().String())
	port, _ := strconv.Atoi(guestPort)

	dir, err := os.MkdirTemp("", "bx") // unix socket paths are short on macOS
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	sock := filepath.Join(dir, "b.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		b.Fatal(err)
	}
	var vmmdReads, vmmdWrites, bridgeReads, bridgeWrites atomic.Int64
	srv, _ := buildServerWithGuestPool(guestHost, uint16(port), time.Now().Add(time.Hour))
	go func() { _ = srv.Serve(countingListener{ln, &bridgeReads, &bridgeWrites}) }()
	defer func() { _ = srv.Close() }()
	client := &http.Client{Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, _, _ string, _ *tls.Config) (net.Conn, error) {
			var dialer net.Dialer
			conn, err := dialer.DialContext(ctx, "unix", sock)
			if err != nil {
				return nil, err
			}
			return countingConn{conn, &vmmdReads, &vmmdWrites}, nil
		},
		StrictMaxConcurrentStreams: true,
	}}

	exchange := func(b *testing.B, retain bool) {
		req, _ := http.NewRequest(http.MethodGet, "http://unix/", http.NoBody)
		req.Header.Set(bridgeRequestMarkerHeader, "1")
		req.Header.Set(bridgeRequestProtocolHeader, "h1")
		req.Header.Set(bridgeRequestPortHeader, guestPort)
		req.Header.Set(bridgeRequestHostHeader, "app.example")
		id := uuid.NewString()
		req.Header.Set(bridgecompletion.ExchangeHeader, id)
		if retain {
			req.Header.Set(bridgecompletion.RetainRequestHeader, bridgecompletion.RetainRequestValue)
		}
		resp, err := client.Do(req)
		if err != nil {
			b.Fatal(err)
		}
		_, readErr := io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if retain && readErr == nil && slices.Contains(resp.Header.Values(bridgecompletion.RetainedHeader), id) {
			return
		}
		wait, _ := http.NewRequest(http.MethodGet, "http://unix/", nil)
		wait.Header.Set(bridgecompletion.WaitHeader, id)
		waited, err := client.Do(wait)
		if err != nil {
			b.Fatal(err)
		}
		_ = waited.Body.Close()
		if waited.StatusCode != http.StatusNoContent {
			b.Fatalf("completion status %d", waited.StatusCode)
		}
	}
	for _, mode := range []struct {
		name   string
		retain bool
	}{{"wait", false}, {"retained", true}} {
		b.Run(mode.name, func(b *testing.B) {
			for range 100 {
				exchange(b, mode.retain)
			}
			for _, counter := range []*atomic.Int64{&vmmdReads, &vmmdWrites, &bridgeReads, &bridgeWrites} {
				counter.Store(0)
			}
			b.ResetTimer()
			start := processCPU()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					exchange(b, mode.retain)
				}
			})
			n := float64(b.N)
			b.ReportMetric(float64(processCPU()-start)/n/1e3, "cpu-µs/op")
			b.ReportMetric(float64(vmmdWrites.Load())/n, "vmmd-writes/op")
			b.ReportMetric(float64(vmmdReads.Load())/n, "vmmd-reads/op")
			b.ReportMetric(float64(bridgeReads.Load())/n, "bridge-reads/op")
			b.ReportMetric(float64(bridgeWrites.Load())/n, "bridge-writes/op")
		})
	}
}
