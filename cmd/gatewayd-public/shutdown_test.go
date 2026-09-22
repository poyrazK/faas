package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway/drain"
)

func TestShutdownGatewayServersReturnsImmediatelyWhenIdle(t *testing.T) {
	public := httptest.NewServer(http.NotFoundHandler())
	control := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(public.Close)
	t.Cleanup(control.Close)

	started := time.Now()
	err := shutdownGatewayServers(context.Background(), discardLogger(), public.Config, control.Config, drain.NewTracker(), nil, 2*time.Second, nil)
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
		shutdownDone <- shutdownGatewayServers(context.Background(), discardLogger(), public.Config, control.Config, tracker, nil, 2*time.Second, nil)
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
		shutdownDone <- shutdownGatewayServers(context.Background(), discardLogger(), public.Config, control.Config, drain.NewTracker(), nil, time.Second, tcpDrain)
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
	err := shutdownGatewayServers(context.Background(), discardLogger(), public.Config, control.Config, tracker, nil, 75*time.Millisecond, nil)
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
