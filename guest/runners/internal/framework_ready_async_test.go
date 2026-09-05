package internal

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFrameworkReadyDoesNotHoldHTTPResponses(t *testing.T) {
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	previous := SetProxyDialHook(func(_, _ string) (net.Conn, error) {
		if calls.Add(1) == 1 {
			close(entered)
			defer close(finished)
		}
		<-release
		return nil, errors.New("controlled unavailable proxy")
	})
	signal := NewRunnerSignal("node22", time.Now())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "complete response")
		signal.SignalReady(42)
	}))
	var clients sync.WaitGroup
	defer func() {
		close(release)
		clients.Wait()
		server.Close()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("signal worker did not finish")
		}
		SetProxyDialHook(previous)
	}()

	const concurrent = 8
	results := make(chan error, concurrent)
	for range concurrent {
		clients.Add(1)
		go func() {
			defer clients.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			if err != nil {
				results <- err
				return
			}
			resp, err := server.Client().Do(req)
			if err != nil {
				results <- err
				return
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err == nil && (resp.StatusCode != http.StatusOK || string(body) != "complete response") {
				err = errors.New("incomplete HTTP response")
			}
			results <- err
		}()
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("signal never reached proxy dial")
	}
	deadline := time.NewTimer(500 * time.Millisecond)
	defer deadline.Stop()
	for range concurrent {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-deadline.C:
			t.Fatal("HTTP response waited for the optional framework-ready proxy")
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("proxy dials = %d, want one per runner", got)
	}
}

func TestFrameworkReadyBackgroundSignalKeepsReadDeadline(t *testing.T) {
	client, proxy := net.Pipe()
	previous := SetProxyDialHook(func(_, _ string) (net.Conn, error) { return client, nil })
	defer SetProxyDialHook(previous)
	defer client.Close()
	defer proxy.Close()
	signal := NewRunnerSignal("node22", time.Now())
	signal.SignalReady(42)
	if err := proxy.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(proxy)
	line, err := reader.ReadString('\n')
	if err != nil || line != "node22 42\n" {
		t.Fatalf("signal = %q, %v", line, err)
	}
	// Never acknowledge the signal. The worker must time out and close its
	// connection without keeping an unbounded goroutine alive in the runner.
	if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
		t.Fatalf("unacknowledged signal did not close before test deadline: %v", err)
	}
}

func TestFrameworkReadyBackgroundSignalCapturesStderr(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = writer

	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWorker := func() { releaseOnce.Do(func() { close(release) }) }
	previous := SetProxyDialHook(func(_, _ string) (net.Conn, error) {
		close(entered)
		<-release
		return nil, errors.New("controlled unavailable proxy")
	})
	defer func() {
		releaseWorker()
		os.Stderr = original
		SetProxyDialHook(previous)
		_ = writer.Close()
		_ = reader.Close()
	}()

	signal := NewRunnerSignal("node24", time.Now())
	signal.SignalReady(42)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("signal never reached proxy dial")
	}

	// Restore the process-global before the background worker logs. The
	// worker must retain the destination captured with the signal instead
	// of racing with this assignment or redirecting the message.
	os.Stderr = original
	releaseWorker()

	if err := reader.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	message, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil {
		t.Fatalf("read captured stderr: %v", err)
	}
	if !strings.Contains(message, "framework_ready signal failed: dial proxy: controlled unavailable proxy") {
		t.Fatalf("captured stderr = %q", message)
	}
}
