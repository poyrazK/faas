package outbound

// adr: 20261027132936 — amendment 1: a short lease is renewed before it
// lapses, a sent billable AI call is not sent again, and the public host
// waits at most 90 s for headers.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRenewAILeaseHoldsALeaseShorterThanTheRenewalMinimum(t *testing.T) {
	backend := NewMemoryBackend()
	spec := AdmissionSpec{IntegrationID: "integration-1", RatePerSecond: 100, Burst: 10, MaxInFlight: 1, LeaseTTL: 300 * time.Millisecond}
	ctx := context.Background()
	first, err := backend.Admit(ctx, spec)
	if err != nil || !first.Granted {
		t.Fatalf("first admission = %+v, %v", first, err)
	}
	h := &Handler{Backend: backend}
	stop := h.renewAILease("integration-1", first.LeaseID, 300*time.Millisecond)
	time.Sleep(900 * time.Millisecond)
	second, err := backend.Admit(ctx, spec)
	stop()
	if err != nil || second.Granted {
		t.Fatalf("admission while a long session runs = %+v, %v; want its renewed lease to hold the slot", second, err)
	}
}

func TestBillableAICallIsNotSentAgainAfterTheProviderGotIt(t *testing.T) {
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		switch r.URL.Path {
		case "/reset":
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = conn.Close()
			}
		case "/504":
			w.WriteHeader(http.StatusGatewayTimeout)
		case "/503":
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer upstream.Close()
	h := &Handler{Backend: NewMemoryBackend()}
	for _, tc := range []struct {
		path       string
		billable   bool
		wantHits   int64
		wantStatus int
	}{
		{"/reset", false, 3, 0},
		{"/reset", true, 1, 0},
		{"/504", false, 3, http.StatusGatewayTimeout},
		{"/504", true, 1, http.StatusGatewayTimeout},
		{"/503", true, 3, http.StatusServiceUnavailable},
	} {
		hits.Store(0)
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, upstream.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		setReplayableBody(req, []byte(`{"model":"gpt-5","messages":[]}`))
		resp, attempts, err := h.doWithRetries(context.Background(), upstream.Client(), req, "integration-1", "integration-1", 1, 2, 0, nil, tc.billable)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if hits.Load() != tc.wantHits || int64(attempts) != tc.wantHits {
			t.Errorf("%s billable=%v: provider got %d calls in %d attempts, want %d", tc.path, tc.billable, hits.Load(), attempts, tc.wantHits)
		}
		if tc.wantStatus != 0 && (err != nil || resp == nil || resp.StatusCode != tc.wantStatus) {
			t.Errorf("%s billable=%v: response = %v, %v; want %d", tc.path, tc.billable, resp, err, tc.wantStatus)
		}
	}
}

func TestPublicHeaderTimeout(t *testing.T) {
	for _, tc := range []struct{ in, want time.Duration }{
		{0, 90 * time.Second},
		{30 * time.Second, 30 * time.Second},
		{120 * time.Second, 90 * time.Second},
		{300 * time.Second, 90 * time.Second},
	} {
		if got := publicHeaderTimeout(tc.in); got != tc.want {
			t.Errorf("publicHeaderTimeout(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
