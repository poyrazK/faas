package gateway

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 091 — amendment 2026-11-04 (review round 12): the health path and a
// parked app's HEAD / are answered past the edge access gates, so a client
// an ip rule refuses gets the refusal, not an edge answer.
func TestGatedEdgeAnswersTakeTheEdgeGates(t *testing.T) {
	_, blocked, _ := net.ParseCIDR("198.51.100.0/24")
	for _, tc := range []struct {
		name, method, path, client string
		want                       int
	}{
		{name: "parked HEAD from a refused client", method: http.MethodHead, path: "/", client: "198.51.100.7", want: http.StatusForbidden},
		{name: "health from a refused client", method: http.MethodGet, path: "/healthz", client: "198.51.100.7", want: http.StatusForbidden},
		{name: "parked HEAD from an admitted client", method: http.MethodHead, path: "/", client: "192.0.2.1", want: http.StatusNoContent},
		{name: "favicon stays early", method: http.MethodGet, path: "/favicon.ico", client: "198.51.100.7", want: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, b, _ := newTestHandler(t)
			b.mu.Lock()
			b.targets, b.running = nil, false
			b.mu.Unlock()
			h.WithEdgeRules(stubEdgeRuleMatcher{ip: &EdgeRuleIPResolved{ID: "rule-ip", AccountID: b.app.AccountID, AppID: b.app.ID, Deny: []*net.IPNet{blocked}}}, nil, nil)
			req := httptest.NewRequest(tc.method, "http://jane-api.apps.dom"+tc.path, nil)
			req.Header.Set("X-Forwarded-For", tc.client)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body)
			}
			if got := atomic.LoadInt32(b.Admits()); got != 0 {
				t.Fatalf("admits = %d, want 0", got)
			}
		})
	}
}

// adr: 091 — amendment 2026-11-04 (review round 12): validate-rule bodies
// hold heap under the client's and the owner's shares, and a body of unknown
// length reserves as it is read instead of the whole cap up front.
func TestValidateBodiesHoldTheirShare(t *testing.T) {
	h := &Handler{bodyMemoryBudget: newRequestBodyMemoryBudget()}
	owner := func(id string) App { return App{ID: id + "-app", AccountID: id} }
	req := func(client string, length int64) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://example.test/", nil)
		r.ContentLength = length
		r.Header.Set("X-Forwarded-For", client)
		return r.WithContext(withTrustedClientIP(r.Context(), net.ParseIP(client), true))
	}
	// One owner holds at most a third of the node budget.
	if _, ok := h.holdValidateBody(req("192.0.2.1", 25<<20), owner("a"), 25<<20); !ok {
		t.Fatal("first 25 MiB body refused")
	}
	if _, ok := h.holdValidateBody(req("192.0.2.2", 25<<20), owner("a"), 25<<20); ok {
		t.Fatal("one owner held two 25 MiB bodies, past its share")
	}
	if _, ok := h.holdValidateBody(req("192.0.2.3", 25<<20), owner("b"), 25<<20); !ok {
		t.Fatal("another owner was refused while the first held its share")
	}
	// A body of unknown length reserves one step, not the cap.
	before := h.bodyMemoryBudget.usedBytes()
	lease, ok := h.holdValidateBody(req("192.0.2.4", -1), owner("c"), 25<<20)
	if !ok {
		t.Fatal("chunked body refused")
	}
	if got := h.bodyMemoryBudget.usedBytes() - before; got != api.RequestBodyMemoryReserveStep {
		t.Fatalf("chunked body reserved %d bytes up front, want %d", got, api.RequestBodyMemoryReserveStep)
	}
	// Reading past the owner's share fails the read.
	body := &budgetedBodyReader{r: strings.NewReader(strings.Repeat("x", 40<<20)), lease: lease}
	if _, err := io.ReadAll(body); !errors.Is(err, errRequestBodyBudget) {
		t.Fatalf("reading 40 MiB past the share: err = %v, want errRequestBodyBudget", err)
	}
	lease.release()
	// One client holds at most RequestBodyMemoryPerClient bodies.
	for i := range api.RequestBodyMemoryPerClient {
		if _, ok := h.holdValidateBody(req("203.0.113.9", 1), owner(fmt.Sprintf("d%d", i)), 25<<20); !ok {
			t.Fatalf("client body %d refused", i)
		}
	}
	if _, ok := h.holdValidateBody(req("203.0.113.9", 1), owner("e"), 25<<20); ok {
		t.Fatal("one client held more bodies than its cap")
	}
}
