package ingress

// adr: 700

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func trackedTestGuard(t *testing.T, g *Guard, tracker *ActivityTracker, authorize func(context.Context, Identity) (Generation, error)) *Guard {
	t.Helper()
	tracked, err := g.WithAdmissions(func() (Authorize, func(), error) {
		a, err := tracker.Begin()
		if err != nil {
			return nil, nil, err
		}
		return func(ctx context.Context, id Identity) error {
			generation, err := authorize(ctx, id)
			if err != nil {
				return err
			}
			return a.Bind(generation)
		}, a.Finish, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return tracked
}

func waitActivityZero(t *testing.T, tk *ActivityTracker, g Generation) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		a := tk.Snapshot(g)
		if a.Pending+a.Current+a.Previous == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("forward scope leaked", a)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestGuardAdmissionsRetainOldHTTPBodiesAndCancelStreams(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, cancelStream := range []bool{false, true} {
			t.Run(map[bool]string{false: "http1", true: "h2c"}[h2]+map[bool]string{false: "/close", true: "/cancel"}[cancelStream], func(t *testing.T) {
				identity, _ := NewIdentityHandler(testToken, uuid.NewString(), uuid.NewString())
				server := testServer(t, Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.WriteString(w, "hello")
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}), identity))
				g, _ := New(&testDialer{addresses: []string{server.Listener.Addr().String()}}, testTransport(h2), testToken, func(context.Context, Identity) error {
					t.Error("old authorizer bypassed tracked binding")
					return ErrUnverified
				})
				tk, old, next := NewActivityTracker(), testGeneration(), testGeneration()
				started, release := make(chan struct{}), make(chan struct{})
				g = trackedTestGuard(t, g, tk, func(ctx context.Context, _ Identity) (Generation, error) {
					close(started)
					select {
					case <-release:
						return old, nil
					case <-ctx.Done():
						return Generation{}, ctx.Err()
					}
				})
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				r, _ := http.NewRequestWithContext(ctx, "GET", "http://logical/app", nil)
				type result struct {
					resp *http.Response
					err  error
				}
				ch := make(chan result, 1)
				go func() { resp, err := g.RoundTrip(r); ch <- result{resp, err} }() //nolint:bodyclose // The receiver reads the streaming response and closes its body.
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("authorization not reached")
				}
				if a := tk.Snapshot(next); a.Pending != 1 || a.Previous != 0 {
					t.Fatal(a)
				}
				close(release)
				var out result
				select {
				case out = <-ch:
				case <-time.After(time.Second):
					t.Fatal("forward not started")
				}
				if out.err != nil {
					t.Fatal(out.err)
				}
				defer out.resp.Body.Close()
				buf := make([]byte, 5)
				if _, err := io.ReadFull(out.resp.Body, buf); err != nil || string(buf) != "hello" {
					t.Fatal(string(buf), err)
				}
				if a := tk.Snapshot(next); a.Pending != 0 || a.Previous != 1 || a.Current != 0 {
					t.Fatal("body release lost older admission", a)
				}
				if cancelStream {
					cancel()
				} else {
					_ = out.resp.Body.Close()
					_ = out.resp.Body.Close()
				}
				waitActivityZero(t, tk, next)
			})
		}
	}
}

func TestGuardAdmissionsRetainRawUpgradeUntilConnectionClose(t *testing.T) {
	identity, _ := NewIdentityHandler(testToken, uuid.NewString(), uuid.NewString())
	server := testServer(t, Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(conn, rw)
	}), identity))
	g, _ := New(&testDialer{addresses: []string{server.Listener.Addr().String()}}, testTransport(true), testToken, func(context.Context, Identity) error { return nil })
	tk, old, next := NewActivityTracker(), testGeneration(), testGeneration()
	g = trackedTestGuard(t, g, tk, func(context.Context, Identity) (Generation, error) { return old, nil })
	dialCtx, cancelDial := context.WithCancel(t.Context())
	conn, err := g.DialContext(dialCtx, "")
	cancelDial() // proxy dialWithTimeout cancels its dial-only context on return
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	_, _ = io.WriteString(conn, "GET /app HTTP/1.1\r\nHost: app.example\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n")
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil || resp.StatusCode != 101 {
		t.Fatal(resp, err)
	}
	defer resp.Body.Close()
	_, _ = io.WriteString(conn, "ping")
	got := make([]byte, 4)
	if _, err := io.ReadFull(reader, got); err != nil || string(got) != "ping" {
		t.Fatal(string(got), err)
	}
	if a := tk.Snapshot(next); a.Previous != 1 {
		t.Fatal(a)
	}
	_ = conn.Close()
	_ = conn.Close()
	waitActivityZero(t, tk, next)
}

func TestGuardAdmissionsReleaseFailedAndCancelledProbes(t *testing.T) {
	for _, raw := range []bool{false, true} {
		identity, _ := NewIdentityHandler(testToken, uuid.NewString(), uuid.NewString())
		server := testServer(t, identity)
		g, _ := New(&testDialer{addresses: []string{server.Listener.Addr().String()}}, testTransport(false), testToken, func(context.Context, Identity) error { return nil })
		tk, next := NewActivityTracker(), testGeneration()
		started := make(chan struct{})
		g = trackedTestGuard(t, g, tk, func(ctx context.Context, _ Identity) (Generation, error) {
			close(started)
			<-ctx.Done()
			return Generation{}, ctx.Err()
		})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		body := &closeSpy{Reader: strings.NewReader("must not forward")}
		go func() {
			if raw {
				conn, err := g.DialContext(ctx, "")
				if conn != nil {
					_ = conn.Close()
				}
				done <- err
				return
			}
			r, _ := http.NewRequestWithContext(ctx, "POST", "http://logical/app", body)
			resp, err := g.RoundTrip(r)
			if resp != nil {
				_ = resp.Body.Close()
			}
			done <- err
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("authorization not reached")
		}
		cancel()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("cancel admitted work")
			}
		case <-time.After(time.Second):
			t.Fatal("cancelled probe stuck")
		}
		if !raw && !body.closed.Load() {
			t.Fatal("rejected request body retained")
		}
		waitActivityZero(t, tk, next)
	}
}

func TestGuardAdmissionsPreserveDuplexUpgradeBodyWriter(t *testing.T) {
	identity, _ := NewIdentityHandler(testToken, uuid.NewString(), uuid.NewString())
	server := testServer(t, Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(conn, rw)
	}), identity))
	g, _ := New(&testDialer{addresses: []string{server.Listener.Addr().String()}}, testTransport(false), testToken, func(context.Context, Identity) error { return nil })
	tk, old, next := NewActivityTracker(), testGeneration(), testGeneration()
	g = trackedTestGuard(t, g, tk, func(context.Context, Identity) (Generation, error) { return old, nil })
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", "http://logical/app", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "test")
	resp, err := g.RoundTrip(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	w, ok := resp.Body.(io.Writer)
	if !ok || resp.StatusCode != 101 {
		t.Fatal("duplex writer lost", resp.Status)
	}
	if _, err := io.WriteString(w, "ping"); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 4)
	if _, err := io.ReadFull(resp.Body, got); err != nil || string(got) != "ping" {
		t.Fatal(string(got), err)
	}
	if a := tk.Snapshot(next); a.Previous != 1 {
		t.Fatal(a)
	}
	_ = resp.Body.Close()
	waitActivityZero(t, tk, next)
}
