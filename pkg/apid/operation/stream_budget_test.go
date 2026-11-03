package operation

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// An Operation event stream must outlive apid's request budget: every stream
// used to close when the 5 s default fired (bug hunt 2026-10-28 B35).
func TestCommittedStreamOutlivesTheRequestBudget(t *testing.T) {
	cfg, err := reqbudget.NewMiddlewareConfig(reqbudget.MiddlewareConfig{Default: 50 * time.Millisecond, Max: time.Second, Route: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(cfg.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		r, cancel, ok := commitStream(w, r)
		defer cancel()
		if !ok {
			return
		}
		select {
		case <-time.After(300 * time.Millisecond):
			_ = writeOperationFrame(w, "operation", 1, map[string]int{"sequence": 1})
		case <-r.Context().Done():
		}
	})))
	defer server.Close()
	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if scanner.Text() == "event: operation" {
			return
		}
	}
	t.Fatalf("stream closed before its first event, after the request budget: %v", scanner.Err())
}
