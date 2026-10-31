package apidserver_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Any string with the inbound token prefix cost a control-plane lookup, at
// whatever rate the client sent (bug hunt 2026-10-27 H15). Unknown tokens
// now count against a per-IP bucket.
func TestInboundWebhookProbesAreLimitedPerIP(t *testing.T) {
	h := apidserver.NewServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).Handler()
	probe := func(i int) int {
		req := httptest.NewRequest(http.MethodPost, "/v1/hooks/"+api.InboundWebhookTokenPrefix+strings.Repeat("x", i+1), strings.NewReader("{}"))
		req.RemoteAddr = "203.0.113.50:1234"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := range api.InboundWebhookFailuresPerIPPerMinute {
		if code := probe(i); code != http.StatusNotFound {
			t.Fatalf("probe %d: %d, want 404", i, code)
		}
	}
	if code := probe(1000); code != http.StatusTooManyRequests {
		t.Fatalf("probe past the bucket: %d, want 429", code)
	}
}
