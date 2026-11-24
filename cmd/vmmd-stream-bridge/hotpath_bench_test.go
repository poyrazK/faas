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

// prodGuestHeaders is the header set a production gateway forward carried to
// the guest (captured on a compute node, 2026-11-24; values are stand-ins of
// the same shape and length).
var prodGuestHeaders = [][2]string{
	{"User-Agent", "Mozilla/5.0 (X11; Linux x86_64) k6/0.54.0 (https://k6.io/)"},
	{"Accept", "*/*"},
	{"Accept-Encoding", "gzip, deflate, br"},
	{"Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"},
	{"True-Client-Ip", "203.0.113.47"},
	{"X-Faas-App-Id", "dc001401-baa6-4ae8-8817-8c45906a47a3"},
	{"X-Faas-Client-Country", "US"},
	{"X-Faas-Client-Ip", "203.0.113.47"},
	{"X-Faas-Client-Ua-Family", "other"},
	{"X-Faas-Deadline-Ms", "1764000000000"},
	{"X-Faas-Deployment-Created-At", "2026-11-21T12:00:00Z"},
	{"X-Faas-Deployment-Id", "5b8e2a0c-7d1f-4c3e-9a6b-2f4d8e1c0a97"},
	{"X-Faas-Image-Digest", "sha256:9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"},
	{"X-Faas-Instance-Id", "0c9a7e5d-3b2f-4a1e-8d6c-5f4e3d2c1b0a"},
	{"X-Faas-Invocation-Id", "7e6d5c4b-3a29-4180-9f7e-6d5c4b3a2918"},
	{"X-Faas-Node-Id", "62946968-dbff-44fe-a7fd-6e02489826ad"},
	{"X-Faas-Request-Id", "req_01JDXK8Q2M3N4P5R6S7T8V9W0X"},
	{"X-Faas-Tenant-Id", "a1b2c3d4-e5f6-4789-8abc-def012345678"},
	{"X-Forwarded-For", "203.0.113.47"},
	{"X-Forwarded-Host", "loadtest-hello-20261121.gregale.dev"},
	{"X-Forwarded-Proto", "https"},
	{"X-Gregale-Trace-Id", "4bf92f3577b34da6a3ce929d0e0e4736"},
	{"X-Real-Ip", "203.0.113.47"},
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

	exchange := func(b *testing.B, retain, prodHeaders bool) {
		req, _ := http.NewRequest(http.MethodGet, "http://unix/", http.NoBody)
		if prodHeaders {
			for _, h := range prodGuestHeaders {
				req.Header.Set(h[0], h[1])
			}
		}
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
		name        string
		retain      bool
		prodHeaders bool
	}{{"wait", false, false}, {"retained", true, false}, {"retained-prod-headers", true, true}} {
		b.Run(mode.name, func(b *testing.B) {
			for range 100 {
				exchange(b, mode.retain, mode.prodHeaders)
			}
			for _, counter := range []*atomic.Int64{&vmmdReads, &vmmdWrites, &bridgeReads, &bridgeWrites} {
				counter.Store(0)
			}
			b.ResetTimer()
			start := processCPU()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					exchange(b, mode.retain, mode.prodHeaders)
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
