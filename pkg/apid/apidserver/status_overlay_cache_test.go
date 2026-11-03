package apidserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type countingStatusEventStore struct {
	Store state.Store
	reads atomic.Int32
}

func (c *countingStatusEventStore) ListPublicStatusEvents(ctx context.Context, opts state.StatusEventListOptions) ([]state.StatusIncident, error) {
	c.reads.Add(1)
	return c.Store.ListPublicStatusEvents(ctx, opts)
}

// slo.json is unauthenticated and read up to 200 operator status events
// from CP Postgres on every request; monitors poll it hardest during an
// incident. It now reads them once per 15 s and lets the CDN cache it for
// as long (bug hunt 2026-10-28 O25).
func TestStatusJSONReusesOperatorEventRead(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "ALERTS") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[1,"100"]}]}}`))
	}))
	t.Cleanup(prom.Close)
	events := &countingStatusEventStore{Store: state.NewMemStore()}
	handler := apidserver.StatusJSONHandler(statetest.NewStore(events), prom.URL)
	serve := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest(http.MethodGet, "/status/slo.json", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
		}
		return rec
	}
	first := serve()
	if got := first.Header().Get("Cache-Control"); got != "public, max-age=15, stale-while-revalidate=45" {
		t.Fatalf("Cache-Control = %q", got)
	}
	afterFirst := events.reads.Load()
	if afterFirst == 0 {
		t.Fatal("slo.json never read the operator status events")
	}
	for range 5 {
		serve()
	}
	if got := events.reads.Load(); got != afterFirst {
		t.Fatalf("6 requests read operator status events %d times, want %d", got, afterFirst)
	}
}
