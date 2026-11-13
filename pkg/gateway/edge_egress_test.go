package gateway

// adr: 20261104132753 — a response served from the gateway's cache never
// reaches the instance, so its bytes are metered as the app's edge egress.

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/egresssink"
)

func TestCacheHitBytesCountAsAppEdgeEgress(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	cache := NewResponseCacheWithClock(DefaultResponseCacheMaxBytes, clock)
	h, _, _ := newTestHandler(t)
	h.WithResponseCache(cache)
	sink := egresssink.NewEgressSinkWithClock(clock)
	h.WithEgressSink(sink)
	rule := EdgeRuleCacheResolved{ID: "rule-cache-1", PathGlob: "/catalog", MaxAgeSeconds: 60}
	seedCacheRule(t, h, "jane-api.apps.dom", rule)
	app := App{ID: "app-1", Plan: api.PlanHobby}
	key := CacheKey{AppID: app.ID, RuleID: rule.ID, Method: "GET", NormalizedPath: "/catalog", VaryHash: hostVaryHash("jane-api.apps.dom")}
	body := []byte(`{"items":["a","b"]}`)
	cache.Put(key, 200, http.Header{"Content-Type": []string{"application/json"}}, body,
		now.Add(60*time.Second), now.Add(360*time.Second), rule.toStateEdgeRuleCacheAction())

	for range 3 {
		req := httptest.NewRequest("GET", "http://jane-api.apps.dom/catalog", nil)
		w := httptest.NewRecorder()
		if got, _ := h.applyEdgeRuleCache(w, req, app, newTestStatusRecorder(w)); !got {
			t.Fatal("applyEdgeRuleCache returned false on a fresh hit")
		}
	}
	records := sink.DrainRecords()
	if len(records) != 1 {
		t.Fatalf("records = %+v, want one edge record", records)
	}
	got := records[0]
	if got.AppID != app.ID || got.InstanceID != api.EdgeUsageInstanceID(app.ID) || got.Bytes != uint64(3*len(body)) {
		t.Fatalf("edge record = %+v, want three bodies for app-1 under its edge usage ID", got)
	}

	// A HEAD hit sends no body.
	h.recordEdgeEgress(httptest.NewRequest(http.MethodHead, "http://jane-api.apps.dom/catalog", nil), app, len(body))
	if records := sink.DrainRecords(); len(records) != 0 {
		t.Fatalf("HEAD recorded %+v, want nothing", records)
	}
}
