package operatorobs

import (
	"context"
	"encoding/json"
	"math"
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

type notificationReceiverFixture struct {
	rows    [4][]promql.VectorSample
	fail    [4]bool
	warning [4]bool
	block   bool
}

func receiverFixtureRow(metric, receiver, integration string, value float64) promql.VectorSample {
	labels := map[string]string{receiverMetric: metric, "job": "node", "instance": "private-target:9100"}
	if receiver != "" {
		labels["receiver"], labels["integration"] = receiver, integration
	}
	return promql.VectorSample{Labels: labels, Value: value}
}

func receiverFixture(now time.Time) notificationReceiverFixture {
	fixture := notificationReceiverFixture{}
	fixture.rows[0] = []promql.VectorSample{
		receiverFixtureRow("up", "", "", 1), receiverFixtureRow(receiverUp, "", "", 1),
		receiverFixtureRow(receiverRun, "", "", float64(now.Add(-30*time.Second).Unix())),
	}
	for _, receiver := range []string{"faas-page", "faas-warn"} {
		for metric, value := range map[string]float64{receiverExpected: 1, receiverRequests: 10, receiverFailures: 0, receiverSuccess: float64(now.Add(-90 * time.Second).Unix())} {
			fixture.rows[0] = append(fixture.rows[0], receiverFixtureRow(metric, receiver, "slack", value))
		}
		fixture.rows[2] = append(fixture.rows[2], receiverFixtureRow(receiverRequests, receiver, "slack", 0))
		fixture.rows[3] = append(fixture.rows[3], receiverFixtureRow(receiverFailures, receiver, "slack", 0))
	}
	for _, row := range fixture.rows[0] {
		copy := receiverFixtureRow(row.Labels[receiverMetric], row.Labels["receiver"], row.Labels["integration"], float64(now.Add(-15*time.Second).Unix()))
		fixture.rows[1] = append(fixture.rows[1], copy)
	}
	return fixture
}

func (fixture *notificationReceiverFixture) change(index int, metric, receiver string, update func(*promql.VectorSample)) {
	for i := range fixture.rows[index] {
		row := &fixture.rows[index][i]
		if row.Labels[receiverMetric] == metric && row.Labels["receiver"] == receiver {
			update(row)
		}
	}
}

func (fixture *notificationReceiverFixture) remove(index int, metric, receiver string) {
	rows := []promql.VectorSample{}
	for _, row := range fixture.rows[index] {
		if row.Labels[receiverMetric] != metric || receiver != "*" && row.Labels["receiver"] != receiver {
			rows = append(rows, row)
		}
	}
	fixture.rows[index] = rows
}

func (fixture notificationReceiverFixture) server(t *testing.T, now time.Time) *httptest.Server {
	t.Helper()
	queries := notificationReceiverQueries()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/query" || r.URL.Query().Get("time") != now.UTC().Format(time.RFC3339Nano) {
			t.Error("receiver read did not pin one evaluation time")
		}
		for i, query := range queries {
			if r.URL.Query().Get("query") != query {
				continue
			}
			if fixture.block {
				<-r.Context().Done()
				return
			}
			if fixture.fail[i] {
				http.Error(w, "private provider credentials", http.StatusServiceUnavailable)
				return
			}
			rows := []any{}
			for _, row := range fixture.rows[i] {
				rows = append(rows, map[string]any{"metric": row.Labels, "value": []any{now.Unix(), strconv.FormatFloat(row.Value, 'f', -1, 64)}})
			}
			payload := map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": rows}}
			if fixture.warning[i] {
				payload["warnings"] = []string{"private provider warning"}
			}
			_ = json.NewEncoder(w).Encode(payload)
			return
		}
		t.Error("unexpected receiver query")
		http.Error(w, "unknown", 400)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNotificationReceiverHealthEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	set := func(value float64) func(*promql.VectorSample) {
		return func(row *promql.VectorSample) { row.Value = value }
	}
	cases := []struct {
		name, state, groupReason string
		mutate                   func(*notificationReceiverFixture)
	}{
		{"quiet observed transports", "healthy", "", func(*notificationReceiverFixture) {}},
		{"retired range-query groups stay retired", "healthy", "", func(f *notificationReceiverFixture) {
			f.rows[2] = append(f.rows[2], receiverFixtureRow(receiverRequests, "faas-heartbeat", "webhook", 0))
			f.rows[3] = append(f.rows[3], receiverFixtureRow(receiverFailures, "faas-heartbeat", "webhook", 0))
		}},
		{"cold receipt baseline", "partial", "incomplete_evidence", func(f *notificationReceiverFixture) { f.change(0, receiverSuccess, "faas-page", set(0)) }},
		{"missing expected receiver", "degraded", "telemetry_missing", func(f *notificationReceiverFixture) {
			for i := range 2 {
				for _, metric := range []string{receiverRequests, receiverFailures, receiverSuccess} {
					f.remove(i, metric, "faas-page")
				}
			}
		}},
		{"missing inventory", "partial", "inventory_unavailable", func(f *notificationReceiverFixture) { f.remove(0, receiverExpected, "*") }},
		{"stale inventory", "partial", "inventory_unavailable", func(f *notificationReceiverFixture) {
			f.change(1, receiverExpected, "faas-page", set(float64(now.Add(-3*time.Minute).Unix())))
		}},
		{"invalid inventory", "partial", "inventory_unavailable", func(f *notificationReceiverFixture) { f.change(0, receiverExpected, "faas-page", set(2)) }},
		{"inventory belongs to another source", "partial", "inventory_unavailable", func(f *notificationReceiverFixture) {
			for i := range 2 {
				for _, receiver := range []string{"faas-page", "faas-warn"} {
					f.change(i, receiverExpected, receiver, func(row *promql.VectorSample) { row.Labels["instance"] = "private-other:9100" })
				}
				value := 1.0
				if i == 1 {
					value = float64(now.Unix())
				}
				up := receiverFixtureRow("up", "", "", value)
				up.Labels["instance"] = "private-other:9100"
				f.rows[i] = append(f.rows[i], up)
			}
		}},
		{"unexpected observed group", "degraded", "unexpected_group", func(f *notificationReceiverFixture) {
			f.remove(0, receiverExpected, "faas-page")
			f.remove(1, receiverExpected, "faas-page")
		}},
		{"collector failed", "partial", "collector_unavailable", func(f *notificationReceiverFixture) { f.change(0, receiverUp, "", set(0)) }},
		{"collector never installed", "partial", "collector_unavailable", func(f *notificationReceiverFixture) { f.remove(0, receiverUp, "") }},
		{"duplicate collector", "partial", "collector_unavailable", func(f *notificationReceiverFixture) {
			for i := range 2 {
				value := 1.0
				if i == 1 {
					value = float64(now.Unix())
				}
				row := receiverFixtureRow(receiverUp, "", "", value)
				row.Labels["instance"] = "private-other:9100"
				f.rows[i] = append(f.rows[i], row)
			}
		}},
		{"unpaired collector timestamp", "partial", "collector_unavailable", func(f *notificationReceiverFixture) {
			row := receiverFixtureRow(receiverRun, "", "", float64(now.Unix()))
			row.Labels["instance"] = "private-other:9100"
			f.rows[1] = append(f.rows[1], row)
		}},
		{"frozen collector", "partial", "collector_unavailable", func(f *notificationReceiverFixture) {
			f.change(0, receiverRun, "", set(float64(now.Add(-3*time.Minute).Unix())))
		}},
		{"future collector clock", "partial", "collector_unavailable", func(f *notificationReceiverFixture) {
			f.change(0, receiverRun, "", set(float64(now.Add(time.Minute).Unix())))
		}},
		{"node scrape failed", "partial", "inventory_unavailable", func(f *notificationReceiverFixture) { f.change(0, "up", "", set(0)) }},
		{"stale receipt gauge", "partial", "incomplete_evidence", func(f *notificationReceiverFixture) {
			f.change(1, receiverSuccess, "faas-page", set(float64(now.Add(-3*time.Minute).Unix())))
		}},
		{"future receipt clock", "partial", "incomplete_evidence", func(f *notificationReceiverFixture) {
			f.change(0, receiverSuccess, "faas-page", set(float64(now.Add(time.Minute).Unix())))
		}},
		{"counter with one sample", "partial", "incomplete_evidence", func(f *notificationReceiverFixture) { f.remove(2, receiverRequests, "faas-page") }},
		{"fractional raw counter", "partial", "incomplete_evidence", func(f *notificationReceiverFixture) { f.change(0, receiverRequests, "faas-page", set(1.5)) }},
		{"inconsistent completed counters", "partial", "incomplete_evidence", func(f *notificationReceiverFixture) { f.change(0, receiverFailures, "faas-page", set(11)) }},
		{"another target cannot supply a counter", "degraded", "telemetry_missing", func(f *notificationReceiverFixture) {
			for i := range 2 {
				f.change(i, receiverRequests, "faas-page", func(row *promql.VectorSample) { row.Labels["instance"] = "private-other:9100" })
			}
		}},
		{"duplicate counter cannot establish health", "partial", "incomplete_evidence", func(f *notificationReceiverFixture) {
			for i := range 2 {
				row := receiverFixtureRow(receiverRequests, "faas-page", "slack", 10)
				row.Labels["instance"] = "private-other:9100"
				if i == 1 {
					row.Value = float64(now.Unix())
				}
				f.rows[i] = append(f.rows[i], row)
			}
		}},
		{"one failed signal cannot hide valid failures", "degraded", "requests_failing", func(f *notificationReceiverFixture) {
			f.fail[2] = true
			f.change(3, receiverFailures, "faas-page", set(1.5))
		}},
		{"stale sibling cannot hide valid failures", "degraded", "requests_failing", func(f *notificationReceiverFixture) {
			f.change(1, receiverRequests, "faas-warn", set(float64(now.Add(-3*time.Minute).Unix())))
			f.change(3, receiverFailures, "faas-page", set(1.5))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := receiverFixture(now)
			tc.mutate(&fixture)
			srv := fixture.server(t, now)
			out := notificationReceiverHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), now)
			if out.State != tc.state || len(out.Groups) != 2 || out.Groups[0].Receiver != "faas-page" || out.Groups[0].Reason != tc.groupReason {
				t.Fatalf("receiver assessment: %+v", out)
			}
			if tc.name == "quiet observed transports" && (out.Groups[0].Requests.State != api.ObsHealthSignalHealthy || out.Groups[0].Requests.Value == nil || *out.Groups[0].Requests.Value != 0 || out.Groups[0].Expected == nil || !*out.Groups[0].Expected) {
				t.Fatal("quiet counter or independent intent was not known")
			}
			if tc.name == "cold receipt baseline" && (out.Groups[0].LastSuccessObservation.Value != nil || out.Groups[0].LastSuccessObservation.Reason != "success_not_observed") {
				t.Fatal("cold baseline fabricated a historical receipt")
			}
			body, _ := json.Marshal(out)
			if strings.Contains(string(body), "private") || strings.Contains(string(body), srv.URL) {
				t.Fatalf("source metadata leaked: %s", body)
			}
		})
	}
}

func TestNotificationReceiverInvalidSnapshotAndBounds(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, mode := range []string{"query error", "query warning", "nan", "unknown receiver", "unknown integration", "duplicate", "too many groups"} {
		t.Run(mode, func(t *testing.T) {
			fixture := receiverFixture(now)
			switch mode {
			case "query error":
				fixture.fail[0] = true
			case "query warning":
				fixture.warning[1] = true
			case "nan":
				fixture.rows[0][0].Value = math.NaN()
			case "unknown receiver":
				fixture.change(0, receiverRequests, "faas-page", func(row *promql.VectorSample) { row.Labels["receiver"] = "private-receiver" })
			case "unknown integration":
				fixture.change(0, receiverRequests, "faas-page", func(row *promql.VectorSample) { row.Labels["integration"] = "private-integration" })
			case "duplicate":
				fixture.rows[0] = append(fixture.rows[0], fixture.rows[0][0])
			case "too many groups":
				fixture.remove(0, receiverExpected, "*")
				for _, receiver := range NotificationReceiverNames() {
					for _, integration := range NotificationIntegrationNames() {
						fixture.rows[0] = append(fixture.rows[0], receiverFixtureRow(receiverExpected, receiver, integration, 1))
					}
				}
			}
			srv := fixture.server(t, now)
			out := notificationReceiverHealth(t.Context(), promql.NewClient(srv.URL, srv.Client()), now)
			body, _ := json.Marshal(out)
			if out.State != "partial" || out.Collector.Value != nil || out.Inventory.Value != nil || len(out.Groups) != 0 || strings.Contains(string(body), "private") {
				t.Fatalf("invalid snapshot: %s", body)
			}
		})
	}
}

func TestNotificationReceiverReadsShareCancellation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	fixture := receiverFixture(now)
	fixture.block = true
	srv := fixture.server(t, now)
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	out := notificationReceiverHealth(ctx, promql.NewClient(srv.URL, srv.Client()), now)
	if time.Since(start) > time.Second || out.State != "partial" || len(out.Groups) != 0 {
		t.Fatal("receiver reads did not share cancellation")
	}
}

func TestNotificationReceiverQueriesHaveSemanticFixtures(t *testing.T) {
	fixture, err := os.ReadFile("../../promqlrules/testdata/operator_notification_receivers.test.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range notificationReceiverQueries() {
		if !strings.Contains(string(fixture), query) {
			t.Fatal("receiver query lacks a real Prometheus semantic fixture")
		}
	}
}
