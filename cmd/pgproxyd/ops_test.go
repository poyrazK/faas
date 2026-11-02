// adr: 1027
package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/onebox-faas/faas/pkg/pgproxy"
	"github.com/onebox-faas/faas/pkg/wire"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec,noctx // test-only loopback URL
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestOpsServerReportsReadinessAndMetrics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := prometheus.NewRegistry()
	pgproxy.NewMetrics(reg)
	var probe wire.ReadyzProbe
	signal := probe.Register()
	addr := freeAddr(t)
	if err := serveOps(ctx, addr, reg, &probe, slog.Default()); err != nil {
		t.Fatal(err)
	}
	base := "http://" + addr
	if code, _ := get(t, base+"/healthz"); code != http.StatusOK {
		t.Fatalf("/healthz = %d", code)
	}
	if code, body := get(t, base+"/readyz"); code == http.StatusOK {
		t.Fatalf("/readyz = 200 before every signal is ready: %s", body)
	}
	signal.Set(true, "")
	if code, body := get(t, base+"/readyz"); code != http.StatusOK {
		t.Fatalf("/readyz = %d once ready: %s", code, body)
	}
	if _, body := get(t, base+"/metrics"); !strings.Contains(body, "pgproxyd_active_sessions") || !strings.Contains(body, "pgproxyd_wake_seconds") {
		t.Fatalf("/metrics lacks the proxy metrics:\n%s", body)
	}
}

// A schedd that cannot be reached keeps pgproxyd unready: it could
// authenticate clients but never wake their databases.
func TestWatchScheddReportsAnUnreachableSchedd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := grpc.NewClient("passthrough:///"+freeAddr(t), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	var probe wire.ReadyzProbe
	signal := probe.Register()
	go watchSchedd(ctx, conn, signal)
	deadline := time.Now().Add(5 * time.Second)
	for {
		ready, reason := signal.Report()
		if !ready && strings.Contains(reason, "TRANSIENT_FAILURE") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("schedd signal = %v %q, want not ready in TRANSIENT_FAILURE", ready, reason)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
