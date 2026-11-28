package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A public client could POST to /_triggers/{source}/{id} with
// X-Gregale-Batch-Size and the SDKs took it for a broker batch (prod hunt r80,
// 2026-11-28). The path is refused before the app is touched, and the header
// never reaches an app on any other path.
func TestPublicTriggerDeliveryIsRefusedAndBatchHeaderStripped(t *testing.T) {
	h, b, _ := newTestHandler(t)
	var hits atomic.Int32
	var sawBatch atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("X-Gregale-Batch-Size") != "" {
			sawBatch.Store(true)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)
	b.upstream = upstream.Listener.Addr().String()
	b.setLegacyHot()

	for _, path := range []string{"/_triggers/kafka/abc", "/_TRIGGERS/kafka/abc", "//_triggers/kafka/abc"} {
		req := httptest.NewRequest(http.MethodPost, "http://jane-api.apps.dom"+path, strings.NewReader(`{"records":[]}`))
		req.Header.Set("X-Gregale-Batch-Size", "1")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, rec.Code)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("a refused trigger path reached the app %d times", hits.Load())
	}

	req := httptest.NewRequest(http.MethodPost, "http://jane-api.apps.dom/orders", strings.NewReader(`{}`))
	req.Header.Set("X-Gregale-Batch-Size", "3")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /orders = %d, want 200", rec.Code)
	}
	if sawBatch.Load() {
		t.Fatal("a public request's X-Gregale-Batch-Size reached the app")
	}
}
