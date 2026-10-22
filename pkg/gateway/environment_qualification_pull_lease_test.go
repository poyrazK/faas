// adr: 20261022155812
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type pullLeaseStub struct {
	qualificationServiceStub
	mu       sync.Mutex
	armed    bool
	callers  []state.EnvironmentQualificationPullCaller
	settled  []string
	leaseErr error
}

func (s *pullLeaseStub) ArmEnvironmentQualificationPullSmoke(context.Context, state.EnvironmentQualificationPullSmoke) error {
	return nil
}

func (s *pullLeaseStub) EnvironmentQualificationPullSmoke(context.Context, string, int64, string, string) (state.EnvironmentQualificationPullSmoke, error) {
	return state.EnvironmentQualificationPullSmoke{}, state.ErrNotFound
}

func (s *pullLeaseStub) LeaseEnvironmentQualificationPullSmoke(_ context.Context, caller state.EnvironmentQualificationPullCaller, visibility time.Duration) ([]state.EnvironmentQualificationPullLease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callers = append(s.callers, caller)
	if s.leaseErr != nil {
		return nil, s.leaseErr
	}
	if !s.armed {
		return nil, nil
	}
	s.armed = false
	return []state.EnvironmentQualificationPullLease{{MessageID: "m-1", Receipt: "r-1", Payload: json.RawMessage(`{"id":"smoke"}`),
		Attempt: 1, ArmedAt: time.Unix(0, 0), LeaseExpiresAt: time.Now().Add(visibility)}}, nil
}

func (s *pullLeaseStub) SettleEnvironmentQualificationPullSmoke(_ context.Context, caller state.EnvironmentQualificationPullCaller, verb string, receipts []string, _ time.Duration) (state.QueueLeaseOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callers = append(s.callers, caller)
	s.settled = append(s.settled, verb+":"+strings.Join(receipts, ","))
	return state.QueueLeaseOutcome{Succeeded: receipts}, nil
}

func pullLeaseProxy(t *testing.T, s *pullLeaseStub, node string) http.Handler {
	t.Helper()
	return NewEnvironmentQualificationServiceProxy(EnvironmentQualificationServiceProxyConfig{Store: s,
		ResolveNode: func(context.Context, string) (string, error) { return node, nil },
		Next:        http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("lease request reached the ordinary proxy") }),
		Forward:     func(Target) http.Handler { t.Fatal("lease request was forwarded to a VM"); return nil }})
}

func pullLeaseRequest(path, body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, api.EnvironmentQualificationQueuePrefix+path, strings.NewReader(body))
	r.RemoteAddr = "10.100.0.3:4567"
	return r
}

func TestEnvironmentQualificationPullLeaseServesOnlyTheObservedCandidate(t *testing.T) {
	s := &pullLeaseStub{qualificationServiceStub: qualificationServiceStub{held: true}}
	node := uuid.NewString()
	proxy := pullLeaseProxy(t, s, node)

	// A long-poll returns as soon as the armed message is handed out.
	go func() {
		time.Sleep(3 * api.EnvironmentGitOpsQualificationPullSmokePoll / 2)
		s.mu.Lock()
		s.armed = true
		s.mu.Unlock()
	}()
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, pullLeaseRequest("/v1/apps/shop-worker/queues/orders/leases", `{"max_messages":1,"wait_seconds":5}`))
	var leased api.QueueLeaseResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &leased) != nil || len(leased.Messages) != 1 ||
		leased.Messages[0].Receipt != "r-1" || string(leased.Messages[0].Payload) != `{"id":"smoke"}` {
		t.Fatalf("lease = %d %s", w.Code, w.Body.String())
	}
	if got := s.callers[len(s.callers)-1]; got != (state.EnvironmentQualificationPullCaller{NodeID: node, HostIP: "10.100.0.3", App: "shop-worker", Queue: "orders"}) {
		t.Fatalf("caller = %+v", got)
	}

	w = httptest.NewRecorder()
	proxy.ServeHTTP(w, pullLeaseRequest("/v1/apps/shop-worker/queues/orders/leases:ack", `{"receipts":["r-1"]}`))
	var settled api.QueueLeaseReceiptsResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &settled) != nil || len(settled.Succeeded) != 1 || s.settled[0] != "ack:r-1" {
		t.Fatalf("ack = %d %s %v", w.Code, w.Body.String(), s.settled)
	}

	// An empty wait answers at once with no messages.
	w = httptest.NewRecorder()
	proxy.ServeHTTP(w, pullLeaseRequest("/v1/apps/shop-worker/queues/orders/leases", `{}`))
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"messages":[]}` {
		t.Fatalf("empty lease = %d %s", w.Code, w.Body.String())
	}
}

func TestEnvironmentQualificationPullLeaseRejectsForeignShapes(t *testing.T) {
	cases := []struct {
		name   string
		held   bool
		method string
		path   string
		body   string
		err    error
		status int
	}{
		{"not a candidate", false, http.MethodPost, "/v1/apps/shop-worker/queues/orders/leases", `{}`, nil, http.StatusForbidden},
		{"GET", true, http.MethodGet, "/v1/apps/shop-worker/queues/orders/leases", ``, nil, http.StatusMethodNotAllowed},
		{"unknown verb", true, http.MethodPost, "/v1/apps/shop-worker/queues/orders/leases:purge", `{}`, nil, http.StatusForbidden},
		{"escaped path", true, http.MethodPost, "/v1/apps/shop%2Fworker/queues/orders/leases", `{}`, nil, http.StatusForbidden},
		{"other route", true, http.MethodPost, "/v1/apps/shop-worker/queues/orders/messages", `{}`, nil, http.StatusForbidden},
		{"long wait", true, http.MethodPost, "/v1/apps/shop-worker/queues/orders/leases", `{"wait_seconds":21}`, nil, http.StatusBadRequest},
		{"unknown field", true, http.MethodPost, "/v1/apps/shop-worker/queues/orders/leases", `{"queue":"payments"}`, nil, http.StatusBadRequest},
		{"no pull binding", true, http.MethodPost, "/v1/apps/shop-worker/queues/orders/leases", `{}`, state.ErrNotFound, http.StatusNotFound},
		{"stale execution", true, http.MethodPost, "/v1/apps/shop-worker/queues/orders/leases", `{}`, state.ErrConflict, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &pullLeaseStub{qualificationServiceStub: qualificationServiceStub{held: tc.held}, leaseErr: tc.err}
			r := pullLeaseRequest(tc.path, tc.body)
			r.Method = tc.method
			w := httptest.NewRecorder()
			pullLeaseProxy(t, s, uuid.NewString()).ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
		})
	}
}
