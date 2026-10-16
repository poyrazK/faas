package operatorobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func TestNotificationHealthEvidenceAndFailures(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fixture, err := os.ReadFile("../../promqlrules/testdata/operator_notification_health.test.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ mode, want string }{
		{"quiet", "healthy"}, {"failed", "degraded"}, {"dropped", "degraded"}, {"queue", "degraded"}, {"reload", "degraded"}, {"down", "degraded"},
		{"stale", "partial"}, {"missing", "partial"}, {"nan", "partial"}, {"warning", "partial"}, {"unknown integration", "partial"}, {"api error", "partial"}, {"counter mismatch", "partial"}, {"partial failures", "degraded"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v2/alerts" {
					if tc.mode == "api error" {
						http.Error(w, "private provider payload", 503)
						return
					}
					_ = json.NewEncoder(w).Encode([]alertmanagerAlert{})
					return
				}
				query := r.URL.Query().Get("query")
				if tc.mode == "quiet" && strings.Contains(query, "increase(") && !strings.Contains(string(fixture), query) {
					t.Errorf("production counter query lacks semantic fixture: %s", query)
				}
				observation := strings.Contains(query, "timestamp(")
				value := 0.0
				labels := map[string]string{}
				if strings.Contains(query, " by (integration)") {
					labels["integration"] = "slack"
				}
				if strings.Contains(query, "up{") || strings.Contains(query, "reload_successful") {
					value = 1
				}
				if strings.Contains(query, "queue_capacity") {
					value = 1000
				}
				if tc.mode == "queue" && strings.Contains(query, "queue_length") {
					value = 900
				}
				if tc.mode == "down" && strings.Contains(query, "up{") {
					value = 0
				}
				if tc.mode == "reload" && strings.Contains(query, "reload_successful") {
					value = 0
				}
				if tc.mode == "dropped" && strings.Contains(query, "dropped_total") {
					value = 1.5
				}
				if (tc.mode == "failed" || tc.mode == "partial failures") && strings.Contains(query, "notifications_failed_total") {
					value = 2.5
				}
				if observation {
					value = float64(now.Unix())
				}
				if tc.mode == "stale" && observation && strings.Contains(query, "alertmanager_notifications") {
					value = float64(now.Add(-3 * time.Minute).Unix())
				}
				if tc.mode == "unknown integration" && len(labels) > 0 {
					labels["integration"] = "private_receiver_identity"
				}
				rows := []any{map[string]any{"metric": labels, "value": []any{now.Unix(), strconv.FormatFloat(value, 'f', -1, 64)}}}
				if tc.mode == "missing" || tc.mode == "counter mismatch" && strings.Contains(query, "notifications_failed_total") {
					rows = []any{}
				}
				if tc.mode == "partial failures" && strings.Contains(query, "notifications_total{") {
					rows = append(rows, map[string]any{"metric": map[string]string{"integration": "email"}, "value": []any{now.Unix(), "-1"}})
				}
				if tc.mode == "nan" && strings.Contains(query, "notifications_failed_total") {
					rows = []any{map[string]any{"metric": labels, "value": []any{now.Unix(), "NaN"}}}
				}
				payload := map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": rows}}
				if tc.mode == "warning" {
					payload["warnings"] = []string{"private upstream warning"}
				}
				_ = json.NewEncoder(w).Encode(payload)
			}))
			defer srv.Close()
			out := NotificationHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), NewAlertmanagerClient(srv.URL, nil), now)
			if out.State != tc.want || out.Integrations == nil || len(out.Signals) != 7 {
				t.Fatalf("health: %+v", out)
			}
			if tc.mode == "quiet" {
				if len(out.Integrations) != 1 || out.Integrations[0].Attempts.Value == nil || *out.Integrations[0].Attempts.Value != 0 {
					t.Fatalf("quiet counter: %+v", out)
				}
			}
			if tc.mode == "stale" && out.Integrations[0].Failures.Value != nil {
				t.Fatal("stale counter exposed a usable zero")
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private") || strings.Contains(string(body), srv.URL) {
				t.Fatalf("source metadata leaked: %s", body)
			}
		})
	}
	out := NotificationHealth(t.Context(), nil, nil, now)
	if out.State != "partial" || out.AlertmanagerAPI.State != "disabled" || out.Signals["alertmanager_failures"].Value != nil {
		t.Fatalf("disabled: %+v", out)
	}
}

func TestNotificationHealthSharedDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	out := NotificationHealth(ctx, promql.NewClient(srv.URL, srv.Client()), NewAlertmanagerClient(srv.URL, nil), start)
	if time.Since(start) > time.Second || out.State != "partial" {
		t.Fatalf("shared deadline: %+v", out)
	}
}

func TestNotificationHealthSlowAlertmanagerPreservesMetrics(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/alerts" {
			<-r.Context().Done()
			return
		}
		value := "2"
		if strings.Contains(r.URL.Query().Get("query"), "timestamp(") {
			value = strconv.FormatInt(now.Unix(), 10)
		}
		labels := map[string]string{}
		if strings.Contains(r.URL.Query().Get("query"), "by (integration)") {
			labels["integration"] = "slack"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": labels, "value": []any{now.Unix(), value}}}}})
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	out := NotificationHealth(ctx, promql.NewClient(srv.URL, srv.Client()), NewAlertmanagerClient(srv.URL, nil), now)
	if out.State != "degraded" || out.AlertmanagerAPI.State != "unavailable" || out.Signals["alertmanager_failures"].Value == nil {
		t.Fatalf("slow source erased independent evidence: %+v", out)
	}
}

func TestNotificationSignalClockAndValueValidation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		value, observed float64
		state           string
	}{
		{0, float64(now.Unix()), api.ObsHealthSignalHealthy},
		{-1, float64(now.Unix()), api.ObsHealthSignalUnavailable},
		{0, float64(now.Add(time.Minute).Unix()), api.ObsHealthSignalUnavailable},
		{0, float64(now.Add(-3 * time.Minute).Unix()), api.ObsHealthSignalStale},
	} {
		out := notificationSignal([]promql.VectorSample{{Value: tc.value}}, nil, []promql.VectorSample{{Value: tc.observed}}, nil, api.ObsNotificationWindow, now)
		if out.State != tc.state || tc.state != api.ObsHealthSignalHealthy && out.Value != nil {
			t.Fatalf("clock/value: %+v", out)
		}
	}
}
