package main

// adr: 615

import (
	"bufio"
	"context"
	"errors"
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
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/gateway/ingress"
	"github.com/onebox-faas/faas/pkg/state"
)

type publicWithdrawalFixture struct {
	*publicActivityFixture
	wmu              sync.Mutex
	intent           string
	failures         int
	sealed           state.RuntimeUpgradePublicEdgeWithdrawalSnapshot
	guardCalls       atomic.Int32
	started, release chan struct{}
	once             sync.Once
}

func (s *publicWithdrawalFixture) RecordRuntimeUpgradePublicEdgeGuard(context.Context, state.RuntimeUpgradePublicEdgeMember) error {
	s.guardCalls.Add(1)
	return nil
}

func (s *publicWithdrawalFixture) AuthorizeRuntimeUpgradePublicEdgeIngress(ctx context.Context, m state.RuntimeUpgradePublicEdgeMember, slot, session string) (state.RuntimeUpgradePublicIngressBinding, error) {
	b, err := s.publicActivityFixture.AuthorizeRuntimeUpgradePublicEdgeIngress(ctx, m, slot, session)
	if s.started != nil {
		s.once.Do(func() { close(s.started) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return state.RuntimeUpgradePublicIngressBinding{}, ctx.Err()
		}
	}
	return b, err
}

func (s *publicWithdrawalFixture) RepairRuntimeUpgradePublicEdgeWithdrawal(_ context.Context, m state.RuntimeUpgradePublicEdgeMember, install func(string) (state.RuntimeUpgradePublicEdgeWithdrawalSnapshot, error)) (bool, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.intent == "" {
		return false, nil
	}
	s.publicActivityFixture.mu.Lock()
	exact := m == s.member
	s.publicActivityFixture.mu.Unlock()
	if !exact {
		return false, state.ErrConflict
	}
	a, err := install(s.intent)
	if err != nil {
		return true, err
	}
	if s.failures > 0 {
		s.failures--
		return true, errors.New("synthetic lost receipt publication")
	}
	if a.Closed && a.Known && a.Active == 0 {
		s.sealed = a
	}
	return true, nil
}

func publicWithdrawalEnv(slot string) func(string) string {
	base := publicActivityEnv(slot)
	return func(k string) string {
		if k == "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_WITHDRAWAL" {
			return "1"
		}
		return base(k)
	}
}

func withdrawalProxy(t *testing.T, h2 bool, h http.Handler, s *publicWithdrawalFixture) (*gateway.InternalReverseProxy, *runtimePublicEdgeObserver) {
	t.Helper()
	identity, err := ingress.NewIdentityHandler(ingressTestToken, s.slot, s.session)
	if err != nil {
		t.Fatal(err)
	}
	p := ingressTestProxy(t, h2, ingress.Wrap(h, identity), s)
	getenv := publicWithdrawalEnv(uuid.NewString())
	o, err := prepareRuntimePublicEdgeObserver(p, s, publicEdgeConfig(internalUpstreamUnix, h2, defaultListenAddr, nil, getenv), getenv, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.member = o.member
	s.mu.Unlock()
	return p, o
}

func newPublicWithdrawalFixture() *publicWithdrawalFixture {
	return &publicWithdrawalFixture{publicActivityFixture: &publicActivityFixture{ingressBindingFixture: &ingressBindingFixture{slot: uuid.NewString(), session: uuid.NewString()}, revision: uuid.NewString()}}
}

func waitPublicWithdrawalSeal(t *testing.T, o *runtimePublicEdgeObserver, s *publicWithdrawalFixture) state.RuntimeUpgradePublicEdgeWithdrawalSnapshot {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if err := o.repair(t.Context()); err != nil {
			t.Fatal(err)
		}
		s.wmu.Lock()
		a := s.sealed
		s.wmu.Unlock()
		if a.Closed && a.Known && a.Active == 0 {
			return a
		}
		if time.Now().After(deadline) {
			t.Fatal("public forward did not drain")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPublicWithdrawalRequiresDependenciesStoreAndProtocolFingerprint(t *testing.T) {
	s := newPublicWithdrawalFixture()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	getenv := publicWithdrawalEnv(uuid.NewString())
	config := publicEdgeConfig(internalUpstreamUnix, true, defaultListenAddr, nil, getenv)
	if config.Protocol != "adr615/withdrawal-v1" {
		t.Fatal(config)
	}
	for _, missing := range []string{"FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_ACTIVITY", "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_CONFIRMATION"} {
		p := ingressTestProxy(t, true, h, s)
		if _, err := prepareRuntimePublicEdgeObserver(p, s, config, func(k string) string {
			if k == missing {
				return ""
			}
			return getenv(k)
		}, log); err == nil {
			t.Fatal("missing withdrawal dependency accepted", missing)
		}
	}
	p := ingressTestProxy(t, true, h, s)
	if _, err := prepareRuntimePublicEdgeObserver(p, s.publicActivityFixture, config, getenv, log); err == nil {
		t.Fatal("missing withdrawal store accepted")
	}
	p = ingressTestProxy(t, true, h, s)
	o, err := prepareRuntimePublicEdgeObserver(p, s, config, getenv, log)
	if err != nil || o.withdrawalStore != s || o.activity == nil {
		t.Fatal(o, err)
	}
	plain := ingressTestProxy(t, true, h, s)
	defaultEnv := func(k string) string {
		if k == "FAAS_RUNTIME_UPGRADE_PUBLIC_EDGE_WITHDRAWAL" {
			return ""
		}
		return getenv(k)
	}
	before, err := prepareRuntimePublicEdgeObserver(plain, s, config, defaultEnv, log)
	if err != nil || before.withdrawalStore != nil || before.member.ConfigSHA256 == o.member.ConfigSHA256 {
		t.Fatal("withdrawal default or installed fingerprint incorrect", err)
	}
}

func TestPublicWithdrawalHTTPStreamsDrainNaturallyAndRejectNewForwards(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "http1", true: "h2c"}[h2], func(t *testing.T) {
			s := newPublicWithdrawalFixture()
			release := make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			var forwarded atomic.Int32
			p, o := withdrawalProxy(t, h2, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				forwarded.Add(1)
				_, _ = io.WriteString(w, "part\n")
				w.(http.Flusher).Flush()
				select {
				case <-release:
					_, _ = io.WriteString(w, "tail\n")
				case <-r.Context().Done():
				}
			}), s)
			edge := httptest.NewServer(p)
			defer edge.Close()
			client := &http.Client{Timeout: 3 * time.Second}
			resp, err := client.Get(edge.URL + "/app")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			part := make([]byte, 5)
			if _, err := io.ReadFull(resp.Body, part); err != nil || string(part) != "part\n" {
				t.Fatal(string(part), err)
			}
			s.wmu.Lock()
			s.intent = uuid.NewString()
			s.wmu.Unlock()
			if err := o.repair(t.Context()); err != nil {
				t.Fatal(err)
			}
			s.wmu.Lock()
			sealed := s.sealed
			s.wmu.Unlock()
			if sealed.Closed {
				t.Fatal("held stream sealed early", sealed)
			}
			checks := s.checks.Load()
			denied, err := client.Get(edge.URL + "/new")
			if err != nil {
				t.Fatal(err)
			}
			_ = denied.Body.Close()
			if denied.StatusCode != 503 || forwarded.Load() != 1 || s.checks.Load() != checks || s.guardCalls.Load() != 0 {
				t.Fatal("withdrawal forwarded or tried current facts", denied.StatusCode, forwarded.Load(), s.checks.Load(), s.guardCalls.Load())
			}
			close(release)
			tail, err := io.ReadAll(resp.Body)
			if err != nil || string(tail) != "tail\n" {
				t.Fatal("withdrawal interrupted old stream", string(tail), err)
			}
			_ = resp.Body.Close()
			seal := waitPublicWithdrawalSeal(t, o, s)
			for range 3 {
				if err := o.repair(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			s.wmu.Lock()
			again := s.sealed
			s.wmu.Unlock()
			if again != seal {
				t.Fatal("sealed process changed", again, seal)
			}
		})
	}
}

func TestPublicWithdrawalLateAuthorizationCannotStartForwarding(t *testing.T) {
	s := newPublicWithdrawalFixture()
	s.started, s.release = make(chan struct{}), make(chan struct{})
	var forwarded atomic.Int32
	p, o := withdrawalProxy(t, true, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { forwarded.Add(1); w.WriteHeader(204) }), s)
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		p.ServeHTTP(w, httptest.NewRequest("GET", "http://app.example/app", nil))
		done <- w.Code
	}()
	select {
	case <-s.started:
	case <-time.After(time.Second):
		t.Fatal("authorization not reached")
	}
	s.wmu.Lock()
	s.intent = uuid.NewString()
	s.wmu.Unlock()
	if err := o.repair(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.wmu.Lock()
	sealed := s.sealed
	s.wmu.Unlock()
	if sealed.Closed {
		t.Fatal("late authorization omitted")
	}
	close(s.release)
	select {
	case status := <-done:
		if status != 503 || forwarded.Load() != 0 {
			t.Fatal("late old authorization forwarded", status, forwarded.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("late authorization stuck")
	}
	waitPublicWithdrawalSeal(t, o, s)
}

func TestPublicWithdrawalRawUpgradeRemainsDuplexUntilClientCloses(t *testing.T) {
	s := newPublicWithdrawalFixture()
	p, o := withdrawalProxy(t, true, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n")
		_ = rw.Flush()
		_, _ = io.Copy(conn, rw)
	}), s)
	edge := httptest.NewServer(p)
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
	s.wmu.Lock()
	s.intent = uuid.NewString()
	s.wmu.Unlock()
	if err := o.repair(t.Context()); err != nil {
		t.Fatal(err)
	}
	s.wmu.Lock()
	sealed := s.sealed
	s.wmu.Unlock()
	if sealed.Closed {
		t.Fatal("live upgrade sealed")
	}
	_, _ = io.WriteString(conn, "ping")
	reply := make([]byte, 4)
	if _, err := io.ReadFull(reader, reply); err != nil || string(reply) != "ping" {
		t.Fatal("withdrawal or dial cancellation broke duplex", string(reply), err)
	}
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("GET", "http://app.example/new", nil))
	if w.Code != 503 {
		t.Fatal("new forward crossed upgrade withdrawal", w.Code)
	}
	_ = conn.Close()
	waitPublicWithdrawalSeal(t, o, s)
}

func TestPublicWithdrawalFailedPublicationKeepsFenceAndObserverRepairsBeforeFacts(t *testing.T) {
	s := newPublicWithdrawalFixture()
	p, o := withdrawalProxy(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), s)
	s.wmu.Lock()
	s.intent, s.failures = uuid.NewString(), 1
	s.wmu.Unlock()
	if err := o.repair(t.Context()); err == nil {
		t.Fatal("lost receipt not surfaced")
	}
	w := httptest.NewRecorder()
	p.ServeHTTP(w, httptest.NewRequest("GET", "http://app.example/app", nil))
	if w.Code != 503 || s.checks.Load() != 0 {
		t.Fatal("failed publication reopened admission", w.Code, s.checks.Load())
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { o.run(ctx); close(done) }()
	deadline := time.Now().Add(time.Second)
	for {
		s.wmu.Lock()
		a := s.sealed
		s.wmu.Unlock()
		if a.Closed && a.Known && a.Active == 0 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("listener observer did not repair withdrawal")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("withdrawal observer did not stop")
	}
	if s.guardCalls.Load() != 0 {
		t.Fatal("withdrawal repair attempted current guard facts")
	}
}
