package gateway

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestControlMuxHealthz(t *testing.T) {
	mux := ControlMux(NewMetrics(), nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("healthz body = %q, want \"ok\"", string(body))
	}
}

func TestControlMuxReadyz(t *testing.T) {
	t.Run("not-ready when callback false", func(t *testing.T) {
		mux := ControlMux(NewMetrics(), func() bool { return false })
		srv := httptest.NewServer(mux)
		defer srv.Close()
		resp, err := http.Get(srv.URL + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("readyz status = %d, want 503", resp.StatusCode)
		}
	})
	t.Run("ready when callback true", func(t *testing.T) {
		mux := ControlMux(NewMetrics(), func() bool { return true })
		srv := httptest.NewServer(mux)
		defer srv.Close()
		resp, err := http.Get(srv.URL + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("readyz status = %d, want 200", resp.StatusCode)
		}
	})
	t.Run("ready by default", func(t *testing.T) {
		mux := ControlMux(NewMetrics(), nil)
		srv := httptest.NewServer(mux)
		defer srv.Close()
		resp, err := http.Get(srv.URL + "/readyz")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("readyz status = %d, want 200", resp.StatusCode)
		}
	})
}

func TestControlMuxMetrics(t *testing.T) {
	m := NewMetrics()
	m.ObserveRequest("app-1", "pro", "200")
	mux := ControlMux(m, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("metrics status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "gateway_requests_total") {
		t.Errorf("metrics body missing gateway_requests_total:\n%s", string(body))
	}
}

func TestRunControlServerShutsDownOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	mux := ControlMux(NewMetrics(), nil)
	errc := make(chan error, 1)
	go func() {
		// Bind to a loopback ephemeral port to avoid ":9090 in use" in CI.
		errc <- RunControlServer(ctx, "127.0.0.1:0", mux)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-errc:
		if err != nil && err != http.ErrServerClosed {
			t.Errorf("RunControlServer returned %v, want nil or ErrServerClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("RunControlServer did not return after ctx cancel")
	}
}
