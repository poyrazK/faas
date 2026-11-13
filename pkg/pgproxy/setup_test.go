package pgproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
)

type setupWaker struct{}

func (setupWaker) Wake(context.Context, string) (Backend, error) {
	return Backend{Target: "node", Node: "db-node"}, nil
}
func (setupWaker) Touch(context.Context, string, int64) error { return nil }

type setupDialer func(context.Context, Backend, string) (io.ReadWriteCloser, error)

func (d setupDialer) Dial(ctx context.Context, node Backend, id string) (io.ReadWriteCloser, error) {
	return d(ctx, node, id)
}

// setupBackend accepts startup and either stalls in authentication, stalls
// before ReadyForQuery, or completes setup. Close unblocks every operation.
func setupBackend(t *testing.T, phase string) (net.Conn, <-chan struct{}) {
	t.Helper()
	client, backend := net.Pipe()
	done := make(chan struct{})
	t.Cleanup(func() { _ = client.Close(); _ = backend.Close(); <-done })
	go func() {
		defer close(done)
		defer backend.Close()
		server := pgproto3.NewBackend(backend, backend)
		if _, err := server.ReceiveStartupMessage(); err != nil {
			return
		}
		if phase != "authentication" {
			server.Send(&pgproto3.AuthenticationOk{})
			if err := server.Flush(); err != nil {
				return
			}
		}
		if phase == "ready" {
			server.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
			if err := server.Flush(); err != nil {
				return
			}
		}
		_, _ = io.Copy(io.Discard, backend)
	}()
	return client, done
}

func TestBackendSetupDeadlineReleasesPoolCapacity(t *testing.T) {
	for _, phase := range []string{"authentication", "before_ready"} {
		t.Run(phase, func(t *testing.T) {
			client, _ := setupBackend(t, phase)
			p := &Proxy{cfg: Config{PoolWait: 30 * time.Millisecond, WakeTimeout: 30 * time.Millisecond, PoolMaxServers: 1}, waker: setupWaker{}, dialer: setupDialer(func(context.Context, Backend, string) (io.ReadWriteCloser, error) { return client, nil })}
			p.pools = newPoolSet(p)
			pl := p.pools.join(poolKey{databaseID: "database", role: "reader", dbname: "gregale", verifier: "v1"}, make([]byte, 32), make([]byte, 32), map[string]string{"user": "reader", "database": "gregale"})
			defer pl.leave()
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := pl.acquire(ctx); done <- err }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("stalled setup succeeded")
				}
			case <-time.After(time.Second):
				_ = client.Close()
				<-done
				t.Fatal("backend ignored setup deadline")
			}
			p.pools.mu.Lock()
			held := p.pools.servers["database"]
			p.pools.mu.Unlock()
			if held != 0 {
				t.Fatalf("timed-out setup retained %d server slots", held)
			}
			// The freed slot serves another setup, whose stream survives the
			// setup context ending after ReadyForQuery.
			fresh, closed := setupBackend(t, "ready")
			p.dialer = setupDialer(func(context.Context, Backend, string) (io.ReadWriteCloser, error) { return fresh, nil })
			ctxReady, cancelReady := context.WithCancel(t.Context())
			p.cfg.PoolWait = time.Second
			p.cfg.WakeTimeout = time.Second
			srv, err := pl.acquire(ctxReady)
			if err != nil {
				t.Fatal("released pool slot unusable", err)
			}
			cancelReady()
			if _, err := srv.rw.Write([]byte("session continues")); err != nil {
				t.Fatal("successful setup retained cancellation", err)
			}
			select {
			case <-closed:
				t.Fatal("completed setup stream was closed")
			default:
			}
			pl.put(srv, false)
		})
	}
}

func TestBackendSetupCancellationDetachesAfterSuccess(t *testing.T) {
	for _, cancelBefore := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancelled", false: "ready"}[cancelBefore], func(t *testing.T) {
			left, right := net.Pipe()
			defer left.Close()
			defer right.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			finish := interruptSetup(ctx, left)
			if cancelBefore {
				cancel()
				if err := finish(); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else {
				if err := finish(); err != nil {
					t.Fatal(err)
				}
				cancel()
			}
			// The helper's finish is idempotent on both outcomes.
			if err := finish(); cancelBefore != errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}
