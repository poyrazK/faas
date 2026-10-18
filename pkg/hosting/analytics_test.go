package hosting

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func analyticsFixture(t *testing.T) (*Analytics, string, string, time.Time, *atomic.Int32) {
	t.Helper()
	site, origin := uuid.NewString(), strings.Repeat("a", 64)
	now := time.Date(2026, 10, 16, 12, 34, 0, 0, time.UTC)
	calls := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		query, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer TEST-SECRET" || !strings.Contains(string(query), "FROM gregale_internal_test_traffic") || !strings.Contains(string(query), "index1 = '"+site+"'") || !strings.Contains(string(query), "blob1 = '"+origin+"'") || !strings.Contains(string(query), "_sample_interval * double2") {
			t.Error("query lost its bounded provider/account/site contract")
		}
		_, _ = io.WriteString(w, `{"data":[{"bucket":"2026-10-16 12:00:00","code":"ok","cache":"hit","status":200,"requests":"10","payload_bytes":1000,"sample_interval":"5"},{"bucket":"2026-10-16 11:00:00","code":"hosting_artifact_unavailable","cache":"none","status":503,"requests":3,"payload_bytes":"0","sample_interval":1}]}`)
	}))
	t.Cleanup(server.Close)
	provider, err := NewAnalytics(strings.Repeat("b", 32), "gregale_internal_test_traffic", "TEST-SECRET")
	if err != nil {
		t.Fatal(err)
	}
	provider.endpoint = server.URL
	provider.client = server.Client()
	return provider, site, origin, now, calls
}
func TestAnalyticsWeightedObservationsCacheAndIsolation(t *testing.T) {
	reader, site, origin, now, calls := analyticsFixture(t)
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			report, err := reader.Read(t.Context(), site, origin, now)
			if err != nil || report.Requests != 13 || report.PayloadBytes != 1000 || report.CacheHits != 10 || report.ServerErrors != 3 || report.MaxSampleInterval != 5 {
				t.Errorf("observations: %+v %v", report, err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("duplicate provider queries: %d", calls.Load())
	}
	report, err := reader.Read(t.Context(), site, origin, now.Add(30*time.Second))
	if err != nil || !report.Approximate || len(report.Buckets) != 25 || len(report.Failures) != 1 || report.Failures[0].Code != "hosting_artifact_unavailable" || !report.ObservedAt.Equal(now) {
		t.Fatalf("report: %+v %v", report, err)
	}
	report.Buckets[0].Requests = 999
	report.Failures[0].Code = "mutated"
	cached, _ := reader.Read(t.Context(), site, origin, now)
	if cached.Buckets[0].Requests == 999 || cached.Failures[0].Code == "mutated" {
		t.Fatal("cached slices are mutable")
	}
	if _, err := reader.Read(t.Context(), site, origin, now.Add(api.HostingTrafficCacheTTL+time.Second)); err != nil || calls.Load() != 2 {
		t.Fatal("cache did not expire", err)
	}
	if _, err := reader.Read(t.Context(), "bad' OR 1=1", origin, now); !errors.Is(err, ErrTrafficUnavailable) {
		t.Fatal("unsafe site accepted")
	}
	if _, err := reader.Read(t.Context(), site, "bad' OR 1=1", now); !errors.Is(err, ErrTrafficUnavailable) {
		t.Fatal("unsafe origin accepted")
	}
	if _, err := NewAnalytics(strings.Repeat("a", 32), "gregale_internal_x; DROP TABLE x", "secret"); err == nil {
		t.Fatal("SQL dataset injection accepted")
	}
	// Origin changes cannot reuse an existing report; a saturated reader fails without an unbounded queue.
	for range api.HostingTrafficQueryConcurrency {
		reader.slots <- struct{}{}
	}
	if _, err := reader.Read(t.Context(), site, strings.Repeat("c", 64), now); !errors.Is(err, ErrTrafficUnavailable) {
		t.Fatal("origin reused cache or bypassed query bound")
	}
	for range api.HostingTrafficQueryConcurrency {
		<-reader.slots
	}
	for i := 0; i < api.HostingTrafficCacheEntries+2; i++ {
		reader.remember(uuid.NewString(), report, now)
	}
	if len(reader.cache) > api.HostingTrafficCacheEntries {
		t.Fatal("unbounded report cache")
	}
}

func TestAnalyticsProviderFailuresNeverBecomeZeroTraffic(t *testing.T) {
	for _, response := range []string{`{}`, `{"data":null}`, `{"data":[{"bucket":"secret"}]}`, `{"data":[{"bucket":"2026-10-16 12:00:00","code":"secret","cache":"none","status":0,"requests":1,"payload_bytes":0,"sample_interval":1}]}`, strings.Repeat("x", api.HostingTrafficResponseMaxBytes+1)} {
		t.Run("invalid_response", func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, response) }))
			defer server.Close()
			reader, _ := NewAnalytics(strings.Repeat("a", 32), "gregale_internal_fixture", "SECRET")
			reader.endpoint = server.URL
			reader.client = server.Client()
			_, err := reader.Read(t.Context(), uuid.NewString(), strings.Repeat("b", 64), time.Date(2026, 10, 16, 12, 34, 0, 0, time.UTC))
			if !errors.Is(err, ErrTrafficUnavailable) || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("unsafe provider failure: %v", err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/credential-leak", http.StatusFound)
	}))
	defer server.Close()
	reader, _ := NewAnalytics(strings.Repeat("a", 32), "gregale_internal_fixture", "SECRET")
	reader.endpoint = server.URL
	if _, err := reader.Read(t.Context(), uuid.NewString(), strings.Repeat("b", 64), time.Now()); !errors.Is(err, ErrTrafficUnavailable) {
		t.Fatal("provider redirect accepted")
	}
}

func TestAnalyticsEmptyDataAndNumericBounds(t *testing.T) {
	report := api.HostingTraffic{PeriodStart: time.Date(2026, 10, 15, 12, 34, 0, 0, time.UTC), PeriodEnd: time.Date(2026, 10, 16, 12, 34, 0, 0, time.UTC)}
	if err := aggregateTraffic(&report, nil); err != nil || report.Requests != 0 || len(report.Buckets) != 25 {
		t.Fatalf("empty observations: %v", err)
	}
	for _, raw := range []string{`-1`, `1.5`, `"NaN"`, `"Infinity"`, `9007199254740992`, `null`} {
		if _, err := trafficInteger(json.RawMessage(raw)); err == nil {
			t.Fatalf("unsafe numeric observation %s", raw)
		}
	}
	for _, raw := range []string{`0`, `"1"`, `10.0`, `"9007199254740991"`} {
		if _, err := trafficInteger(json.RawMessage(raw)); err != nil {
			t.Fatalf("valid numeric observation %s: %v", raw, err)
		}
	}
	counters := api.HostingTrafficCounters{Requests: api.HostingTrafficCounterMax}
	if err := addTraffic(&counters, 1, 0, 200, "hit"); err == nil {
		t.Fatal("counter overflow accepted")
	}
}
