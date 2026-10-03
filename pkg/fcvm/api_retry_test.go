package fcvm

// spec: §6.2 — failed lifecycle operations preserve snapshot/cold-boot fallback;
// an ambiguous Firecracker response must not repeat an accepted mutation.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func firecrackerRetryTestClient(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	address := strings.TrimPrefix(server.URL, "http://")
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}
	client.Transport = transport
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func TestFirecrackerAPIAcceptedTimeoutIsNotRetried(t *testing.T) {
	t.Parallel()
	var accepted atomic.Int32
	release := make(chan struct{})
	client := firecrackerRetryTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		accepted.Add(1)
		// The mutation has reached Firecracker; its response is delayed until
		// the client abandons the request. Retrying could repeat the mutation.
		<-release
	})
	t.Cleanup(func() { close(release) })
	client.Timeout = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(t.Context(), 400*time.Millisecond)
	defer cancel()
	err := (&JailerVMM{}).apiCallWithClient(ctx, client, http.MethodPut, "/snapshot/create", map[string]string{"snapshot_type": "Full"})
	if err == nil {
		t.Fatal("snapshot timeout unexpectedly succeeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout cause was not preserved: %v", err)
	}
	if got := accepted.Load(); got != 1 {
		t.Fatalf("accepted snapshot requests = %d, want 1", got)
	}
}

func TestFirecrackerAPIAcceptedEOFIsNotRetried(t *testing.T) {
	t.Parallel()
	var accepted atomic.Int32
	client := firecrackerRetryTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		accepted.Add(1)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		// A closed response connection cannot establish whether the action
		// completed. Never send it again solely because its reply was lost.
		_ = conn.Close()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := (&JailerVMM{}).apiCallWithClient(ctx, client, http.MethodPut, "/snapshot/create", nil); err == nil {
		t.Fatal("lost snapshot response unexpectedly succeeded")
	}
	if got := accepted.Load(); got != 1 {
		t.Fatalf("accepted snapshot requests = %d, want 1", got)
	}
}

func TestFirecrackerAPISocketStartupStillRetries(t *testing.T) {
	for _, cause := range []syscall.Errno{syscall.ENOENT, syscall.ECONNREFUSED} {
		t.Run(cause.Error(), func(t *testing.T) {
			t.Parallel()
			var accepted, dials atomic.Int32
			client := firecrackerRetryTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
				accepted.Add(1)
				w.WriteHeader(http.StatusNoContent)
			})
			transport := client.Transport.(*http.Transport)
			connectedDial := transport.DialContext
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				if dials.Add(1) == 1 {
					return nil, &net.OpError{Op: "dial", Net: "unix", Err: &os.SyscallError{Syscall: "connect", Err: cause}}
				}
				return connectedDial(ctx, network, addr)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if err := (&JailerVMM{}).apiCallWithClient(ctx, client, http.MethodPut, "/snapshot/load", nil); err != nil {
				t.Fatalf("startup socket race failed: %v", err)
			}
			if got := dials.Load(); got != 2 {
				t.Fatalf("dial attempts = %d, want 2", got)
			}
			if got := accepted.Load(); got != 1 {
				t.Fatalf("accepted restore requests = %d, want 1", got)
			}
		})
	}
}
