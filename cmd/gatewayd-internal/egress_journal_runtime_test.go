package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunWithDeps_EnvironmentEgressJournalServesAndShutsDown(t *testing.T) {
	journal := filepath.Join(t.TempDir(), "private-egress", "pending.json")
	t.Setenv(gatewayEgressPendingPathEnv, journal)
	t.Setenv("FAAS_CONSUMER_USAGE_OUTBOX_ROOT", t.TempDir())
	t.Setenv("FAAS_EGRESS_SOCKET", filepath.Join(t.TempDir(), "egress.sock"))
	deps := defaultDeps()
	deps.capCheck = func() error { return nil }
	deps.backend = &fixedBackend{}
	deps.newSrv = func(addr string, handler http.Handler) *http.Server {
		return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	}
	public, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = public.Close() })
	deps.listen = func(_, _ string) (net.Listener, error) { return public, nil }
	control, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deps.controlAddr = control.Addr().String()
	_ = control.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	go func() {
		runErr = runWithDeps(ctx, discardLogger(), deps)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if runErr != nil && !errors.Is(runErr, context.Canceled) && !strings.Contains(runErr.Error(), "Server closed") && !strings.Contains(runErr.Error(), "use of closed network connection") {
				t.Errorf("gateway returned %v", runErr)
			}
		case <-time.After(3 * time.Second):
			t.Error("gateway did not stop after context cancellation")
		}
	})
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for url, want := range map[string]int{
		"http://" + public.Addr().String() + "/anything": http.StatusNotFound,
		"http://" + deps.controlAddr + "/healthz":        http.StatusOK,
	} {
		deadline := time.Now().Add(2 * time.Second)
		served := false
		for time.Now().Before(deadline) {
			select {
			case <-done:
				t.Fatalf("gateway exited before serving %s: %v", url, runErr)
			default:
			}
			response, err := client.Get(url)
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode != want {
					t.Fatalf("%s status=%d, want %d", url, response.StatusCode, want)
				}
				served = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !served {
			t.Fatalf("gateway did not serve %s with its configured private journal", url)
		}
	}
	if info, err := os.Stat(filepath.Dir(journal)); err != nil || !info.IsDir() {
		t.Fatal("gateway did not claim its configured journal directory", err)
	}
}
