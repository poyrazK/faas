package mcpmanagement

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestActivityConsumerLagCountsAndMetrics(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	opts.Telemetry = NewTelemetry()
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	get := func() activityConsumerResult {
		return decodeConsumer(t, callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("private-worker"), false))
	}
	seedSchedule(t, f, time.Now().Add(time.Hour))
	first := get()
	if first.Lag == nil || first.Lag.RetainedPendingEvents != nil || first.Lag.ExpiredUnacknowledgedEvents != nil || first.Lag.SecondsSinceAck != nil || first.Cursor != "" {
		t.Fatal("uninitialized baseline was invented", first)
	}
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("private-worker", activityCursor(t, f, 0), 0, false), false)
	ready := get()
	checkConsumerLag(t, ready, "ready", 1, 0)
	if ready.Lag.SecondsSinceAck == nil || *ready.Lag.SecondsSinceAck < 0 || ready.Lag.ObservedAt.Before(*ready.UpdatedAt) {
		t.Fatal(ready.Lag)
	}
	if err := f.store.PruneEnvironmentCacheScheduleActivity(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	seedSchedule(t, f, time.Now().Add(2*time.Hour))
	expired := get()
	checkConsumerLag(t, expired, "cursor_expired", 1, 1)
	if expired.Revision != ready.Revision || expired.Cursor != ready.Cursor {
		t.Fatal("diagnostics advanced progress")
	}
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("private-worker", activityCursor(t, f, 2), 1, true), false)
	caughtUp := get()
	checkConsumerLag(t, caughtUp, "ready", 0, 0)
	// ACK retry is a receipt/no-op, not a new progress timestamp.
	retry := decodeConsumer(t, callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("private-worker", activityCursor(t, f, 2), 1, true), false))
	if !retry.UpdatedAt.Equal(*caughtUp.UpdatedAt) || retry.Lag != nil {
		t.Fatal("retry changed age or invented an observation")
	}
	expectMetric(t, opts.Telemetry, `gregale_management_mcp_consumer_reads_total{status="uninitialized"} 1`)
	expectMetric(t, opts.Telemetry, `gregale_management_mcp_consumer_reads_total{status="ready"} 2`)
	expectMetric(t, opts.Telemetry, `gregale_management_mcp_consumer_reads_total{status="cursor_expired"} 1`)
	expectMetric(t, opts.Telemetry, `gregale_management_mcp_consumer_lag_events_sum{kind="retained"} 2`)
	expectMetric(t, opts.Telemetry, `gregale_management_mcp_consumer_lag_events_sum{kind="expired"} 1`)
	expectMetric(t, opts.Telemetry, `gregale_management_mcp_consumer_lag_events_count{kind="retained"} 3`)
	expectMetric(t, opts.Telemetry, `gregale_management_mcp_consumer_ack_age_seconds_count 3`)
	text := telemetryText(t, opts.Telemetry)
	for _, secret := range []string{"private-worker", f.account.ID, f.app.ID, f.env.ID, backend.URL, ready.Cursor, "consumer-fixture", "account_id", "consumer="} {
		if strings.Contains(text, secret) {
			t.Fatalf("identity leaked to metrics: %s", secret)
		}
	}
	if f.writes != 0 {
		t.Fatal("diagnostics wrote native intent")
	}
}

func checkConsumerLag(t *testing.T, got activityConsumerResult, status string, pending, expired int64) {
	t.Helper()
	if got.Status != status || got.Lag == nil || got.Lag.RetainedPendingEvents == nil || got.Lag.ExpiredUnacknowledgedEvents == nil || *got.Lag.RetainedPendingEvents != pending || *got.Lag.ExpiredUnacknowledgedEvents != expired || got.Lag.RetentionSeconds != api.EnvironmentCacheActivityRetentionSeconds || got.Lag.ObservedAt.IsZero() {
		t.Fatalf("unexpected lag: %+v %+v", got, got.Lag)
	}
}

func TestActivityConsumerLagConcurrentChanges(t *testing.T) {
	for _, phase := range []string{"old_checkpoint", "ack_during_read", "retention_during_read"} {
		t.Run(phase, func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			opts, _, _, _ = consumerOptions(t, opts)
			opts.Telemetry = NewTelemetry()
			var block atomic.Bool
			entered, release := make(chan struct{}), make(chan struct{})

			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				checkpoint := strings.HasSuffix(r.URL.Path, "/activity/checkpoint")
				feed := strings.HasSuffix(r.URL.Path, "/activity") && r.URL.Query().Get("after") == activityCursor(t, f, 0)
				if (phase == "old_checkpoint" && checkpoint || phase != "old_checkpoint" && feed) && block.CompareAndSwap(true, false) {
					if checkpoint {
						captured := httptest.NewRecorder()
						base.Config.Handler.ServeHTTP(captured, r)
						close(entered)
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
						for k, values := range captured.Header() {
							w.Header()[k] = values
						}
						w.WriteHeader(captured.Code)
						_, _ = io.Copy(w, captured.Body)
						return
					}
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				base.Config.Handler.ServeHTTP(w, r)
			}))
			defer backend.Close()
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			opts.APIBase = backend.URL
			a, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			b, _ := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			callTool(t, ctx, a, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
			block.Store(true)
			type answer struct {
				result *mcp.CallToolResult
				err    error
			}
			done := make(chan answer, 1)
			go func() {
				r, e := a.CallTool(ctx, &mcp.CallToolParams{Name: getActivityConsumerTool, Arguments: consumerArgs("worker")})
				done <- answer{r, e}
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("inspection never blocked")
			}
			seedSchedule(t, f, time.Now().Add(time.Hour))
			if phase == "retention_during_read" {
				if err := f.store.PruneEnvironmentCacheScheduleActivity(ctx, time.Now().Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
				seedSchedule(t, f, time.Now().Add(2*time.Hour))
			} else {
				callTool(t, ctx, b, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 1), 1, false), false)
			}
			close(release)
			var out answer
			select {
			case out = <-done:
			case <-ctx.Done():
				t.Fatal("inspection did not finish")
			}
			if out.err != nil {
				t.Fatal(out.err)
			}
			raw, _ := json.Marshal(out.result.StructuredContent)
			if phase == "ack_during_read" {
				if !out.result.IsError || !strings.Contains(string(raw), "checkpoint_conflict") {
					t.Fatalf("stale revision returned: %s", raw)
				}
				expectMetric(t, opts.Telemetry, `gregale_management_mcp_requests_total{operation="tools/call",outcome="checkpoint_conflict",tool="gregale_get_environment_cache_activity_consumer"} 1`)
				if strings.Contains(telemetryText(t, opts.Telemetry), "gregale_management_mcp_consumer_reads_total{") {
					t.Fatal("failed inspection sampled lag")
				}
			} else {
				if out.result.IsError {
					t.Fatal(string(raw))
				}
				var got activityConsumerResult
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatal(err)
				}
				if phase == "old_checkpoint" {
					checkConsumerLag(t, got, "ready", 0, 0)
					if got.Revision != 2 {
						t.Fatal(got)
					}
				} else {
					checkConsumerLag(t, got, "cursor_expired", 1, 1)
				}
			}
		})
	}
}

func TestActivityConsumerLagBoundsAndClockSkew(t *testing.T) {
	b := activityConsumerBinding{Account: "acct", App: "app", Project: "proj", Environment: "env", Scope: "staging"}
	cursor := func(n int64) string {
		return api.EncodeEnvironmentCacheActivityCursor(api.EnvironmentCacheActivityCursor{AccountID: b.Account, AppID: b.App, ProjectID: b.Project, EnvironmentID: b.Environment, Sequence: n})
	}
	at := time.Now().UTC()
	point := api.EnvironmentCacheActivityRecovery{AppID: b.App, ProjectID: b.Project, EnvironmentID: b.Environment, Scope: b.Scope, OldestCursor: cursor(3), RestartCursor: cursor(3), RetentionSeconds: api.EnvironmentCacheActivityRetentionSeconds}
	record := activityConsumerRecord{Revision: 1, Cursor: cursor(0), UpdatedAt: at.Add(time.Minute)}
	lag, err := consumerLag(b, record, point, at)
	if err != nil || *lag.RetainedPendingEvents != 0 || *lag.ExpiredUnacknowledgedEvents != 3 || lag.SecondsSinceAck != nil || !lag.AckTimeInFuture {
		t.Fatal(lag, err)
	}
	metrics := NewTelemetry()
	metrics.observeConsumer("cursor_expired", lag)
	expectMetric(t, metrics, `gregale_management_mcp_consumer_ack_age_seconds_count 0`)
	for _, change := range []string{"account", "scope", "ahead", "floor", "retention"} {
		t.Run(change, func(t *testing.T) {
			p, r := point, record
			switch change {
			case "account":
				p.RestartCursor = strings.ReplaceAll(cursor(3), "a", "b")
			case "scope":
				p.Scope = "other"
			case "ahead":
				r.Cursor = cursor(4)
			case "floor":
				p.OldestCursor = cursor(4)
			case "retention":
				p.RetentionSeconds = 1
			}
			if _, err := consumerLag(b, r, p, at); err == nil {
				t.Fatal("invalid observation accepted")
			}
		})
	}
	// The largest valid sequence is still an exact integer count.
	point.RestartCursor = cursor(api.RouteRequirementsMaxRevision)
	lag, err = consumerLag(b, record, point, at)
	if err != nil || *lag.RetainedPendingEvents != api.RouteRequirementsMaxRevision-3 {
		t.Fatal(lag, err)
	}
}
