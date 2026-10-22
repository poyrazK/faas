package operatorobs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func TestAlertSuppressionExactEpisodeAndEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, tc := range []struct{ mode, want string }{
		{"active", "active"}, {"silenced", "suppressed"}, {"inhibited", "suppressed"}, {"both", "suppressed"}, {"unprocessed", "unprocessed"},
		{"millisecond activation", "active"}, {"older millisecond activation", "unmatched"},
		{"external labels", "unmatched"}, {"removed label", "unmatched"}, {"recurrence", "unmatched"}, {"rule change", "unmatched"}, {"ambiguous rules", "unmatched"},
		{"stale rules", "unavailable"}, {"missing alert", "unmatched"}, {"duplicate alert", "unmatched"}, {"stale alert", "stale"}, {"expired alert", "stale"},
		{"old activation", "unmatched"}, {"future update", "unmatched"}, {"inconsistent status", "unavailable"}, {"empty suppressed status", "unavailable"}, {"http failure", "unavailable"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			groups := historyRules(now, true)
			if tc.mode == "stale alert" {
				groups[0].Rules[0].Alerts[0].ActiveAt = now.Add(-10 * time.Minute)
			}
			if tc.mode == "millisecond activation" || tc.mode == "older millisecond activation" {
				groups[0].Rules[0].Alerts[0].ActiveAt = now.Truncate(time.Second).Add(-time.Minute).Add(123456 * time.Microsecond)
			}
			snapshot, err := alertSnapshot(groups, now)
			if err != nil {
				t.Fatal(err)
			}
			episode := snapshot.Episodes[0]
			item := api.ObsIncident{ID: "platform_alert/" + episode.ID, Type: "platform_alert", Status: "firing", AlertEpisode: &api.ObsAlertEpisode{SeriesID: episode.SeriesID, StartedAt: episode.StartedAt}}
			row := notificationTestAlert(now)
			switch tc.mode {
			case "millisecond activation":
				row.StartsAt = episode.StartedAt.Truncate(time.Millisecond)
			case "older millisecond activation":
				row.StartsAt = episode.StartedAt.Truncate(time.Millisecond).Add(-time.Millisecond)
			case "silenced", "both":
				row.Status.State = "suppressed"
				row.Status.SilencedBy = []string{"private-silence-id"}
			case "inhibited":
				row.Status.State = "suppressed"
				row.Status.InhibitedBy = []string{"private-inhibitor-id"}
			case "unprocessed":
				row.Status.State = "unprocessed"
			case "external labels":
				row.Labels["cluster"] = "private-cluster-name"
			case "removed label":
				delete(row.Labels, "component")
			case "stale alert":
				row.StartsAt = now.Add(-9 * time.Minute)
				row.UpdatedAt = now.Add(-3 * time.Minute)
			case "expired alert":
				row.EndsAt = now
			case "old activation":
				row.StartsAt = episode.StartedAt.Add(-time.Second)
			case "future update":
				row.UpdatedAt = now.Add(time.Minute)
			case "inconsistent status":
				row.Status.InhibitedBy = []string{"private-inhibitor-id"}
			case "empty suppressed status":
				row.Status.State = "suppressed"
			case "recurrence":
				groups[0].Rules[0].Alerts[0].ActiveAt = now.Add(-20 * time.Second)
			case "rule change":
				groups[0].Rules[0].Query = "different_query"
			case "stale rules":
				groups[0].Rules[0].LastEvaluation = now.Add(-3 * time.Minute)
			case "ambiguous rules":
				other := groups[0].Rules[0]
				other.Query = "different_query"
				groups[0].Rules = append(groups[0].Rules, other)
			}
			if tc.mode == "both" {
				row.Status.InhibitedBy = []string{"private-inhibitor-id"}
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/rules" {
					_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"groups": groups}})
					return
				}
				if tc.mode == "http failure" {
					http.Error(w, "private-upstream-body", 503)
					return
				}
				rows := []alertmanagerAlert{row}
				if tc.mode == "missing alert" {
					rows = []alertmanagerAlert{}
				}
				if tc.mode == "duplicate alert" {
					rows = append(rows, row)
				}
				_ = json.NewEncoder(w).Encode(rows)
			}))
			defer srv.Close()
			out := AlertSuppression(t.Context(), promql.NewClient(srv.URL, srv.Client()), NewAlertmanagerClient(srv.URL, nil), item, now)
			if out.State != tc.want {
				t.Fatalf("%s: %+v", tc.mode, out)
			}
			if tc.want == "active" || tc.want == "suppressed" {
				if out.Silenced == nil || out.Inhibited == nil || *out.Silenced != (tc.mode == "silenced" || tc.mode == "both") || *out.Inhibited != (tc.mode == "inhibited" || tc.mode == "both") {
					t.Fatalf("suppression evidence: %+v", out)
				}
			} else if out.Silenced != nil || out.Inhibited != nil {
				t.Fatalf("unknown presented as unsuppressed: %+v", out)
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private") || strings.Contains(string(body), srv.URL) {
				t.Fatalf("metadata leak: %s", body)
			}
		})
	}
}

func TestAlertSuppressionHistoricalAndUnverified(t *testing.T) {
	now := time.Now().UTC()
	item := api.ObsIncident{Type: "platform_alert", Status: "resolved", AlertEpisode: &api.ObsAlertEpisode{ResolvedAt: &now}}
	out := AlertSuppression(t.Context(), nil, nil, item, now)
	if out.State != "not_applicable" || out.Silenced != nil {
		t.Fatalf("historical: %+v", out)
	}
	item = api.ObsIncident{Type: "platform_alert", Status: "firing"}
	out = AlertSuppression(t.Context(), nil, NewAlertmanagerClient("http://127.0.0.1:1", nil), item, now)
	if out.State != "unknown" || out.Reason != "episode_unverified" {
		t.Fatalf("legacy live: %+v", out)
	}
	out = AlertSuppression(t.Context(), nil, nil, item, now)
	if out.State != "disabled" {
		t.Fatalf("disabled: %+v", out)
	}
}
