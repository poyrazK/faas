package ingress

// adr: 698

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/net/http2"
)

type testDialer struct {
	addresses []string
	calls     atomic.Int32
}

func (d *testDialer) DialContext(ctx context.Context, _ string) (net.Conn, error) {
	n := int(d.calls.Add(1)) - 1
	return (&net.Dialer{}).DialContext(ctx, "tcp", d.addresses[n%len(d.addresses)])
}

func testServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(handler)
	s.Config.Protocols = new(http.Protocols)
	s.Config.Protocols.SetHTTP1(true)
	s.Config.Protocols.SetUnencryptedHTTP2(true)
	s.Start()
	t.Cleanup(s.Close)
	return s
}

func testTransport(h2 bool) http.RoundTripper {
	if h2 {
		return &http2.Transport{AllowHTTP: true, MaxHeaderListSize: uint32(api.DefaultMaxHeaderBytes)}
	}
	return &http.Transport{Proxy: nil}
}

func TestGuardUsesOneConnectionAndFreshMembershipForHTTP1AndHTTP2(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "http1", true: "h2c"}[h2], func(t *testing.T) {
			slot, session := uuid.NewString(), uuid.NewString()
			identity, err := NewIdentityHandler(testToken, slot, session)
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			proofPeer := ""
			server := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Host == IdentityHost {
					if r.Header.Get(TokenHeader) == testToken {
						t.Error("shared secret was sent over the connection")
					}
					proofPeer = r.RemoteAddr
					identity.ServeHTTP(w, r)
					return
				}
				if r.RemoteAddr != proofPeer || r.Header.Get(TokenHeader) != "" || r.Header.Get(NonceHeader) != "" {
					t.Error("forward escaped checked connection or leaked private headers")
				}
				w.Header().Set("Trailer", "Grpc-Status")
				_, _ = io.WriteString(w, "reply")
				w.Header().Set("Grpc-Status", "0")
			}))
			dialer := &testDialer{addresses: []string{server.Listener.Addr().String()}}
			checks := 0
			guard, err := New(dialer, testTransport(h2), testToken, func(ctx context.Context, got Identity) error {
				checks++
				if ctx.Err() != nil || got.SlotID != slot || got.SessionID != session {
					return ErrUnverified
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				r, _ := http.NewRequestWithContext(t.Context(), "GET", "http://logical/app", nil)
				r.Header.Set(TokenHeader, "untrusted-caller-value")
				r.Header.Set(NonceHeader, "untrusted-caller-value")
				resp, err := guard.RoundTrip(r)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if err != nil || string(body) != "reply" || resp.Trailer.Get("Grpc-Status") != "0" {
					t.Fatal(string(body), resp.Trailer, err)
				}
			}
			if checks != 2 || dialer.calls.Load() != 2 {
				t.Fatal("cached authorization or extra dial", checks, dialer.calls.Load())
			}
		})
	}
}

type closeSpy struct {
	io.Reader
	closed atomic.Bool
}

func (s *closeSpy) Close() error { s.closed.Store(true); return nil }

func TestGuardRejectsPeerReplacementWithoutForwardOrIdentityCache(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		firstSession, nextSession, slot := uuid.NewString(), uuid.NewString(), uuid.NewString()
		var calls atomic.Int32
		var servers []*httptest.Server
		for _, session := range []string{firstSession, nextSession} {
			identity, _ := NewIdentityHandler(testToken, slot, session)
			servers = append(servers, testServer(t, Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(204) }), identity)))
		}
		dialer := &testDialer{addresses: []string{servers[0].Listener.Addr().String(), servers[1].Listener.Addr().String()}}
		guard, err := New(dialer, testTransport(h2), testToken, func(_ context.Context, got Identity) error {
			if got.SessionID != firstSession {
				return errors.New("unreviewed replacement")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		r, _ := http.NewRequestWithContext(t.Context(), "GET", "http://logical/app", nil)
		resp, err := guard.RoundTrip(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		body := &closeSpy{Reader: strings.NewReader("must-not-forward")}
		r, _ = http.NewRequestWithContext(t.Context(), "POST", "http://logical/app", body)
		if _, err := guard.RoundTrip(r); !errors.Is(err, ErrUnverified) || !body.closed.Load() || calls.Load() != 1 || dialer.calls.Load() != 2 {
			t.Fatal("unreviewed replacement reached forwarding", err, body.closed.Load(), calls.Load(), dialer.calls.Load())
		}
	}
}

func TestGuardRawUpgradeRetainsProbedConnectionAndBothDirections(t *testing.T) {
	slot, session := uuid.NewString(), uuid.NewString()
	identity, _ := NewIdentityHandler(testToken, slot, session)
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
	dialer := &testDialer{addresses: []string{server.Listener.Addr().String()}}
	guard, _ := New(dialer, testTransport(true), testToken, func(_ context.Context, got Identity) error {
		if got.SlotID != slot || got.SessionID != session {
			return ErrUnverified
		}
		return nil
	})
	conn, err := guard.DialContext(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = io.WriteString(conn, "GET /app HTTP/1.1\r\nHost: app.example\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n")
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 101 {
		t.Fatal(resp.Status)
	}
	_, _ = io.WriteString(conn, "ping")
	got := make([]byte, 4)
	if _, err := io.ReadFull(reader, got); err != nil || string(got) != "ping" || dialer.calls.Load() != 1 {
		t.Fatal(string(got), err, dialer.calls.Load())
	}
}

func TestGuardProbeDeadlineDoesNotCutForwardLifetime(t *testing.T) {
	identity, _ := NewIdentityHandler(testToken, uuid.NewString(), uuid.NewString())
	started, release := make(chan struct{}), make(chan struct{})
	server := testServer(t, Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
			_, _ = io.WriteString(w, "complete")
		case <-r.Context().Done():
		}
	}), identity))
	guard, _ := New(&testDialer{addresses: []string{server.Listener.Addr().String()}}, testTransport(false), testToken, func(context.Context, Identity) error { return nil })
	done := make(chan error, 1)
	go func() {
		r, _ := http.NewRequestWithContext(t.Context(), "GET", "http://logical/app", nil)
		resp, err := guard.RoundTrip(r)
		if err == nil {
			_, err = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("forward not started")
	}
	select {
	case err := <-done:
		t.Fatal("forward ended at probe deadline", err)
	case <-time.After(api.RuntimeUpgradeIngressProbeTimeout + 50*time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("forward stuck")
	}
}

func TestGuardCancelsBlockedProbeAndMembership(t *testing.T) {
	for _, blockMembership := range []bool{false, true} {
		identity, _ := NewIdentityHandler(testToken, uuid.NewString(), uuid.NewString())
		started := make(chan struct{})
		server := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if blockMembership {
				identity.ServeHTTP(w, r)
				return
			}
			close(started)
			<-r.Context().Done()
		}))
		guard, _ := New(&testDialer{addresses: []string{server.Listener.Addr().String()}}, testTransport(false), testToken, func(ctx context.Context, _ Identity) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})
		ctx, cancel := context.WithCancel(t.Context())
		r, _ := http.NewRequestWithContext(ctx, "GET", "http://logical/app", nil)
		done := make(chan error, 1)
		go func() { _, err := guard.RoundTrip(r); done <- err }()
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("probe not started")
		}
		cancel()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("canceled proof accepted")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("canceled proof stuck")
		}
	}
}

func TestGuardPreservesDuplexUploadResponseAndTrailers(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		identity, err := NewIdentityHandler(testToken, uuid.NewString(), uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		server := testServer(t, Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor == 1 {
				if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
					t.Error(err)
					return
				}
			}
			w.Header().Set("Trailer", "Grpc-Status")
			_, _ = io.WriteString(w, "hello")
			w.(http.Flusher).Flush()
			buf := make([]byte, 4)
			if _, err := io.ReadFull(r.Body, buf); err != nil || string(buf) != "ping" {
				t.Error("duplex upload", string(buf), err)
				return
			}
			_, _ = io.WriteString(w, "pong")
			w.(http.Flusher).Flush()
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("Grpc-Status", "0")
		}), identity))
		guard, err := New(&testDialer{addresses: []string{server.Listener.Addr().String()}}, testTransport(h2), testToken, func(context.Context, Identity) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		reader, writer := io.Pipe()
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		r, _ := http.NewRequestWithContext(ctx, "POST", "http://logical/app", reader)
		resp, err := guard.RoundTrip(r)
		if err != nil {
			cancel()
			_ = writer.Close()
			t.Fatal(err)
		}
		hello := make([]byte, 5)
		if _, err := io.ReadFull(resp.Body, hello); err != nil || string(hello) != "hello" {
			cancel()
			_ = writer.Close()
			_ = resp.Body.Close()
			t.Fatal(string(hello), err)
		}
		if _, err := io.WriteString(writer, "ping"); err != nil {
			cancel()
			_ = writer.Close()
			_ = resp.Body.Close()
			t.Fatal(err)
		}
		pong := make([]byte, 4)
		if _, err := io.ReadFull(resp.Body, pong); err != nil || string(pong) != "pong" {
			cancel()
			_ = writer.Close()
			_ = resp.Body.Close()
			t.Fatal(string(pong), err)
		}
		_ = writer.Close()
		_, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		if err != nil || resp.Trailer.Get("Grpc-Status") != "0" {
			t.Fatal(resp.Trailer, err)
		}
	}
}

func TestGuardConcurrentForwardingKeepsIndependentConnections(t *testing.T) {
	identity, err := NewIdentityHandler(testToken, uuid.NewString(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	server := testServer(t, Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), identity))
	var checks atomic.Int32
	dialer := &testDialer{addresses: []string{server.Listener.Addr().String()}}
	guard, err := New(dialer, testTransport(true), testToken, func(context.Context, Identity) error { checks.Add(1); return nil })
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 32)
	for range 32 {
		go func() {
			r, _ := http.NewRequestWithContext(t.Context(), "GET", "http://logical/app", nil)
			resp, err := guard.RoundTrip(r)
			if err == nil {
				err = resp.Body.Close()
			}
			done <- err
		}()
	}
	for range 32 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if checks.Load() != 32 || dialer.calls.Load() != 32 {
		t.Fatal(checks.Load(), dialer.calls.Load())
	}
}
