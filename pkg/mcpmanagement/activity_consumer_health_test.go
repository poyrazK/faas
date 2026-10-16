package mcpmanagement

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func decodeConsumerHealth(t *testing.T, fields map[string]json.RawMessage) activityConsumerHealthResult {
	t.Helper()
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{`"cursor":`, `"activity_cursor":`, `"activity_recovery":`, `"acknowledgment":`, `"request":`, `"records":`, `"snapshot":`} {
		if strings.Contains(string(data), private) {
			t.Fatal("health exposed processing metadata", private)
		}
	}
	var out activityConsumerHealthResult
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func noConsumerHealth(t *testing.T, fields map[string]json.RawMessage, code string) {
	t.Helper()
	if string(fields["code"]) != `"`+code+`"` {
		t.Fatal(fields)
	}
	for _, key := range []string{"summary", "entries", "generation", "next_cursor", "observed_at", "activity_recovery"} {
		if fields[key] != nil {
			t.Fatal("failed health read exposed metadata", key)
		}
	}
}

func TestActivityConsumerHealthReadinessPaginationAndReadOnly(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, remote := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_http_%v", version, remote), func(t *testing.T) {
				f, base, opts := newEnvironmentScheduleFixture(t)
				opts, store, path, _ := consumerOptions(t, opts)
				opts.Telemetry = NewTelemetry()
				var feed, checkpoints atomic.Int64
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/activity") {
						feed.Add(1)
					}
					if strings.HasSuffix(r.URL.Path, "/activity/checkpoint") {
						checkpoints.Add(1)
					}
					base.Config.Handler.ServeHTTP(w, r)
				}))
				t.Cleanup(backend.Close)
				opts.APIBase = backend.URL
				h := connectSubscriptionHarness(t, backend, opts, version, remote)
				get := func(after string, limit int) activityConsumerHealthResult {
					t.Helper()
					reads, points := feed.Load(), checkpoints.Load()
					out := decodeConsumerHealth(t, callTool(t, h.ctx, h.client, activityConsumerHealthTool, consumerListArgs(after, limit), false))
					if feed.Load()-reads != 1 || checkpoints.Load()-points != 2 {
						t.Fatal("health reads scaled with consumer count", feed.Load()-reads, checkpoints.Load()-points)
					}
					return out
				}
				empty := get("", 0)
				if empty.Entries == nil || len(empty.Entries) != 0 || empty.Summary != (activityConsumerHealthSummary{}) || empty.NextCursor != "" {
					t.Fatal(empty)
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("health created consumer state", err)
				}
				seedSchedule(t, f, time.Now().Add(time.Hour))
				seedSchedule(t, f, time.Now().Add(2*time.Hour))
				cutoff := time.Now()
				seedSchedule(t, f, time.Now().Add(3*time.Hour))
				for name, cursor := range map[string]int64{"alpha": 0, "bravo": 2, "charlie": 3} {
					callTool(t, h.ctx, h.client, ackActivityConsumerTool, consumerAck(name, activityCursor(t, f, cursor), 0, false), false)
				}
				var records map[string]activityConsumerRecord
				if err := store.read("activity-consumers/"+opts.planNamespace, &records); err != nil {
					t.Fatal(err)
				}
				for key, record := range records {
					if record.Binding.Consumer == "charlie" {
						record.UpdatedAt = time.Now().Add(time.Hour).UTC()
						records[key] = record
					}
				}
				if err := store.update("activity-consumers/"+opts.planNamespace, records); err != nil {
					t.Fatal(err)
				}
				if err := f.store.PruneEnvironmentCacheScheduleActivity(h.ctx, cutoff); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				first := get("", 1)
				wantSummary := activityConsumerHealthSummary{Total: 3, Ready: 2, CursorExpired: 1, WithRetainedBacklog: 2, NeedsAttention: 2}
				if first.Summary != wantSummary || first.AppID != f.app.ID || first.ProjectID != f.project.ID || first.EnvironmentID != f.env.ID || first.Scope != "staging" || first.ObservedAt.IsZero() || first.RetentionSeconds != api.EnvironmentCacheActivityRetentionSeconds || len(first.Entries) != 1 || first.NextCursor == "" {
					t.Fatal(first)
				}
				alpha := first.Entries[0]
				if alpha.Consumer != "alpha" || alpha.Status != "cursor_expired" || alpha.Revision != 1 || *alpha.Lag.RetainedPendingEvents != 1 || *alpha.Lag.ExpiredUnacknowledgedEvents != 2 || alpha.Lag.SecondsSinceAck == nil || alpha.Lag.ObservedAt != first.ObservedAt {
					t.Fatal(alpha)
				}
				tail := get(first.NextCursor, 2)
				if tail.Summary != wantSummary || len(tail.Entries) != 2 || tail.NextCursor != "" || tail.Entries[0].Consumer != "bravo" || tail.Entries[1].Consumer != "charlie" {
					t.Fatal(tail)
				}
				bravo, charlie := tail.Entries[0], tail.Entries[1]
				if bravo.Status != "ready" || *bravo.Lag.RetainedPendingEvents != 1 || *bravo.Lag.ExpiredUnacknowledgedEvents != 0 || charlie.Status != "ready" || *charlie.Lag.RetainedPendingEvents != 0 || charlie.Lag.SecondsSinceAck != nil || !charlie.Lag.AckTimeInFuture {
					t.Fatal(bravo, charlie)
				}
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) || f.writes != 0 {
					t.Fatal("health changed journal or native intent", err)
				}
				if strings.Contains(telemetryText(t, opts.Telemetry), "gregale_management_mcp_consumer_reads_total{") {
					t.Fatal("health sampled exact get-tool lag metrics")
				}
			})
		}
	}
}

func TestActivityConsumerHealthScopedCapacityAndIsolation(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, store, _, _ := consumerOptions(t, opts)
	b := activityConsumerBinding{Endpoint: backend.URL, Account: f.account.ID, App: f.app.ID, Project: f.project.ID, Environment: f.env.ID, Scope: "staging"}
	records := map[string]activityConsumerRecord{}
	for i := range api.MCPActivityConsumersPerConnection {
		target := b
		target.Consumer = fmt.Sprintf("worker-%03d", i)
		switch i {
		case 0:
			target.Endpoint = "https://another.example"
		case 1:
			target.Account = "foreign-account"
		case 2:
			target.App = "foreign-app"
		case 3:
			target.Project = "foreign-project"
		case 4:
			target.Environment = "foreign-environment"
		case 5:
			target.Scope = "production"
		}
		cursor := api.EncodeEnvironmentCacheActivityCursor(api.EnvironmentCacheActivityCursor{AccountID: target.Account, AppID: target.App, ProjectID: target.Project, EnvironmentID: target.Environment})
		records[target.key()] = activityConsumerRecord{Binding: target, Cursor: cursor, Revision: 1, UpdatedAt: time.Now().UTC()}
	}
	if err := store.update("activity-consumers/"+opts.planNamespace, records); err != nil {
		t.Fatal(err)
	}
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	var names []string
	after := ""
	limit := 0
	for {
		out := decodeConsumerHealth(t, callTool(t, ctx, cs, activityConsumerHealthTool, consumerListArgs(after, limit), false))
		if out.Summary != (activityConsumerHealthSummary{Total: 94, Ready: 94}) || len(out.Entries) > 50 {
			t.Fatal(out)
		}
		if limit == 0 && len(out.Entries) != api.MCPActivityConsumerListDefault {
			t.Fatal("wrong default health page size", len(out.Entries))
		}
		for _, entry := range out.Entries {
			names = append(names, entry.Consumer)
		}
		if out.NextCursor == "" {
			break
		}
		after = out.NextCursor
		limit = api.MCPActivityConsumerListMax
	}
	if len(names) != 94 || names[0] != "worker-006" || names[93] != "worker-099" {
		t.Fatal(names)
	}
	other := opts
	other.planNamespace = "other-connection"
	otherClient, otherCtx := connectConfiguredTestServer(t, backend, other, "2026-07-28")
	noConsumerHealth(t, callTool(t, otherCtx, otherClient, activityConsumerHealthTool, consumerListArgs(after, 1), true), "invalid_argument")
	empty := decodeConsumerHealth(t, callTool(t, otherCtx, otherClient, activityConsumerHealthTool, consumerListArgs("", 0), false))
	if empty.Summary.Total != 0 || len(empty.Entries) != 0 {
		t.Fatal("foreign connection leaked consumers", empty)
	}
}

func TestActivityConsumerHealthContinuationGuardsAndLiveFeed(t *testing.T) {
	for _, change := range []string{"ack", "ack_retry", "create", "retire", "foreign", "feed"} {
		t.Run(change, func(t *testing.T) {
			f, backend, opts := newEnvironmentScheduleFixture(t)
			opts, store, _, _ := consumerOptions(t, opts)
			cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			for _, name := range []string{"alpha", "bravo", "charlie"} {
				callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck(name, activityCursor(t, f, 0), 0, false), false)
			}
			first := decodeConsumerHealth(t, callTool(t, ctx, cs, activityConsumerHealthTool, consumerListArgs("", 1), false))
			seedSchedule(t, f, time.Now().Add(time.Hour))
			code := "checkpoint_conflict"
			switch change {
			case "ack":
				// A change outside the returned page must invalidate its full summary.
				callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("charlie", activityCursor(t, f, 1), 1, false), false)
			case "ack_retry":
				callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("charlie", activityCursor(t, f, 0), 0, false), false)
			case "create":
				callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("delta", activityCursor(t, f, 0), 0, false), false)
			case "retire":
				callTool(t, ctx, cs, retireActivityConsumerTool, consumerRetirement("charlie", 1, ""), false)
				code = "consumer_generation_conflict"
			case "foreign":
				var records map[string]activityConsumerRecord
				if err := store.read("activity-consumers/"+opts.planNamespace, &records); err != nil {
					t.Fatal(err)
				}
				for _, record := range records {
					record.Binding.Scope = "production"
					records[record.Binding.key()] = record
					break
				}
				if err := store.update("activity-consumers/"+opts.planNamespace, records); err != nil {
					t.Fatal(err)
				}
			}
			if change == "feed" || change == "foreign" || change == "ack_retry" {
				tail := decodeConsumerHealth(t, callTool(t, ctx, cs, activityConsumerHealthTool, consumerListArgs(first.NextCursor, 2), false))
				if tail.Summary != (activityConsumerHealthSummary{Total: 3, Ready: 3, WithRetainedBacklog: 3, NeedsAttention: 3}) || len(tail.Entries) != 2 {
					t.Fatal("pagination froze feed or included foreign records", tail)
				}
			} else {
				noConsumerHealth(t, callTool(t, ctx, cs, activityConsumerHealthTool, consumerListArgs(first.NextCursor, 2), true), code)
			}
		})
	}
}

func TestActivityConsumerHealthRejectsMalformedTokensAndLimits(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	for _, name := range []string{"alpha", "bravo"} {
		callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck(name, activityCursor(t, f, 0), 0, false), false)
	}
	first := decodeConsumerHealth(t, callTool(t, ctx, cs, activityConsumerHealthTool, consumerListArgs("", 1), false))
	list := decodeConsumerList(t, callTool(t, ctx, cs, listActivityConsumersTool, consumerListArgs("", 1), false))
	noConsumerHealth(t, callTool(t, ctx, cs, activityConsumerHealthTool, consumerListArgs(list.NextCursor, 1), true), "invalid_argument")
	callTool(t, ctx, cs, listActivityConsumersTool, consumerListArgs(first.NextCursor, 1), true)
	for _, field := range []string{"version", "binding", "snapshot", "generation", "after"} {
		token, err := decodeConsumerHealthCursor(first.NextCursor)
		if err != nil {
			t.Fatal(err)
		}
		code := "invalid_argument"
		switch field {
		case "version":
			token.Version = 2
		case "binding":
			token.Binding = strings.Repeat("x", 64)
		case "snapshot":
			token.Snapshot = strings.Repeat("x", 64)
			code = "checkpoint_conflict"
		case "generation":
			token.Generation = "cg_" + strings.Repeat("a", 43)
			code = "consumer_generation_conflict"
		case "after":
			token.After = "Upper"
		}
		raw, _ := json.Marshal(token)
		noConsumerHealth(t, callTool(t, ctx, cs, activityConsumerHealthTool, consumerListArgs(base64.RawURLEncoding.EncodeToString(raw), 1), true), code)
	}
	tokenRaw, _ := base64.RawURLEncoding.DecodeString(first.NextCursor)
	for _, raw := range []string{"invalid", strings.Repeat("a", api.MCPActivityConsumerListCursorMaxBytes+1), base64.RawURLEncoding.EncodeToString(append(tokenRaw, []byte(` {}`)...)), base64.RawURLEncoding.EncodeToString([]byte(strings.TrimSuffix(string(tokenRaw), "}") + `,"extra":true}`))} {
		noConsumerHealth(t, callTool(t, ctx, cs, activityConsumerHealthTool, consumerListArgs(raw, 1), true), "invalid_argument")
	}
	for _, limit := range []int{-1, api.MCPActivityConsumerListMax + 1} {
		noConsumerHealth(t, callTool(t, ctx, cs, activityConsumerHealthTool, consumerListArgs("", limit), true), "invalid_argument")
	}
	args := consumerListArgs("", 1)
	args["environment"] = ""
	noConsumerHealth(t, callTool(t, ctx, cs, activityConsumerHealthTool, args, true), "invalid_argument")
}

func TestActivityConsumerHealthContinuationSurvivesRestart(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, store, path, key := consumerOptions(t, opts)
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	for _, name := range []string{"alpha", "bravo"} {
		callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck(name, activityCursor(t, f, 0), 0, false), false)
	}
	first := decodeConsumerHealth(t, callTool(t, ctx, cs, activityConsumerHealthTool, consumerListArgs("", 1), false))
	_ = cs.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = next.Close() })
	opts.planStore = next
	cs, ctx = connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	tail := decodeConsumerHealth(t, callTool(t, ctx, cs, activityConsumerHealthTool, consumerListArgs(first.NextCursor, 1), false))
	if !reflect.DeepEqual(tail.Summary, first.Summary) || len(tail.Entries) != 1 || tail.Entries[0].Consumer != "bravo" || tail.NextCursor != "" {
		t.Fatal(tail)
	}
}
