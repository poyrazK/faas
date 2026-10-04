package main

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// Production regression: apid's request budget (5 s) and the listener's
// request-wide WriteTimeout both ended every SSE stream, so `gregale tail`
// exited five seconds after it attached. A stream started with
// startSSEStream must outlive both while it keeps writing.
func TestStartSSEStreamOutlivesRequestBudgetAndWriteTimeout(t *testing.T) {
	budget, err := reqbudget.NewMiddlewareConfig(reqbudget.MiddlewareConfig{
		Default: 50 * time.Millisecond,
		Max:     50 * time.Millisecond,
		Route:   "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	const beats = 8
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w, ctx, cancel := startSSEStream(w, r)
		defer cancel()
		flusher, _ := w.(http.Flusher)
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		for i := 0; i < beats; i++ {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				_, _ = fmt.Fprintf(w, "event: beat\ndata: %d\n\n", i)
				flusher.Flush()
			}
		}
	})
	srv := httptest.NewUnstartedServer(budget.Middleware(handler))
	srv.Config.WriteTimeout = 150 * time.Millisecond
	srv.Start()
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}
	got := 0
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "event: beat") {
			got++
		}
	}
	// 8 beats span ~400 ms: well past the 50 ms budget and the 150 ms
	// request-wide write timeout.
	if got != beats {
		t.Fatalf("received %d of %d heartbeats; the stream was cut early", got, beats)
	}
}
