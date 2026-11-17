package backlog_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/backlog"
)

// hold is a handler that does what step says with the response, then waits
// until it is let go.
type hold struct {
	entered chan struct{}
	release chan struct{}
	step    func(http.ResponseWriter)
}

func newHold(step func(http.ResponseWriter)) *hold {
	return &hold{entered: make(chan struct{}, 16), release: make(chan struct{}), step: step}
}

func (h *hold) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	if h.step != nil {
		h.step(w)
	}
	h.entered <- struct{}{}
	<-h.release
}

func limit(n int64) *atomic.Int64 {
	var v atomic.Int64
	v.Store(n)
	return &v
}

func serve(c http.Handler) (*httptest.ResponseRecorder, chan struct{}) {
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://app.example/", nil))
	}()
	return rec, done
}

func TestCapRefusesPastTheBacklogUntilAHeadIsWritten(t *testing.T) {
	cases := []struct {
		name string
		step func(http.ResponseWriter)
		// frees reports whether the step writes the head, so a held
		// request no longer counts while its handler runs on.
		frees bool
	}{
		{name: "nothing written", step: nil, frees: false},
		{name: "100 Continue", step: func(w http.ResponseWriter) { w.WriteHeader(http.StatusContinue) }, frees: false},
		{name: "103 Early Hints", step: func(w http.ResponseWriter) { w.WriteHeader(http.StatusEarlyHints) }, frees: false},
		{name: "status written", step: func(w http.ResponseWriter) { w.WriteHeader(http.StatusOK) }, frees: true},
		{name: "101 upgrade", step: func(w http.ResponseWriter) { w.WriteHeader(http.StatusSwitchingProtocols) }, frees: true},
		{name: "body written", step: func(w http.ResponseWriter) { _, _ = io.WriteString(w, "data: 1\n\n") }, frees: true},
		{name: "flushed", step: func(w http.ResponseWriter) { _ = http.NewResponseController(w).Flush() }, frees: true},
		{name: "flushed through http.Flusher", step: func(w http.ResponseWriter) { w.(http.Flusher).Flush() }, frees: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHold(tc.step)
			reg := prometheus.NewRegistry()
			c := backlog.New(h, limit(2), reg)
			var done []chan struct{}
			for range 2 {
				_, d := serve(c)
				<-h.entered
				done = append(done, d)
			}
			wantWaiting := int64(2)
			if tc.frees {
				wantWaiting = 0
			}
			if got := c.Waiting(); got != wantWaiting {
				t.Fatalf("waiting = %d with two held requests, want %d", got, wantWaiting)
			}

			third, thirdDone := serve(c)
			if tc.frees {
				<-h.entered
				done = append(done, thirdDone)
			} else {
				<-thirdDone
				assertOverloaded(t, third)
				if got := testutil.ToFloat64(c.Refused()); got != 1 {
					t.Fatalf("refused counter = %v, want 1", got)
				}
			}

			close(h.release)
			for _, d := range done {
				<-d
			}
			if got := c.Waiting(); got != 0 {
				t.Fatalf("waiting = %d after every handler returned, want 0", got)
			}
			if _, err := reg.Gather(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCapAdmitsAgainOnceAWaitingRequestEnds(t *testing.T) {
	h := newHold(nil)
	c := backlog.New(h, limit(1), nil)
	_, first := serve(c)
	<-h.entered
	refused, refusedDone := serve(c)
	<-refusedDone
	assertOverloaded(t, refused)

	close(h.release)
	<-first
	rec, done := serve(c)
	<-h.entered
	<-done
	if rec.Code != http.StatusOK {
		t.Fatalf("a request after the backlog drained got %d, want 200", rec.Code)
	}
}

func TestCapPassesUpgradesThrough(t *testing.T) {
	var c *backlog.Cap
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.ServeHTTP(w, r)
	}))
	defer srv.Close()
	var waitingAfterHijack atomic.Int64
	waitingAfterHijack.Store(-1)
	c = backlog.New(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("the capped writer does not implement http.Hijacker")
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer conn.Close()
		waitingAfterHijack.Store(c.Waiting())
		_, _ = buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n")
		_ = buf.Flush()
	}), limit(1), nil)

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status %d, want 101", resp.StatusCode)
	}
	if got := waitingAfterHijack.Load(); got != 0 {
		t.Fatalf("waiting = %d after the hijack, want 0", got)
	}
}

func assertOverloaded(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503", rec.Code)
	}
	retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || retry < api.GatewayOverloadedRetryAfterMin || retry > api.GatewayOverloadedRetryAfterMax {
		t.Fatalf("Retry-After %q, want %d..%d", rec.Header().Get("Retry-After"), api.GatewayOverloadedRetryAfterMin, api.GatewayOverloadedRetryAfterMax)
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.Code != api.CodeGatewayOverloaded {
		t.Fatalf("body %q, want problem code %q", rec.Body.String(), api.CodeGatewayOverloaded)
	}
}

func TestCapFollowsALimitChangedWhileServing(t *testing.T) {
	h := newHold(nil)
	max := limit(1)
	c := backlog.New(h, max, nil)
	_, first := serve(c)
	<-h.entered
	refused, refusedDone := serve(c)
	<-refusedDone
	assertOverloaded(t, refused)

	max.Store(2)
	_, second := serve(c)
	<-h.entered
	if got := c.Waiting(); got != 2 {
		t.Fatalf("waiting = %d after raising the limit to 2, want 2", got)
	}
	close(h.release)
	<-first
	<-second
}

func TestCapWithoutALimitHoldsTheDefault(t *testing.T) {
	reg := prometheus.NewRegistry()
	backlog.New(http.NotFoundHandler(), nil, reg)
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == "gateway_awaiting_response_max" {
			if got := f.GetMetric()[0].GetGauge().GetValue(); got != api.GatewayAwaitingResponseMax {
				t.Fatalf("gateway_awaiting_response_max = %v, want %d", got, api.GatewayAwaitingResponseMax)
			}
			return
		}
	}
	t.Fatal("gateway_awaiting_response_max is not registered")
}
