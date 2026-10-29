package gateway

// adr: 20261022184531 — gatewayd-public retries responses that were never
// dispatched (step 5).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

func TestWakeNotDispatchedReason(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string // "" = not marked
	}{
		{name: "no error", err: nil},
		{name: "the wake hold expired", err: fmt.Errorf("wake: %w", ErrWakeQueueWaitTimeout), want: NotDispatchedWakeHoldExpired},
		// production-us 2026-10-22: fsn-3's vmmd refused every wake.
		{name: "scan_critical refusal", err: api.NewProblem(http.StatusServiceUnavailable, api.CodeScanCritical, "runtime base generation unavailable", "x"), want: NotDispatchedWakeRefused},
		{name: "platform internal error", err: api.ErrInternal("schedd failed"), want: NotDispatchedWakeRefused},
		{name: "fleet capacity is the same from every node", err: api.ErrCapacity("no capacity")},
		{name: "the app failed to start", err: api.NewProblem(http.StatusServiceUnavailable, api.CodeAppStartupCrash, "crash", "x")},
		{name: "the app's concurrency queue is full", err: ErrConcurrencyQueueFull},
		{name: "this gateway's wake queue is full", err: ErrQueueFull},
		{name: "an unclassified error", err: errors.New("boom")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := wakeNotDispatchedReason(tc.err)
			if got != tc.want || ok != (tc.want != "") {
				t.Fatalf("wakeNotDispatchedReason = %q, %v; want %q", got, ok, tc.want)
			}
		})
	}
}

func TestNotDispatchedMarker(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set(DispatchNonceHeader, "n1")
	r = takeDispatchNonce(r)
	if r.Header.Get(DispatchNonceHeader) != "" {
		t.Fatal("the nonce stayed in the request headers")
	}
	w := httptest.NewRecorder()
	markNotDispatched(w, r, NotDispatchedWakeRefused)
	value := w.Header().Get(NotDispatchedHeader)
	if reason, ok := parseNotDispatched(value, "n1"); !ok || reason != NotDispatchedWakeRefused {
		t.Fatalf("marker %q parses as %q, %v", value, reason, ok)
	}
	for _, tc := range []struct{ value, nonce string }{
		{value, "n2"},                     // another request's nonce
		{value, ""},                       // the edge sent none
		{"wake_refused", "n1"},            // no nonce
		{"rebooted; nonce=n1", "n1"},      // unknown reason
		{"wake_refused; nonce=n1x", "n1"}, // prefix of a nonce
	} {
		if reason, ok := parseNotDispatched(tc.value, tc.nonce); ok {
			t.Fatalf("parseNotDispatched(%q, %q) = %q, want no marker", tc.value, tc.nonce, reason)
		}
	}
	// Without a nonce from the edge, gatewayd-internal marks nothing.
	plain := httptest.NewRecorder()
	markNotDispatched(plain, takeDispatchNonce(httptest.NewRequest(http.MethodGet, "/", nil)), NotDispatchedWakeRefused)
	if got := plain.Header().Get(NotDispatchedHeader); got != "" {
		t.Fatalf("marker %q without a nonce", got)
	}
}

// A guest that sets the marker itself must not make the edge replay a
// request it already saw.
func TestForwardedResponseHeaderDropsAGuestNotDispatchedMarker(t *testing.T) {
	dst := http.Header{}
	forwardedResponseHeader(context.Background(), dst, "x-faas-not-dispatched", "wake_refused; nonce=guess")
	forwardedResponseHeader(context.Background(), dst, "X-App", "kept")
	if got := dst.Get(NotDispatchedHeader); got != "" {
		t.Fatalf("guest marker forwarded: %q", got)
	}
	if dst.Get("X-App") != "kept" {
		t.Fatal("an ordinary guest header was dropped")
	}
}

func TestFailoverBudget(t *testing.T) {
	b := newFailoverBudget(10, 2)
	if !b.spend() || !b.spend() {
		t.Fatal("a quiet edge cannot spend its burst")
	}
	if b.spend() {
		t.Fatal("spent beyond the burst")
	}
	for range 9 {
		b.deposit()
	}
	if b.spend() {
		t.Fatal("9 requests earned a retry at 10 %")
	}
	b.deposit()
	if !b.spend() {
		t.Fatal("10 requests did not earn a retry at 10 %")
	}
	for range 1000 {
		b.deposit()
	}
	if !b.spend() || !b.spend() || b.spend() {
		t.Fatal("the bank holds more than its burst")
	}
}

// failoverNode is one compute node's gatewayd-internal in the failover tests.
type failoverNode struct {
	name   string
	mode   string // "serve", "refuse", "forge"
	server *httptest.Server
	mu     sync.Mutex
	bodies []string
	nonces []string
}

func (n *failoverNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	nonce := r.Header.Get(DispatchNonceHeader)
	n.mu.Lock()
	n.bodies = append(n.bodies, string(body))
	n.nonces = append(n.nonces, nonce)
	n.mu.Unlock()
	switch n.mode {
	case "refuse":
		markNotDispatched(w, takeDispatchNonce(r), NotDispatchedWakeRefused)
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeScanCritical, "refused by "+n.name, "x"))
	case "forge":
		// A guest that guesses at the marker.
		w.Header().Set(NotDispatchedHeader, NotDispatchedWakeRefused+"; nonce=guess")
		w.WriteHeader(http.StatusServiceUnavailable)
	default:
		_, _ = io.WriteString(w, "served by "+n.name)
	}
}

func (n *failoverNode) requests() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.bodies)
}

// failoverDialer chooses among named nodes in order, like the compute pool.
type failoverDialer struct {
	nodes []*failoverNode
}

func (d *failoverDialer) DialContext(ctx context.Context, _ string) (net.Conn, error) {
	rec := DialRecordFrom(ctx)
	for _, n := range d.nodes {
		if rec.Excluded(n.name) {
			continue
		}
		conn, err := net.Dial("tcp", n.server.Listener.Addr().String())
		if err == nil {
			rec.RecordDialed(n.name)
		}
		return conn, err
	}
	return nil, ErrNoComputeCapacity
}

func (d *failoverDialer) HasAlternate(_ context.Context, rec *DialRecord) bool {
	for _, n := range d.nodes {
		if !rec.Excluded(n.name) {
			return true
		}
	}
	return false
}

type failoverObservation struct{ reason, outcome string }

func TestInternalReverseProxy_WakeFailover(t *testing.T) {
	big := strings.Repeat("b", api.GatewayFailoverMaxBufferedBodyBytes+1)
	cases := []struct {
		name         string
		modes        []string // one node each, dialed in order
		disabled     bool
		method, body string
		budget       *failoverBudget
		remaining    time.Duration // request budget left; 0 = none attached
		wantStatus   int
		wantBody     string
		wantRequests []int
		wantObserved []failoverObservation
	}{
		{name: "the refusing node's request goes to the next node", modes: []string{"refuse", "serve"},
			wantStatus: 200, wantBody: "served by n1", wantRequests: []int{1, 1},
			wantObserved: []failoverObservation{{NotDispatchedWakeRefused, FailoverOutcomeSucceeded}}},
		{name: "a POST body is replayed byte for byte", modes: []string{"refuse", "serve"}, method: http.MethodPost, body: `{"order":42}`,
			wantStatus: 200, wantBody: "served by n1", wantRequests: []int{1, 1},
			wantObserved: []failoverObservation{{NotDispatchedWakeRefused, FailoverOutcomeSucceeded}}},
		{name: "two refusals, then a node serves", modes: []string{"refuse", "refuse", "serve"},
			wantStatus: 200, wantBody: "served by n2", wantRequests: []int{1, 1, 1},
			wantObserved: []failoverObservation{{NotDispatchedWakeRefused, FailoverOutcomeRefusedAgain}, {NotDispatchedWakeRefused, FailoverOutcomeSucceeded}}},
		{name: "at most two extra nodes", modes: []string{"refuse", "refuse", "refuse", "serve"},
			wantStatus: 503, wantRequests: []int{1, 1, 1, 0},
			wantObserved: []failoverObservation{{NotDispatchedWakeRefused, FailoverOutcomeRefusedAgain}, {NotDispatchedWakeRefused, FailoverOutcomeRefusedAgain}, {NotDispatchedWakeRefused, FailoverOutcomeMaxNodes}}},
		{name: "no other node", modes: []string{"refuse"},
			wantStatus: 503, wantRequests: []int{1},
			wantObserved: []failoverObservation{{NotDispatchedWakeRefused, FailoverOutcomeNoAlternate}}},
		{name: "a guest's forged marker is never retried", modes: []string{"forge", "serve"},
			wantStatus: 503, wantRequests: []int{1, 0}},
		{name: "a body the edge did not buffer", modes: []string{"refuse", "serve"}, method: http.MethodPost, body: big,
			wantStatus: 503, wantRequests: []int{1, 0},
			wantObserved: []failoverObservation{{NotDispatchedWakeRefused, FailoverOutcomeBodyNotReplay}}},
		{name: "the retry budget is spent", modes: []string{"refuse", "serve"}, budget: newFailoverBudget(10, 0),
			wantStatus: 503, wantRequests: []int{1, 0},
			wantObserved: []failoverObservation{{NotDispatchedWakeRefused, FailoverOutcomeNoBudget}}},
		{name: "too little of the request's budget is left", modes: []string{"refuse", "serve"}, remaining: time.Second,
			wantStatus: 503, wantRequests: []int{1, 0},
			wantObserved: []failoverObservation{{NotDispatchedWakeRefused, FailoverOutcomeOutOfTime}}},
		{name: "enough of the request's budget is left", modes: []string{"refuse", "serve"}, remaining: 20 * time.Second,
			wantStatus: 200, wantBody: "served by n1", wantRequests: []int{1, 1},
			wantObserved: []failoverObservation{{NotDispatchedWakeRefused, FailoverOutcomeSucceeded}}},
		{name: "failover off: one attempt and no nonce", modes: []string{"refuse", "serve"}, disabled: true,
			wantStatus: 503, wantRequests: []int{1, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dialer := &failoverDialer{}
			for i, mode := range tc.modes {
				n := &failoverNode{name: fmt.Sprintf("n%d", i), mode: mode}
				n.server = httptest.NewServer(n)
				t.Cleanup(n.server.Close)
				dialer.nodes = append(dialer.nodes, n)
			}
			var mu sync.Mutex
			var observed []failoverObservation
			p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "gatewayd-internal"}, slog.Default(), false)
			p.Transport.(*http.Transport).DisableKeepAlives = true
			if !tc.disabled {
				f := NewWakeFailover(func(reason, outcome string) {
					mu.Lock()
					observed = append(observed, failoverObservation{reason, outcome})
					mu.Unlock()
				})
				if tc.budget != nil {
					f.budget = tc.budget
				}
				p.WithWakeFailover(f)
			}
			method := tc.method
			if method == "" {
				method = http.MethodGet
			}
			req := httptest.NewRequest(method, "/orders", strings.NewReader(tc.body))
			req.Host = "shop.apps.gregale.dev"
			// A client cannot pick the nonce.
			req.Header.Set(DispatchNonceHeader, "client-chosen")
			if tc.remaining > 0 {
				ctx, cancel, _ := reqbudget.WithRemaining(req.Context(), tc.remaining, 0, "edge", "")
				t.Cleanup(cancel)
				req = req.WithContext(ctx)
			}
			rr := httptest.NewRecorder()
			p.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus || (tc.wantBody != "" && rr.Body.String() != tc.wantBody) {
				t.Fatalf("response = %d %q, want %d %q", rr.Code, rr.Body.String(), tc.wantStatus, tc.wantBody)
			}
			if got := rr.Header().Get(NotDispatchedHeader); got != "" {
				t.Fatalf("the client saw the marker %q", got)
			}
			for i, n := range dialer.nodes {
				if got := n.requests(); got != tc.wantRequests[i] {
					t.Fatalf("node %s got %d requests, want %d", n.name, got, tc.wantRequests[i])
				}
				for j, body := range n.bodies {
					if body != tc.body {
						t.Fatalf("node %s request %d body = %d bytes, want %d", n.name, j, len(body), len(tc.body))
					}
					nonce := n.nonces[j]
					if nonce == "client-chosen" || (tc.disabled && nonce != "") || (!tc.disabled && nonce == "") {
						t.Fatalf("node %s saw nonce %q (failover off: %v)", n.name, nonce, tc.disabled)
					}
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if fmt.Sprint(observed) != fmt.Sprint(tc.wantObserved) {
				t.Fatalf("observed %v, want %v", observed, tc.wantObserved)
			}
		})
	}
}

// When the retry cannot reach another node, the client gets the refusal the
// edge held, not a transport error.
func TestInternalReverseProxy_WakeFailoverAnswersWithTheHeldRefusal(t *testing.T) {
	refusing := &failoverNode{name: "n0", mode: "refuse"}
	refusing.server = httptest.NewServer(refusing)
	t.Cleanup(refusing.server.Close)
	gone := &failoverNode{name: "n1", mode: "serve"}
	gone.server = httptest.NewServer(gone)
	gone.server.Close()
	dialer := &failoverDialer{nodes: []*failoverNode{refusing, gone}}
	var observed []failoverObservation
	p := NewInternalReverseProxy(dialer, &url.URL{Scheme: "http", Host: "gatewayd-internal"}, slog.Default(), false)
	p.Transport.(*http.Transport).DisableKeepAlives = true
	p.WithWakeFailover(NewWakeFailover(func(reason, outcome string) {
		observed = append(observed, failoverObservation{reason, outcome})
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "shop.apps.gregale.dev"
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "refused by n0") {
		t.Fatalf("response = %d %q, want n0's refusal", rr.Code, rr.Body.String())
	}
	if fmt.Sprint(observed) != fmt.Sprint([]failoverObservation{{NotDispatchedWakeRefused, FailoverOutcomeFailed}}) {
		t.Fatalf("observed %v", observed)
	}
}

// gatewayd-internal marks a platform wake refusal only for an edge that sent
// a nonce, and never for a customer outcome.
func TestHandlerMarksAWakeRefusalNotDispatched(t *testing.T) {
	cases := []struct {
		name    string
		wakeErr error
		nonce   string
		want    string // "" = no marker
	}{
		{name: "scan_critical refusal", wakeErr: api.NewProblem(http.StatusServiceUnavailable, api.CodeScanCritical, "runtime base generation unavailable", "x"),
			nonce: "n1", want: NotDispatchedWakeRefused + "; nonce=n1"},
		{name: "no nonce from the edge", wakeErr: api.NewProblem(http.StatusServiceUnavailable, api.CodeScanCritical, "refused", "x")},
		{name: "the app failed to start", wakeErr: api.NewProblem(http.StatusUnprocessableEntity, api.CodeAppStartupCrash, "crash", "x"), nonce: "n1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, b, _ := newTestHandler(t)
			b.wakeErr = tc.wakeErr
			req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
			if tc.nonce != "" {
				req.Header.Set(DispatchNonceHeader, tc.nonce)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code < 400 {
				t.Fatalf("status = %d, want a refusal", rec.Code)
			}
			if got := rec.Header().Get(NotDispatchedHeader); got != tc.want {
				t.Fatalf("marker = %q, want %q", got, tc.want)
			}
		})
	}
}

// The nonce never reaches a guest, which could otherwise forge the marker.
func TestHandlerDoesNotForwardTheDispatchNonce(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get(DispatchNonceHeader))
		mu.Unlock()
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(guest.Close)
	h, b, _ := newTestHandler(t)
	b.upstream = guest.Listener.Addr().String()
	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	req.Header.Set(DispatchNonceHeader, "n1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	mu.Lock()
	defer mu.Unlock()
	if rec.Code != http.StatusOK || len(seen) != 1 || seen[0] != "" {
		t.Fatalf("status %d, guest saw nonces %q", rec.Code, seen)
	}
}
