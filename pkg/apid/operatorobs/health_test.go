package operatorobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

type healthReply struct {
	value  float64
	empty  bool
	failed bool
}

func healthClient(t *testing.T, now time.Time, override func(string, bool) healthReply) *promql.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, query := range healthQueries() {
			isObservation := r.URL.Query().Get("query") == query.observedAt
			if !isObservation && r.URL.Query().Get("query") != query.value {
				continue
			}
			reply := override(query.name, isObservation)
			if reply.failed {
				http.Error(w, "private upstream error", http.StatusServiceUnavailable)
				return
			}
			result := []any{}
			if !reply.empty {
				// The output timestamp is always fresh, including when the
				// underlying observation is old. Only the value carries evidence.
				result = append(result, map[string]any{"metric": map[string]string{}, "value": []any{now.Unix(), strconv.FormatFloat(reply.value, 'f', -1, 64)}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": result}})
			return
		}
		t.Errorf("unexpected query %q", r.URL.Query().Get("query"))
		http.Error(w, "unknown query", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)
	return promql.NewClient(server.URL, server.Client())
}

func TestPrometheusHealthIndependentSignals(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, state, reason string
		value, observed     healthReply
	}{
		{"zero activity", api.ObsHealthSignalHealthy, "", healthReply{}, healthReply{value: float64(now.Unix())}},
		{"freshness boundary", api.ObsHealthSignalHealthy, "", healthReply{}, healthReply{value: float64(now.Add(-2 * time.Minute).Unix())}},
		{"past freshness boundary", api.ObsHealthSignalStale, "observation_stale", healthReply{}, healthReply{value: float64(now.Add(-2*time.Minute - time.Second).Unix())}},
		{"allowed clock skew", api.ObsHealthSignalHealthy, "", healthReply{}, healthReply{value: float64(now.Add(30 * time.Second).Unix())}},
		{"partial value failure", api.ObsHealthSignalUnavailable, "query_failed", healthReply{failed: true}, healthReply{value: float64(now.Unix())}},
		{"freshness query failure", api.ObsHealthSignalUnavailable, "query_failed", healthReply{value: 7}, healthReply{failed: true}},
		{"missing series", api.ObsHealthSignalNoSamples, "missing_observation", healthReply{empty: true}, healthReply{empty: true}},
		{"missing heartbeat", api.ObsHealthSignalNoSamples, "missing_observation", healthReply{}, healthReply{}},
		{"insufficient range samples", api.ObsHealthSignalNoSamples, "insufficient_samples", healthReply{empty: true}, healthReply{value: float64(now.Unix())}},
		{"stale range", api.ObsHealthSignalStale, "observation_stale", healthReply{value: 7}, healthReply{value: float64(now.Add(-3 * time.Minute).Unix())}},
		{"stale without range", api.ObsHealthSignalStale, "observation_stale", healthReply{empty: true}, healthReply{value: float64(now.Add(-8 * time.Minute).Unix())}},
		{"future observation", api.ObsHealthSignalUnavailable, "observation_in_future", healthReply{value: 7}, healthReply{value: float64(now.Add(time.Minute).Unix())}},
		{"negative observation", api.ObsHealthSignalUnavailable, "invalid_observation", healthReply{value: 7}, healthReply{value: -1}},
		{"negative value", api.ObsHealthSignalUnavailable, "invalid_value", healthReply{value: -1}, healthReply{value: float64(now.Unix())}},
		{"overflowing value", api.ObsHealthSignalUnavailable, "invalid_value", healthReply{value: 1e20}, healthReply{value: float64(now.Unix())}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := healthClient(t, now, func(name string, observation bool) healthReply {
				if name == healthAuditFailures {
					if observation {
						return tc.observed
					}
					return tc.value
				}
				if observation {
					return healthReply{value: float64(now.Add(-15 * time.Second).Unix())}
				}
				return healthReply{value: 17.9}
			})
			got := PrometheusHealth(context.Background(), client, now)
			signal := got.Signals[healthAuditFailures]
			if signal.State != tc.state || signal.Reason != tc.reason || got.Available != (tc.state == api.ObsHealthSignalHealthy) {
				t.Fatalf("snapshot=%+v signal=%+v", got, signal)
			}
			if got.AuditFailures != 0 || got.AuditWrites != 17 || got.AlertsFiring != 17 || len(got.Signals) != 3 {
				t.Fatalf("partial failure erased healthy data or retained fallback: %+v", got)
			}
			if signal.WindowSeconds != 300 || signal.MaxAgeSeconds != 120 || got.Signals[healthAlerts].WindowSeconds != 0 {
				t.Fatalf("missing window/freshness policy: %+v", got.Signals)
			}
			if tc.state == api.ObsHealthSignalStale && (signal.ObservedAt == nil || signal.ObservedAt.Unix() != int64(tc.observed.value)) {
				t.Fatalf("stale signal lost underlying timestamp: %+v", signal)
			}
		})
	}
}

func TestPrometheusHealthRecoveryDoesNotReuseCachedEvidence(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	var phase atomic.Int32
	client := healthClient(t, now, func(_ string, observation bool) healthReply {
		if phase.Load() == 1 {
			return healthReply{failed: true}
		}
		if observation {
			if phase.Load() == 0 {
				return healthReply{value: float64(now.Add(-3 * time.Minute).Unix())}
			}
			return healthReply{value: float64(now.Unix())}
		}
		return healthReply{value: 9}
	})
	for i, state := range []string{api.ObsHealthSignalStale, api.ObsHealthSignalUnavailable, api.ObsHealthSignalHealthy} {
		phase.Store(int32(i))
		got := PrometheusHealth(context.Background(), client, now)
		for name, signal := range got.Signals {
			if signal.State != state || (i == 2 && signal.Reason != "") || (i == 1 && signal.ObservedAt != nil) {
				t.Fatalf("phase=%d signal=%s: %+v", i, name, signal)
			}
		}
		if got.Available != (i == 2) || (i == 2 && got.AuditWrites != 9) || (i < 2 && got.AuditWrites != 0) {
			t.Fatalf("phase=%d snapshot=%+v", i, got)
		}
	}
}

func TestPrometheusHealthNotConfiguredAndSharedDeadline(t *testing.T) {
	for _, client := range []*promql.Client{nil, promql.NewClient("", nil)} {
		got := PrometheusHealth(context.Background(), client, time.Now())
		for _, signal := range got.Signals {
			if got.Available || signal.State != api.ObsHealthSignalUnavailable || signal.Reason != "not_configured" {
				t.Fatalf("unconfigured source: %+v", got)
			}
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	got := PrometheusHealth(ctx, promql.NewClient(server.URL, server.Client()), time.Now())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("parent deadline was not shared: %s", elapsed)
	}
	for _, signal := range got.Signals {
		if got.Available || signal.State != api.ObsHealthSignalUnavailable || signal.Reason != "query_failed" {
			t.Fatalf("timed out source: %+v", got)
		}
	}
}

func TestPrometheusHealthOutageHasOneTotalBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	start := time.Now()
	got := PrometheusHealth(context.Background(), promql.NewClient(server.URL, server.Client()), time.Now())
	if elapsed := time.Since(start); elapsed > api.ObsHealthPrometheusBudget+2*time.Second {
		t.Fatalf("outage multiplied the total query budget: %s", elapsed)
	}
	for _, signal := range got.Signals {
		if got.Available || signal.State != api.ObsHealthSignalUnavailable || signal.Reason != "query_failed" {
			t.Fatalf("outage source: %+v", got)
		}
	}
}
