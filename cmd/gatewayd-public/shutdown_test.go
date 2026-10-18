package main

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gateway/drain"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestShutdownGatewayServersReturnsImmediatelyWhenIdle(t *testing.T) {
	public := httptest.NewServer(http.NotFoundHandler())
	control := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(public.Close)
	t.Cleanup(control.Close)

	started := time.Now()
	err := shutdownGatewayServers(context.Background(), discardLogger(), public.Config, nil, control.Config, drain.NewTracker(), nil, 2*time.Second, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("idle shutdown took %v, want <= 500ms", elapsed)
	}
}

func TestShutdownGatewayServersWaitsForInflightRequest(t *testing.T) {
	tracker := drain.NewTracker()
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	requestDone := make(chan struct{})
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		defer tracker.Begin("http")()
		close(requestStarted)
		<-releaseRequest
		w.WriteHeader(http.StatusNoContent)
	}))
	control := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(public.Close)
	t.Cleanup(control.Close)

	go func() {
		defer close(requestDone)
		resp, err := public.Client().Get(public.URL)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-requestStarted

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- shutdownGatewayServers(context.Background(), discardLogger(), public.Config, nil, control.Config, tracker, nil, 2*time.Second, nil, nil)
	}()
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before the request completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseRequest)
	if err := <-shutdownDone; err != nil {
		t.Fatal(err)
	}
	<-requestDone
}

func TestShutdownGatewayServersDrainsTCPWithSharedBudget(t *testing.T) {
	public := httptest.NewServer(http.NotFoundHandler())
	control := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(public.Close)
	t.Cleanup(control.Close)

	tcpStarted := make(chan struct{})
	releaseTCP := make(chan struct{})
	tcpDrain := func(ctx context.Context) error {
		close(tcpStarted)
		select {
		case <-releaseTCP:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- shutdownGatewayServers(context.Background(), discardLogger(), public.Config, nil, control.Config, drain.NewTracker(), nil, time.Second, tcpDrain, nil)
	}()
	<-tcpStarted
	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before TCP drain completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseTCP)
	if err := <-shutdownDone; err != nil {
		t.Fatal(err)
	}
}

func TestShutdownGatewayServersUsesOneBoundedBudget(t *testing.T) {
	tracker := drain.NewTracker()
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	public := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		defer tracker.Begin("http")()
		close(requestStarted)
		<-releaseRequest
	}))
	control := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(public.Close)
	t.Cleanup(control.Close)

	go func() {
		resp, err := public.Client().Get(public.URL)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-requestStarted

	started := time.Now()
	err := shutdownGatewayServers(context.Background(), discardLogger(), public.Config, nil, control.Config, tracker, nil, 75*time.Millisecond, nil, nil)
	elapsed := time.Since(started)
	close(releaseRequest)
	if err == nil {
		t.Fatal("shutdown error = nil, want bounded deadline failure")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("bounded shutdown took %v, want <= 500ms", elapsed)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestDrainOldGenerationKeepsStreamsUntilStopping: after a hand-off the old
// generation keeps an SSE stream open (it is not cut at the restart grace)
// and ends it cleanly when the unit stops (ADR-20261018074719).
func TestDrainOldGenerationKeepsStreamsUntilStopping(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set(api.StreamingStatusHeader, string(api.StreamingStatusStreaming))
		_, _ = io.WriteString(w, "data: 1\n\n")
		http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(upstream.Close)
	target, _ := url.Parse(upstream.URL)
	tracker := drain.NewTracker()
	proxy := gateway.NewInternalReverseProxy(gateway.NewTCPDialer(target.Host), target, discardLogger(), false).WithInFlightTracker(tracker)
	public := httptest.NewUnstartedServer(proxy)
	publicDrain := wire.NewHTTPDrain(public.Config)
	public.Listener = publicDrain.Listener(public.Listener)
	public.Start()
	t.Cleanup(public.Close)
	control := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(control.Close)

	resp, err := http.Get(public.URL + "/events") //nolint:noctx // test client
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != "data: 1\n" {
		t.Fatalf("first event = %q, %v", line, err)
	}
	ended := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, reader); ended <- err }()

	stopping, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- drainOldGeneration(context.Background(), stopping, discardLogger(), public.Config, publicDrain, control.Config, tracker, nil, nil)
	}()
	select {
	case err := <-ended:
		t.Fatalf("SSE stream ended (%v) as the hand-off began; the old generation keeps it", err)
	case <-time.After(500 * time.Millisecond):
	}
	stop() // SIGTERM to the old generation
	select {
	case err := <-ended:
		if err != nil {
			t.Fatalf("SSE stream aborted (%v); want a clean end the client reconnects from", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SSE stream still open after the old generation was asked to stop")
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("old generation's drain did not finish")
	}
}
