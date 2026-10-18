//go:build unix

package wire_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// TestServeUntilShutdownFinishesInFlightCalls: a stopping daemon (outboundd,
// bridged) returns only after an in-flight call completed. Both used to
// return as soon as Shutdown began, and the process exited under the call.
func TestServeUntilShutdownFinishesInFlightCalls(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "provider answered")
	})}
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() {
		returned <- wire.ServeUntilShutdown(ctx, server, listener, 5*time.Second)
	}()

	type result struct {
		body string
		err  error
	}
	call := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String() + "/i/llm/") //nolint:noctx // test client
		if err != nil {
			call <- result{err: err}
			return
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		call <- result{body: string(body), err: err}
	}()
	<-started
	cancel() // SIGTERM
	select {
	case err := <-returned:
		t.Fatalf("serveUntilShutdown returned (%v) with a call in flight", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if got := <-call; got.err != nil || got.body != "provider answered" {
		t.Fatalf("in-flight call = %q, %v", got.body, got.err)
	}
	if err := <-returned; err != nil {
		t.Fatal(err)
	}
}

// TestServeUntilShutdownRestartUnderLoadDropsNothing restarts a daemon such
// as outboundd on a TCP socket a systemd unit holds open: calls in flight
// finish on the old generation and calls made while neither runs wait for
// the new one.
func TestServeUntilShutdownRestartUnderLoadDropsNothing(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	master, err := base.(*net.TCPListener).File()
	addr := base.Addr().String()
	_ = base.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close() })

	var served [2]atomic.Int64
	start := func(generation int) (stop func()) {
		fd, err := syscall.Dup(int(master.Fd()))
		if err != nil {
			t.Fatal(err)
		}
		file := os.NewFile(uintptr(fd), "inherited")
		listener, err := net.FileListener(file)
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
		server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(5 * time.Millisecond) // an outbound call in flight
			served[generation].Add(1)
			_, _ = io.WriteString(w, "ok")
		})}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- wire.ServeUntilShutdown(ctx, server, listener, 5*time.Second)
		}()
		return func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("generation %d shutdown: %v", generation, err)
			}
		}
	}

	stopOld := start(0)
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	var (
		failures atomic.Int64
		firstErr atomic.Value
		halt     = make(chan struct{})
		wg       sync.WaitGroup
	)
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-halt:
					return
				default:
				}
				resp, err := client.Post("http://"+addr+"/i/integration/", "application/json", strings.NewReader(`{}`))
				if err != nil {
					failures.Add(1)
					firstErr.CompareAndSwap(nil, err.Error())
					continue
				}
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusOK || string(body) != "ok" {
					failures.Add(1)
					firstErr.CompareAndSwap(nil, fmt.Sprintf("status %d body %q", resp.StatusCode, body))
				}
			}
		})
	}
	waitFor(t, func() bool { return served[0].Load() > 50 })
	stopOld()
	time.Sleep(300 * time.Millisecond) // replacement start-up
	stopNew := start(1)
	waitFor(t, func() bool { return served[1].Load() > 50 })
	close(halt)
	wg.Wait()
	stopNew()

	if n := failures.Load(); n != 0 {
		t.Fatalf("%d calls failed across the restart; first: %v", n, firstErr.Load())
	}
}
