package mcpmanagement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// This wraps the actual handler with a completion signal, keeping the real
// registration/authorization/serialization path. It avoids timing or counting
// native calls to guess whether the handler has returned.
func signalConsumerRead[In any](ready *atomic.Bool, handle func(context.Context, In) (any, error)) func(context.Context, In) (any, error) {
	return func(ctx context.Context, args In) (any, error) {
		out, err := handle(ctx, args)
		if err == nil {
			ready.Store(true)
		}
		return out, err
	}
}

func registerSignaledConsumerRead(s *mcp.Server, m *management, mode string, ready *atomic.Bool) {
	switch {
	case strings.HasPrefix(mode, "get"):
		register(s, m, getActivityConsumerTool, "Consumer read guard test", signalConsumerRead(ready, m.getActivityConsumer))
	case strings.HasPrefix(mode, "watch"):
		register(s, m, watchActivityConsumerTool, "Consumer read guard test", signalConsumerRead(ready, m.watchActivityConsumer))
	case strings.HasPrefix(mode, "recover"):
		register(s, m, recoverActivityConsumerTool, "Consumer read guard test", signalConsumerRead(ready, m.recoverActivityConsumer))
	case strings.HasPrefix(mode, "list"):
		register(s, m, listActivityConsumersTool, "Consumer read guard test", signalConsumerRead(ready, m.listActivityConsumers))
	}
}

func consumerGuardCall(mode string) (string, map[string]any) {
	switch {
	case strings.HasPrefix(mode, "get"):
		return getActivityConsumerTool, consumerArgs("alpha")
	case strings.HasPrefix(mode, "watch"):
		return watchActivityConsumerTool, consumerWatchArgs("alpha", 1, 1)
	case strings.HasPrefix(mode, "recover"):
		limit := 50
		if mode == "recover_partial" {
			limit = 1
		}
		return recoverActivityConsumerTool, consumerRecoveryArgs("alpha", "", limit)
	default:
		return listActivityConsumersTool, consumerListArgs("", 1)
	}
}

func TestActivityConsumerFinalReadGuards(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, remote := range []bool{false, true} {
			for _, mode := range []string{"get_ready", "watch_events", "recover_partial", "recover_complete", "list_page"} {
				for _, change := range []string{"ack", "retire", "revoke"} {
					t.Run(fmt.Sprintf("%s_http_%v/%s/%s", version, remote, mode, change), func(t *testing.T) {
						testConsumerFinalRead(t, version, remote, mode, change)
					})
				}
			}
		}
	}
}

func TestActivityConsumerFinalReadBranchesAndFailures(t *testing.T) {
	for _, mode := range []string{"get_uninitialized", "get_expired", "watch_uninitialized", "watch_expired", "watch_timeout", "recover_uninitialized", "recover_expired", "list_empty"} {
		t.Run(mode+"/create", func(t *testing.T) {
			change := "ack"
			if strings.Contains(mode, "uninitialized") || mode == "list_empty" {
				change = "create"
			}
			testConsumerFinalRead(t, "2026-07-28", false, mode, change)
		})
	}
	for _, mode := range []string{"get_ready", "watch_events", "recover_partial", "list_page"} {
		for _, change := range []string{"unchanged", "noop", "other_scope", "storage", "deny", "identity", "cancel", "drain"} {
			t.Run(mode+"/"+change, func(t *testing.T) {
				testConsumerFinalRead(t, "2026-07-28", true, mode, change)
			})
		}
	}
	for _, mode := range []string{"get_ready", "watch_events", "recover_complete", "get_uninitialized", "watch_uninitialized", "watch_timeout", "get_expired", "watch_expired", "recover_uninitialized", "recover_expired", "list_empty"} {
		t.Run(mode+"/unchanged", func(t *testing.T) {
			testConsumerFinalRead(t, "2026-07-28", false, mode, "unchanged")
		})
	}
	for _, mode := range []string{"get_ready", "watch_events", "recover_complete"} {
		t.Run(mode+"/other_consumer", func(t *testing.T) {
			testConsumerFinalRead(t, "2026-07-28", false, mode, "other_consumer")
		})
	}
}

func testConsumerFinalRead(t *testing.T, version string, remote bool, mode, change string) {
	t.Helper()
	f, base, opts := newEnvironmentScheduleFixture(t)
	opts, store, _, _ := consumerOptions(t, opts)
	opts.Telemetry = NewTelemetry()
	if change == "revoke" {
		opts.grantID, opts.accountID, opts.planNamespace = "grant", f.account.ID, "grant"
		snapshot := oauthSnapshot{Grants: map[string]storedGrant{"grant": {ID: "grant", Active: true, AccountID: f.account.ID, Scopes: []string{scopeRead}, Expires: time.Now().Add(time.Hour), Environments: opts.Environments}}}
		if err := store.update("oauth", snapshot); err != nil {
			t.Fatal(err)
		}
	}
	var ready, denied, recreated atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects/shop" && ready.CompareAndSwap(true, false) {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		if denied.Load() {
			api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Denied", "Denied"))
			return
		}
		if recreated.Load() && r.URL.Path == "/v1/projects/shop/environments/staging" {
			_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentResponse{ID: "replacement", ProjectID: f.project.ID, Slug: "staging"})
			return
		}
		base.Config.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(backend.Close)
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	opts.APIBase = backend.URL
	h := connectSubscriptionHarness(t, backend, opts, version, remote, func(s *mcp.Server, m *management) {
		registerSignaledConsumerRead(s, m, mode, &ready)
	})
	b, _ := connectConfiguredTestServer(t, backend, opts, version)
	if mode != "list_empty" {
		for _, name := range []string{"alpha", "bravo", "charlie"} {
			if name == "alpha" && strings.Contains(mode, "uninitialized") {
				continue
			}
			callTool(t, h.ctx, b, ackActivityConsumerTool, consumerAck(name, activityCursor(t, f, 0), 0, false), false)
		}
	}
	if mode != "watch_timeout" {
		seedSchedule(t, f, time.Now().Add(time.Hour))
		seedSchedule(t, f, time.Now().Add(2*time.Hour))
	}
	if strings.Contains(mode, "expired") {
		if err := f.store.PruneEnvironmentCacheScheduleActivity(h.ctx, time.Now().Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	name, args := consumerGuardCall(mode)
	callCtx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	type answer struct {
		result *mcp.CallToolResult
		err    error
	}
	done := make(chan answer, 1)
	go func() {
		r, e := h.client.CallTool(callCtx, &mcp.CallToolParams{Name: name, Arguments: args})
		done <- answer{r, e}
	}()
	select {
	case <-entered:
	case <-h.ctx.Done():
		t.Fatal("tool did not reach its post-handler authorization read")
	}
	code := "checkpoint_conflict"
	target := "alpha"
	if strings.HasPrefix(mode, "list") && mode != "list_empty" {
		target = "charlie" // Outside the returned page, but inside its guarded scope.
	}
	switch change {
	case "ack", "create":
		cursor, revision, rebuilt := int64(1), int64(1), false
		if change == "create" {
			revision = 0
		}
		if mode == "watch_timeout" {
			seedSchedule(t, f, time.Now().Add(time.Hour))
		}
		if strings.Contains(mode, "expired") {
			cursor, rebuilt = 2, true
		}
		callTool(t, h.ctx, b, ackActivityConsumerTool, consumerAck(target, activityCursor(t, f, cursor), revision, rebuilt), false)
	case "retire":
		callTool(t, h.ctx, b, retireActivityConsumerTool, consumerRetirement(target, 1, ""), false)
		code = "consumer_generation_conflict"
	case "revoke":
		if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}); err != nil {
			t.Fatal(err)
		}
		code = "mcp_connection_revoked"
	case "storage":
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		code = "durable_state_unavailable"
	case "deny":
		denied.Store(true)
		code = "insufficient_scope"
	case "identity":
		recreated.Store(true)
		code = "environment_access_denied"
	case "cancel":
		cancel()
		code = "cancelled"
	case "drain":
		h.manager.lifecycle.BeginDrain()
		code = "cancelled"
	case "noop":
		callTool(t, h.ctx, b, ackActivityConsumerTool, consumerAck(target, activityCursor(t, f, 0), 0, false), false)
	case "other_consumer":
		callTool(t, h.ctx, b, ackActivityConsumerTool, consumerAck("bravo", activityCursor(t, f, 1), 1, false), false)
	case "other_scope":
		var records map[string]activityConsumerRecord
		if err := store.read("activity-consumers/"+opts.planNamespace, &records); err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			record.Binding.Scope, record.Binding.Consumer = "production", "foreign-worker"
			records[record.Binding.key()] = record
			break
		}
		if err := store.update("activity-consumers/"+opts.planNamespace, records); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	success := change == "unchanged" || change == "noop" || change == "other_scope" || change == "other_consumer"
	select {
	case out := <-done:
		if out.err != nil && change != "cancel" {
			t.Fatal(out.err)
		}
		if out.err == nil {
			if out.result == nil || out.result.IsError == success {
				t.Fatal("unexpected guarded tool result", out.result)
			}
			data, _ := json.Marshal(out.result.StructuredContent)
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if success {
				assertConsumerGuardPayload(t, mode, fields)
			} else {
				if string(fields["code"]) != `"`+code+`"` {
					t.Fatal(fields)
				}
				for _, key := range []string{"cursor", "page", "entries", "generation", "revision", "lag", "activity_recovery", "activity_cursor", "recovery_token", "next_cursor", "acknowledgment"} {
					if fields[key] != nil {
						t.Fatal("rejected read exposed stale metadata", key)
					}
				}
			}
		}
	case <-h.ctx.Done():
		t.Fatal("final authorization read did not finish")
	}
	awaitAdmissionEmpty(t, h.manager.admission)
	if success && strings.HasPrefix(mode, "get") {
		status := "ready"
		if mode == "get_uninitialized" {
			status = "uninitialized"
		} else if mode == "get_expired" {
			status = "cursor_expired"
		}
		expectMetric(t, opts.Telemetry, `gregale_management_mcp_consumer_reads_total{status="`+status+`"} 1`)
	} else if strings.Contains(telemetryText(t, opts.Telemetry), "gregale_management_mcp_consumer_reads_total{") {
		t.Fatal("unsuccessful get or another consumer tool sampled lag")
	}
	if f.writes != 0 {
		t.Fatal("read guard caused native writes")
	}
}

func TestActivityConsumerListLivePaginationAfterACK(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	for _, name := range []string{"alpha", "bravo", "charlie"} {
		callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck(name, activityCursor(t, f, 0), 0, false), false)
	}
	first := decodeConsumerList(t, callTool(t, ctx, cs, listActivityConsumersTool, consumerListArgs("", 1), false))
	seedSchedule(t, f, time.Now().Add(time.Hour))
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("charlie", activityCursor(t, f, 1), 1, false), false)
	tail := decodeConsumerList(t, callTool(t, ctx, cs, listActivityConsumersTool, consumerListArgs(first.NextCursor, 2), false))
	if len(tail.Entries) != 2 || tail.Entries[1].Consumer != "charlie" || tail.Entries[1].Revision != 2 || tail.NextCursor != "" {
		t.Fatal("final list guard changed live pagination semantics", tail)
	}
}

func assertConsumerGuardPayload(t *testing.T, mode string, fields map[string]json.RawMessage) {
	t.Helper()
	for _, key := range []string{"value", "args", "binding", "record", "snapshot", "sample"} {
		if fields[key] != nil {
			t.Fatal("private guard context leaked", key)
		}
	}
	switch {
	case strings.HasPrefix(mode, "get"):
		if fields["lag"] == nil || fields["consumer"] == nil {
			t.Fatal("get lost its wire format", fields)
		}
	case strings.HasPrefix(mode, "watch"):
		if fields["watch_status"] == nil {
			t.Fatal("watch lost its wire format", fields)
		}
		if mode == "watch_events" && fields["acknowledgment"] == nil || mode == "watch_timeout" && fields["acknowledgment"] != nil {
			t.Fatal("watch acknowledgment semantics changed", fields)
		}
	case strings.HasPrefix(mode, "recover"):
		if mode == "recover_partial" && (fields["recovery_token"] == nil || fields["acknowledgment"] != nil) || mode != "recover_partial" && fields["acknowledgment"] == nil {
			t.Fatal("recovery changed continuation/ACK semantics", fields)
		}
	case strings.HasPrefix(mode, "list"):
		if fields["entries"] == nil || mode == "list_page" && fields["next_cursor"] == nil {
			t.Fatal("list lost its wire format", fields)
		}
	}
}
