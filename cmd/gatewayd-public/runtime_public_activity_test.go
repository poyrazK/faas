package main

// adr: 614

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
	"github.com/onebox-faas/faas/pkg/state"
)

type publicActivityFixture struct {
	*ingressBindingFixture
	mu       sync.Mutex
	member   state.RuntimeUpgradePublicEdgeMember
	revision string
	checks   atomic.Int32
	recorded state.RuntimeUpgradePublicEdgeActivity
}

func (s *publicActivityFixture) RecordRuntimeUpgradePublicEdgeGuard(context.Context, state.RuntimeUpgradePublicEdgeMember) error {
	return nil
}

func (s *publicActivityFixture) AuthorizeRuntimeUpgradePublicEdgeIngress(ctx context.Context, m state.RuntimeUpgradePublicEdgeMember, slot, session string) (state.RuntimeUpgradePublicIngressBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks.Add(1)
	if m != s.member {
		return state.RuntimeUpgradePublicIngressBinding{}, state.ErrConflict
	}
	b, err := s.ingressBindingFixture.AuthorizeRuntimeUpgradeGatewayIngress(ctx, slot, session)
	return state.RuntimeUpgradePublicIngressBinding{RuntimeUpgradeIngressBinding: b, RuntimeUpgradeIngressGeneration: state.RuntimeUpgradeIngressGeneration{PublicRevision: s.revision, GatewayRevision: b.GatewayRosterRevision}}, err
}

func (s *publicActivityFixture) RecordRuntimeUpgradePublicEdgeActivity(ctx context.Context, m state.RuntimeUpgradePublicEdgeMember, snapshot func(state.RuntimeUpgradeIngressGeneration) state.RuntimeUpgradePublicEdgeActivity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m != s.member {
		return state.ErrConflict
	}
	s.recorded = snapshot(state.RuntimeUpgradeIngressGeneration{PublicRevision: s.revision, GatewayRevision: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"})
	return nil
}

func publicActivityEnv(slot string) func(string) string {
	return func(k string) string {
		switch k {
		case "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_ACTIVITY", "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_CONFIRMATION":
			return "1"
		case "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_SLOT_ID":
			return slot
		}
		return ingressTestEnv(k)
	}
}

func TestPublicActivityRequiresDependentFlagsStoreAndVersionedConfig(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	base := &ingressBindingFixture{slot: uuid.NewString(), session: uuid.NewString()}
	proxy := ingressTestProxy(t, true, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), base)
	getenv := publicActivityEnv(uuid.NewString())
	config := publicEdgeConfig(internalUpstreamUnix, true, defaultListenAddr, nil, getenv)
	if config.Protocol != "adr614/generation-v1" {
		t.Fatal(config)
	}
	if _, err := prepareRuntimePublicEdgeObserver(proxy, &publicGuardFactProbe{}, config, getenv, log); err == nil {
		t.Fatal("missing generation store accepted")
	}
	if _, err := prepareRuntimePublicEdgeObserver(proxy, nil, config, func(k string) string {
		if k == "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_ACTIVITY" {
			return "1"
		}
		return ""
	}, log); err == nil {
		t.Fatal("missing dependent flag accepted")
	}
	store := &publicActivityFixture{ingressBindingFixture: base, revision: uuid.NewString()}
	o, err := prepareRuntimePublicEdgeObserver(proxy, store, config, getenv, log)
	if err != nil {
		t.Fatal(err)
	}
	if o.activity == nil || o.activityStore != store {
		t.Fatal("tracking not installed")
	}
	dialer := proxy.Dialer.(runtimeIngressProxy)
	transport := proxy.Transport.(runtimeIngressProxy)
	if dialer.Guard != transport.Guard {
		t.Fatal("HTTP and raw upgrade trackers diverged")
	}
	plainConfig := publicEdgeConfig(internalUpstreamUnix, true, defaultListenAddr, nil, func(k string) string {
		if k == "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_ACTIVITY" {
			return ""
		}
		return getenv(k)
	})
	plainProxy := ingressTestProxy(t, true, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), base)
	before := plainProxy.Dialer
	plain, err := prepareRuntimePublicEdgeObserver(plainProxy, store, plainConfig, func(k string) string {
		if k == "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_ACTIVITY" {
			return ""
		}
		return getenv(k)
	}, log)
	if err != nil || plain.activity != nil || plainProxy.Dialer != before || plain.member.ConfigSHA256 == o.member.ConfigSHA256 {
		t.Fatal("default-off/config binding failed", err)
	}
}

func TestPublicActivityBindsActualProxyAndRejectsUnreviewedEdge(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		store := &publicActivityFixture{ingressBindingFixture: &ingressBindingFixture{slot: uuid.NewString(), session: uuid.NewString()}, revision: uuid.NewString()}
		identity, _ := ingress.NewIdentityHandler(ingressTestToken, store.slot, store.session)
		var forwarded atomic.Int32
		proxy := ingressTestProxy(t, h2, ingress.Wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { forwarded.Add(1); _, _ = io.WriteString(w, "reply") }), identity), store)
		getenv := publicActivityEnv(uuid.NewString())
		o, err := prepareRuntimePublicEdgeObserver(proxy, store, publicEdgeConfig(internalUpstreamUnix, h2, defaultListenAddr, nil, getenv), getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("GET", "http://app.example/app", nil)
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, r)
		if w.Code != 503 || forwarded.Load() != 0 {
			t.Fatal("unreviewed public process forwarded", w.Code, forwarded.Load())
		}
		store.mu.Lock()
		store.member = o.member
		store.mu.Unlock()
		w = httptest.NewRecorder()
		proxy.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != "reply" || forwarded.Load() != 1 {
			t.Fatal(w.Code, w.Body.String(), forwarded.Load())
		}
		// The proxy's body-copy goroutine closes after delivering EOF to the
		// handler. Retain activity until that close completes, even if the
		// handler has just returned; it must then converge to known zero.
		deadline := time.Now().Add(time.Second)
		var got state.RuntimeUpgradePublicEdgeActivity
		for {
			if err := o.recordActivity(t.Context()); err != nil {
				t.Fatal(err)
			}
			store.mu.Lock()
			got = store.recorded
			store.mu.Unlock()
			if got.Pending+got.Current+got.Previous == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("actual forward scope leaked", got)
			}
			time.Sleep(time.Millisecond)
		}
		if !got.Known || got.Version < 4 || got.Pending+got.Current+got.Previous != 0 || store.checks.Load() != 2 {
			t.Fatal("actual forward scope leaked", got, store.checks.Load())
		}
		store.mu.Lock()
		store.member.SessionID = uuid.NewString()
		store.mu.Unlock()
		w = httptest.NewRecorder()
		proxy.ServeHTTP(w, r)
		if w.Code != 503 || forwarded.Load() != 1 {
			t.Fatal("old public session reused replacement review", w.Code, forwarded.Load())
		}
	}
}

func TestPublicActivityRawUpgradeOutlivesDialContextAndRetainsOldGeneration(t *testing.T) {
	store := &publicActivityFixture{ingressBindingFixture: &ingressBindingFixture{slot: uuid.NewString(), session: uuid.NewString()}, revision: uuid.NewString()}
	identity, _ := ingress.NewIdentityHandler(ingressTestToken, store.slot, store.session)
	proxy := ingressTestProxy(t, true, ingress.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	getenv := publicActivityEnv(uuid.NewString())
	o, err := prepareRuntimePublicEdgeObserver(proxy, store, publicEdgeConfig(internalUpstreamUnix, true, defaultListenAddr, nil, getenv), getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.member = o.member
	store.mu.Unlock()
	edge := httptest.NewServer(proxy)
	defer edge.Close()
	conn, err := net.DialTimeout("tcp", edge.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
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
	store.mu.Lock()
	store.revision = uuid.NewString()
	store.mu.Unlock()
	if err := o.recordActivity(t.Context()); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	a := store.recorded
	store.mu.Unlock()
	if !a.Known || a.Previous != 1 || a.Current != 0 {
		t.Fatal("upgrade disappeared on review", a)
	}
	_ = conn.Close()
	deadline := time.Now().Add(time.Second)
	for {
		if err := o.recordActivity(t.Context()); err != nil {
			t.Fatal(err)
		}
		store.mu.Lock()
		a = store.recorded
		store.mu.Unlock()
		if a.Pending+a.Current+a.Previous == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnected upgraded scope retained", a)
		}
		time.Sleep(time.Millisecond)
	}
}
