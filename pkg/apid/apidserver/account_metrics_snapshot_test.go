package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/promql"
)

func TestFetchAccountMetricsSnapshotSerializesRawScans(t *testing.T) {
	var activeHeavy atomic.Int32
	var overlapped atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		heavy := strings.Contains(query, "gateway_request_duration_seconds_count") ||
			strings.Contains(query, "gateway_request_duration_seconds_bucket")
		if heavy {
			if activeHeavy.Add(1) != 1 {
				overlapped.Store(true)
			}
			time.Sleep(20 * time.Millisecond)
			activeHeavy.Add(-1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	t.Cleanup(srv.Close)

	client := promql.NewClient(srv.URL, srv.Client())
	snapshot := apidserver.FetchAccountMetricsSnapshot(t.Context(), client, []string{"app-1"}, "24h", false, false)
	if (*snapshot.RequestsErr()) != nil || (*snapshot.LatencyErr()) != nil || (*snapshot.ColdBootsErr()) != nil {
		t.Fatalf("snapshot errors: requests=%v latency=%v cold=%v", (*snapshot.RequestsErr()), (*snapshot.LatencyErr()), (*snapshot.ColdBootsErr()))
	}
	if overlapped.Load() {
		t.Fatal("request-count and latency-bucket scans overlapped")
	}
}
