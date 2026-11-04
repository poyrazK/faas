package gateway

// adr: 1736 — the control listener is opened (claimed) before a daemon reports
// ready, then served; ServeControl serves on the listener it is given.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeControlServesTheGivenListener(t *testing.T) {
	l, err := ListenControl("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	errc := make(chan error, 1)
	go func() { errc <- ServeControl(ctx, l, mux) }()

	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + l.Addr().String() + "/healthz") //nolint:noctx // test client
	if err != nil {
		t.Fatalf("control listener did not serve: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	cancel()
	select {
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("ServeControl returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ServeControl did not stop after cancel")
	}
}

func TestListenControlDefaultsToControlAddr(t *testing.T) {
	// The default address may be taken on a developer machine; only the
	// substitution matters, so accept either outcome but check the address
	// it tried.
	l, err := ListenControl("")
	if err == nil {
		defer func() { _ = l.Close() }()
		_, gotPort, _ := net.SplitHostPort(l.Addr().String())
		_, wantPort, _ := net.SplitHostPort(ControlAddr)
		if gotPort != wantPort {
			t.Fatalf("default listener on %s, want port %s", l.Addr(), wantPort)
		}
	}
}
