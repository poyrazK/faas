package operatorobs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/promql"
)

type nodeNotificationFixture struct {
	source                 servingFixture
	foreign, blockChannels bool
	calls, scoped          atomic.Int64
}

func (f *nodeNotificationFixture) server(t *testing.T, node string, now time.Time) *httptest.Server {
	t.Helper()
	all, scoped := servingHealthQueries(), servingNotificationQueriesForNode(node)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if r.Method != "GET" || r.URL.Path != "/api/v1/query" || r.URL.Query().Get("time") != now.UTC().Format(time.RFC3339Nano) {
			t.Error("node lookup did not use one pinned read snapshot")
		}
		query, index, selected := r.URL.Query().Get("query"), -1, false
		for i, q := range all {
			if query == q {
				index = i
			}
		}
		for i, q := range scoped {
			if query == q {
				index, selected = i+6, true
			}
		}
		if index < 0 {
			t.Error("unexpected node query")
			http.Error(w, "private error", 400)
			return
		}
		if selected {
			f.scoped.Add(1)
		}
		if f.source.block || selected && f.blockChannels {
			<-r.Context().Done()
			return
		}
		if f.source.fail[index] {
			http.Error(w, "private-provider-error", 503)
			return
		}
		rows := []any{}
		for _, row := range f.source.rows[index] {
			if selected && !f.foreign && row.Labels["probe_node"] != node {
				continue
			}
			rows = append(rows, map[string]any{"metric": row.Labels, "value": []any{now.Unix(), strconv.FormatFloat(row.Value, 'f', -1, 64)}})
		}
		body := map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": rows}}
		if f.source.warning[index] {
			body["warnings"] = []string{"private-provider-warning"}
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNodeProbeNotificationSelection(t *testing.T) {
	for _, tc := range []struct {
		node, query  string
		force, valid bool
	}{
		{"fsn-2", "", false, true}, {"node_2", "", false, true}, {strings.Repeat("n", 128), "", false, true},
		{"", "", false, false}, {"a..b", "", false, false}, {"../x", "", false, false},
		{"https://private/token", "", false, false}, {"node\"}", "", false, false}, {strings.Repeat("n", 129), "", false, false},
		{"fsn-2", "url=https://private/token", false, false}, {"fsn-2", "node=fsn-3", false, false}, {"fsn-2", "", true, false},
	} {
		if _, err := NodeProbeNotificationsSelection(tc.node, tc.query, tc.force); (err == nil) != tc.valid {
			t.Fatal("node selection accepted unsafe or rejected valid input")
		}
	}
}

func TestNodeProbeNotificationEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, tc := range []struct {
		name, node, state string
		expected          *bool
		mutate            func(*nodeNotificationFixture)
	}{
		{"direct result", "fsn-2", "available", boolPtrNode(true), func(*nodeNotificationFixture) {}},
		{"node outside intent", "fsn-9", "not_applicable", boolPtrNode(false), func(*nodeNotificationFixture) {}},
		{"stale recording", "fsn-2", "unknown", boolPtrNode(true), func(f *nodeNotificationFixture) {
			f.source.change(7, notificationRecording+"state", "fsn-2", func(row *promql.VectorSample) { row.Value = float64(now.Add(-121 * time.Second).Unix()) })
		}},
		{"failed metadata query", "fsn-2", "partial", boolPtrNode(true), func(f *nodeNotificationFixture) { f.source.fail[8] = true }},
		{"partial source warning", "fsn-2", "partial", boolPtrNode(true), func(f *nodeNotificationFixture) { f.source.warning[11] = true }},
		{"foreign or oversized response", "fsn-2", "unknown", boolPtrNode(true), func(f *nodeNotificationFixture) { f.foreign = true }},
		{"duplicate metadata", "fsn-2", "partial", boolPtrNode(true), func(f *nodeNotificationFixture) { f.source.rows[8] = append(f.source.rows[8], f.source.rows[8][0]) }},
		{"unreviewed source label", "fsn-2", "partial", boolPtrNode(true), func(f *nodeNotificationFixture) { f.source.rows[8][0].Labels["url"] = "https://private/token" }},
		{"inventory unavailable", "fsn-2", "unknown", nil, func(f *nodeNotificationFixture) { f.source.rows[0], f.source.rows[1] = nil, nil }},
		{"inventory query warning", "fsn-2", "unknown", nil, func(f *nodeNotificationFixture) { f.source.warning[0] = true }},
		{"inventory unconfigured", "fsn-2", "unknown", nil, func(f *nodeNotificationFixture) {
			f.source.rows[0], f.source.rows[1] = nil, nil
			for _, field := range []string{probeConfigured, probePairs, "up"} {
				value := 0.0
				if field == "up" {
					value = 1
				}
				f.source.rows[0] = append(f.source.rows[0], servingFixtureRow(field, "", "", value, false))
				f.source.rows[1] = append(f.source.rows[1], servingFixtureRow(field, "", "", float64(now.Add(-10*time.Second).Unix()), false))
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &nodeNotificationFixture{source: notificationServingFixture(now)}
			tc.mutate(f)
			srv := f.server(t, tc.node, now)
			selection, _ := NodeProbeNotificationsSelection(tc.node, "", false)
			out := NodeProbeNotifications(t.Context(), promql.NewClient(srv.URL, srv.Client()), selection, now)
			if !NodeProbeNotificationsValid(out) || out.Node != tc.node || out.State != tc.state || (out.Expected == nil) != (tc.expected == nil) || out.Expected != nil && *out.Expected != *tc.expected {
				t.Fatalf("node evidence: %+v", out)
			}
			if tc.expected != nil && *tc.expected {
				if len(out.Channels) != 2 || f.scoped.Load() != 8 {
					t.Fatal("expected node did not receive both channels through filtered reads")
				}
			} else if len(out.Channels) != 0 || f.scoped.Load() != 0 {
				t.Fatal("unproved node intent triggered channel reads or fabricated outcomes")
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private") || strings.Contains(string(body), "https://") {
				t.Fatalf("source metadata leaked: %s", body)
			}
		})
	}
}

func boolPtrNode(value bool) *bool { return &value }

func TestNodeProbeNotificationLookupOutsideFleetPreview(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	f := &nodeNotificationFixture{source: notificationServingFixture(now)}
	const count = 40
	f.source.change(0, probePairs, "", func(row *promql.VectorSample) { row.Value = count })
	for i := 2; i < count; i++ {
		node := fmt.Sprintf("node-%04d", i)
		for _, spec := range []struct {
			metric, url string
			value       float64
			index       int
		}{
			{probeExpected, servingFixtureURL, 1, 0}, {probeBudget, "", 2, 0}, {probeAge, "", 900, 0},
			{probeState, servingFixtureURL, 0, 2}, {probeEvalBudget, "", 2, 2}, {probeEvalAge, "", 900, 2}, {probeRun, "", float64(now.Add(-time.Minute).Unix()), 2},
		} {
			f.source.rows[spec.index] = append(f.source.rows[spec.index], servingFixtureRow(spec.metric, node, spec.url, spec.value, spec.index == 2))
			f.source.rows[spec.index+1] = append(f.source.rows[spec.index+1], servingFixtureRow(spec.metric, node, spec.url, float64(now.Add(-10*time.Second).Unix()), spec.index == 2))
		}
		f.source.notification(6, probeRun, node, "", float64(now.Add(-time.Minute).Unix()), now)
		for _, channel := range ServingProbeNotificationChannels() {
			f.source.notification(6, notificationRecording+"state", node, channel, 3, now)
			f.source.notification(8, notificationRecording+"last_attempt_success", node, channel, 0, now)
			f.source.notification(8, notificationRecording+"last_attempt_failure_code", node, channel, 4, now)
			f.source.notification(10, notificationRecording+"last_attempt_timestamp_seconds", node, channel, float64(now.Add(-time.Hour).Unix()), now)
		}
	}
	node := "node-0039"
	srv := f.server(t, node, now)
	client := promql.NewClient(srv.URL, srv.Client())
	fleet := ServingHealth(t.Context(), client, now)
	if !fleet.ProbeNotifications.Truncated {
		t.Fatal("fixture did not truncate the fleet preview")
	}
	for _, item := range fleet.ProbeNotifications.Channels {
		if item.Node == node {
			t.Fatal("requested node was already in the preview")
		}
	}
	selection, _ := NodeProbeNotificationsSelection(node, "", false)
	out := NodeProbeNotifications(t.Context(), client, selection, now)
	if !NodeProbeNotificationsValid(out) || out.State != "available" || len(out.Channels) != 2 {
		t.Fatalf("omitted node lost its independent lookup: %+v", out)
	}
	for _, item := range out.Channels {
		if item.Node != node || item.State != "last_submission_failed" || item.FailureCode != "timeout" {
			t.Fatal("node lookup inherited a different preview entry")
		}
	}
}

func TestNodeProbeNotificationSharedDeadline(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, channels := range []bool{false, true} {
		f := &nodeNotificationFixture{source: notificationServingFixture(now), blockChannels: channels}
		f.source.block = !channels
		srv := f.server(t, "fsn-2", now)
		ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
		selection, _ := NodeProbeNotificationsSelection("fsn-2", "", false)
		start := time.Now()
		out := NodeProbeNotifications(ctx, promql.NewClient(srv.URL, srv.Client()), selection, now)
		cancel()
		if time.Since(start) > time.Second || !NodeProbeNotificationsValid(out) || out.State != "unknown" {
			t.Fatal("node read did not share cancellation or fabricated usable evidence")
		}
		if channels && (out.Expected == nil || !*out.Expected || len(out.Channels) != 2) {
			t.Fatal("source failure erased independently known node intent")
		}
	}
}

func TestNodeProbeNotificationQueriesHaveSemanticFixtures(t *testing.T) {
	body, err := os.ReadFile("../../promqlrules/testdata/operator_node_probe_notifications.test.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range servingNotificationQueriesForNode("fsn-2") {
		if !strings.Contains(string(body), query) {
			t.Fatal("scoped notification query lacks a real Prometheus fixture")
		}
	}
}
