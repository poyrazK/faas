package main

// adr: 612

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
	"github.com/onebox-faas/faas/pkg/state"
)

const ingressTestToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

type ingressBindingFixture struct {
	mu            sync.RWMutex
	slot, session string
	checks        atomic.Int32
}

func (s *ingressBindingFixture) AuthorizeRuntimeUpgradeGatewayIngress(ctx context.Context, slot, session string) (state.RuntimeUpgradeIngressBinding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.checks.Add(1)
	if _, ok := ctx.Deadline(); !ok || slot != s.slot || session != s.session {
		return state.RuntimeUpgradeIngressBinding{}, state.ErrConflict
	}
	return state.RuntimeUpgradeIngressBinding{SlotID: slot, SessionID: session, GatewayRosterRevision: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ValidForSeconds: 30, CheckedAt: time.Now().UTC()}, nil
}

func (s *ingressBindingFixture) replaceSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session = uuid.NewString()
}

func ingressTestEnv(key string) string {
	if key == "FAAS_RUNTIME_UPGRADE_INGRESS_CONFIRMATION" {
		return "1"
	}
	if key == "FAAS_RUNTIME_UPGRADE_INGRESS_TOKEN" {
		return ingressTestToken
	}
	return ""
}

func ingressTestProxy(t *testing.T, h2 bool, handler http.Handler, store state.RuntimeUpgradeIngressBindingStore) *gateway.InternalReverseProxy {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Config.Protocols = new(http.Protocols)
	server.Config.Protocols.SetHTTP1(true)
	server.Config.Protocols.SetUnencryptedHTTP2(true)
	server.Start()
	t.Cleanup(server.Close)
	target, _ := url.Parse("http://internal")
	proxy := gateway.NewInternalReverseProxy(gateway.NewTCPDialer(server.Listener.Addr().String()), target, slog.New(slog.NewTextHandler(io.Discard, nil)), h2)
	if err := configureRuntimeIngressProxy(proxy, store, ingressTestEnv); err != nil {
		t.Fatal(err)
	}
	return proxy
}

func TestPrivateIngressProxyDefaultsOffWithoutChangingTransports(t *testing.T) {
	target, _ := url.Parse("http://internal")
	proxy := gateway.NewInternalReverseProxy(gateway.NewTCPDialer("127.0.0.1:1"), target, nil, true)
	beforeDialer, beforeTransport := proxy.Dialer, proxy.Transport
	if err := configureRuntimeIngressProxy(proxy, nil, func(string) string { return "" }); err != nil || proxy.Dialer != beforeDialer || proxy.Transport != beforeTransport {
		t.Fatal("default-off proxy changed", err)
	}
	if err := configureRuntimeIngressProxy(proxy, nil, ingressTestEnv); err == nil {
		t.Fatal("missing authoritative store accepted")
	}
}

func TestPrivateIngressProxyProtectsHTTPAndH2CAndRejectsUnknownProcess(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		store := &ingressBindingFixture{slot: uuid.NewString(), session: uuid.NewString()}
		identity, err := ingress.NewIdentityHandler(ingressTestToken, store.slot, store.session)
		if err != nil {
			t.Fatal(err)
		}
		var forwards atomic.Int32
		proxy := ingressTestProxy(t, h2, ingress.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwards.Add(1); _, _ = io.WriteString(w, "received") }), identity), store)
		r := httptest.NewRequest("GET", "http://app.example/test", nil)
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != "received" || store.checks.Load() != 1 {
			t.Fatal(w.Code, w.Body.String(), store.checks.Load())
		}
		store.replaceSession() // trusted review changed; old process cannot borrow it.
		w = httptest.NewRecorder()
		proxy.ServeHTTP(w, r)
		if w.Code != 503 || forwards.Load() != 1 || store.checks.Load() != 2 {
			t.Fatal("unreviewed upstream reached app", w.Code, forwards.Load(), store.checks.Load())
		}
	}
}

func TestPrivateIngressProxyUpgradeUsesCheckedConnectionAndFailsClosed(t *testing.T) {
	store := &ingressBindingFixture{slot: uuid.NewString(), session: uuid.NewString()}
	identity, err := ingress.NewIdentityHandler(ingressTestToken, store.slot, store.session)
	if err != nil {
		t.Fatal(err)
	}
	var upgrades atomic.Int32
	proxy := ingressTestProxy(t, true, ingress.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upgrades.Add(1)
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(conn, rw)
	}), identity), store)
	edge := httptest.NewServer(proxy)
	defer edge.Close()
	for _, status := range []int{101, 503} {
		conn, err := net.DialTimeout("tcp", edge.Listener.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = io.WriteString(conn, "GET /app HTTP/1.1\r\nHost: app.example\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n")
		reader := bufio.NewReader(conn)
		resp, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		if resp.StatusCode != status {
			conn.Close()
			t.Fatal(resp.Status)
		}
		if status == 101 {
			_, _ = io.WriteString(conn, "ping")
			got := make([]byte, 4)
			if _, err := io.ReadFull(reader, got); err != nil || string(got) != "ping" {
				conn.Close()
				t.Fatal(string(got), err)
			}
		}
		_ = resp.Body.Close()
		_ = conn.Close()
		store.replaceSession()
	}
	if upgrades.Load() != 1 || store.checks.Load() != 2 {
		t.Fatal(upgrades.Load(), store.checks.Load())
	}
}
