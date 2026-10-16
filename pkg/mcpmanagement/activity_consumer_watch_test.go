package mcpmanagement

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func consumerWatchArgs(name string, limit, wait int) map[string]any {
	args := consumerArgs(name)
	args["limit"], args["wait_seconds"] = limit, wait
	return args
}

func decodeConsumerWatch(t *testing.T, out map[string]json.RawMessage) activityConsumerWatchResult {
	t.Helper()
	raw, _ := json.Marshal(out)
	var got activityConsumerWatchResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestActivityConsumerWatchExplicitAckAndRestart(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, store, path, key := consumerOptions(t, opts)
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	catalog, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range catalog.Tools {
		if tool.Name == watchActivityConsumerTool {
			found = true
			if !tool.Annotations.ReadOnlyHint || *tool.Annotations.DestructiveHint {
				t.Fatal("watch advertises mutation")
			}
			raw, _ := json.Marshal(tool.InputSchema)
			if strings.Contains(string(raw), `"after"`) {
				t.Fatal("consumer watch takes caller-selected cursor")
			}
		}
	}
	if !found {
		t.Fatal("watch missing from read-consented catalog")
	}
	args := consumerWatchArgs("worker", 1, 1)
	initial := decodeConsumerWatch(t, callTool(t, ctx, cs, watchActivityConsumerTool, args, false))
	if initial.Status != "uninitialized" || initial.WatchStatus != "recovery_required" || initial.Cursor != "" || initial.Page != nil || initial.Acknowledgment != nil || initial.Hint == "" || initial.Lag != nil {
		t.Fatal(initial)
	}
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	seedSchedule(t, f, time.Now().Add(time.Hour))
	seedSchedule(t, f, time.Now().Add(2*time.Hour))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var page activityConsumerWatchResult
	for range 2 {
		page = decodeConsumerWatch(t, callTool(t, ctx, cs, watchActivityConsumerTool, args, false))
		if page.Status != "ready" || page.WatchStatus != "events" || page.WatchTimedOut || page.Page == nil || len(page.Page.Entries) != 1 || page.Page.Entries[0].Sequence != 1 || !page.Page.HasMore || page.Cursor != activityCursor(t, f, 0) || page.Revision != 1 || page.Acknowledgment == nil {
			t.Fatal(page)
		}
		ack := page.Acknowledgment
		if ack.ExpectedRevision != 1 || ack.ExpectedGeneration != page.Generation || ack.Cursor != page.Page.Cursor || ack.Consumer != "worker" || ack.BaselineRebuilt {
			t.Fatal(ack)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("watch wrote journal", err)
	}
	cs.Close()
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	opts.planStore = next
	cs, ctx = connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	replay := decodeConsumerWatch(t, callTool(t, ctx, cs, watchActivityConsumerTool, args, false))
	if replay.Page == nil || replay.Page.Cursor != page.Page.Cursor || replay.Revision != 1 {
		t.Fatal("restart skipped unacknowledged page", replay)
	}
	callTool(t, ctx, cs, ackActivityConsumerTool, replay.Acknowledgment, false)
	last := decodeConsumerWatch(t, callTool(t, ctx, cs, watchActivityConsumerTool, args, false))
	if last.Page == nil || last.Page.Entries[0].Sequence != 2 || last.Acknowledgment.ExpectedRevision != 2 {
		t.Fatal(last)
	}
	callTool(t, ctx, cs, ackActivityConsumerTool, last.Acknowledgment, false)
	timed := decodeConsumerWatch(t, callTool(t, ctx, cs, watchActivityConsumerTool, args, false))
	if timed.WatchStatus != "timed_out" || !timed.WatchTimedOut || timed.Revision != 3 || timed.Page == nil || len(timed.Page.Entries) != 0 || timed.Page.Cursor != last.Page.Cursor || timed.Cursor != last.Page.Cursor || timed.Acknowledgment != nil {
		t.Fatal(timed)
	}
	if f.writes != 0 {
		t.Fatal("watch mutated native intent")
	}
}

func TestActivityConsumerWatchPollArrival(t *testing.T) {
	f, base, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	var armed atomic.Bool
	var reads atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if armed.Load() && strings.HasSuffix(r.URL.Path, "/activity") && reads.Add(1) == 2 {
			seedScheduleContext(r.Context(), t, f, time.Now().Add(time.Hour))
		}
		base.Config.Handler.ServeHTTP(w, r)
	}))
	defer backend.Close()
	opts.APIBase = backend.URL
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	armed.Store(true)
	got := decodeConsumerWatch(t, callTool(t, ctx, cs, watchActivityConsumerTool, consumerWatchArgs("worker", 1, 3), false))
	if got.WatchStatus != "events" || got.Page == nil || len(got.Page.Entries) != 1 || got.Page.Entries[0].Sequence != 1 || got.Revision != 1 || reads.Load() != 2 {
		t.Fatal(got, reads.Load())
	}
}

func TestActivityConsumerWatchConcurrentChanges(t *testing.T) {
	for _, change := range []string{"ack", "retire", "expiry", "deny", "revoke", "storage"} {
		t.Run(change, func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			opts, store, _, _ := consumerOptions(t, opts)
			if change == "revoke" {
				opts.grantID, opts.accountID, opts.planNamespace = "grant", f.account.ID, "grant"
				snap := oauthSnapshot{Grants: map[string]storedGrant{"grant": {ID: "grant", Active: true, AccountID: f.account.ID, Scopes: []string{scopeRead}, Expires: time.Now().Add(time.Hour), Environments: opts.Environments}}}
				if err := store.update("oauth", snap); err != nil {
					t.Fatal(err)
				}
			}
			var armed, deny atomic.Bool
			entered, release := make(chan struct{}), make(chan struct{})
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/activity") {
					if deny.Load() {
						api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Denied", "Denied"))
						return
					}
					if armed.CompareAndSwap(true, false) {
						captured := httptest.NewRecorder()
						base.Config.Handler.ServeHTTP(captured, r)
						close(entered)
						select {
						case <-release:
						case <-r.Context().Done():
							return
						}
						for k, v := range captured.Header() {
							w.Header()[k] = v
						}
						w.WriteHeader(captured.Code)
						_, _ = io.Copy(w, captured.Body)
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
			armed.Store(true)
			type answer struct {
				result *mcp.CallToolResult
				err    error
			}
			done := make(chan answer, 1)
			go func() {
				r, e := a.CallTool(ctx, &mcp.CallToolParams{Name: watchActivityConsumerTool, Arguments: consumerWatchArgs("worker", 1, 3)})
				done <- answer{r, e}
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("watch never reached feed")
			}
			expected := ""
			switch change {
			case "ack":
				seedSchedule(t, f, time.Now().Add(time.Hour))
				callTool(t, ctx, b, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 1), 1, false), false)
				expected = "checkpoint_conflict"
			case "retire":
				callTool(t, ctx, b, retireActivityConsumerTool, consumerRetirement("worker", 1, ""), false)
				expected = "consumer_generation_conflict"
			case "expiry":
				seedSchedule(t, f, time.Now().Add(time.Hour))
				if err := f.store.PruneEnvironmentCacheScheduleActivity(ctx, time.Now().Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
			case "deny":
				deny.Store(true)
				expected = "insufficient_scope"
			case "revoke":
				if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}); err != nil {
					t.Fatal(err)
				}
				expected = "mcp_connection_revoked"
			case "storage":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				expected = "durable_state_unavailable"
			}
			close(release)
			var out answer
			select {
			case out = <-done:
			case <-ctx.Done():
				t.Fatal("watch never completed")
			}
			if out.err != nil {
				t.Fatal(out.err)
			}
			raw, _ := json.Marshal(out.result.StructuredContent)
			if change == "expiry" {
				var got activityConsumerWatchResult
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatal(err)
				}
				if out.result.IsError || got.Status != "cursor_expired" || got.WatchStatus != "recovery_required" || got.ActivityRecovery == nil || got.Page != nil || got.Acknowledgment != nil || got.WatchTimedOut || got.Revision != 1 || got.Cursor != activityCursor(t, f, 0) {
					t.Fatalf("expiry skipped recovery: %s", raw)
				}
			} else {
				var problem ToolError
				if err := json.Unmarshal(raw, &problem); err != nil {
					t.Fatal(err)
				}
				if !out.result.IsError || problem.Code != expected {
					t.Fatalf("change=%s want=%s got=%s", change, expected, raw)
				}
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(raw, &fields)
				for _, k := range []string{"page", "acknowledgment", "cursor", "generation"} {
					if fields[k] != nil {
						t.Fatal("error exposed stale progress", k)
					}
				}
			}
		})
	}
}

func TestActivityConsumerWatchCancellationAndDrain(t *testing.T) {
	for _, mode := range []string{"cancel", "drain", "parent_deadline", "watch_deadline"} {
		t.Run(mode, func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			opts, _, _, _ = consumerOptions(t, opts)
			opts.Lifecycle = NewLifecycle()
			var armed atomic.Bool
			entered, released := make(chan struct{}), make(chan struct{})
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/activity") && armed.CompareAndSwap(true, false) {
					close(entered)
					<-r.Context().Done()
					close(released)
					return
				}
				base.Config.Handler.ServeHTTP(w, r)
			}))
			defer backend.Close()
			opts.APIBase = backend.URL
			cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
			callCtx, cancel := context.WithCancel(ctx)
			if mode == "parent_deadline" {
				cancel()
				callCtx, cancel = context.WithTimeout(ctx, 300*time.Millisecond)
			}
			defer cancel()
			armed.Store(true)
			type answer struct {
				r *mcp.CallToolResult
				e error
			}
			done := make(chan answer, 1)
			wait := 3
			if mode == "watch_deadline" {
				wait = 1
			}
			go func() {
				r, e := cs.CallTool(callCtx, &mcp.CallToolParams{Name: watchActivityConsumerTool, Arguments: consumerWatchArgs("worker", 1, wait)})
				done <- answer{r, e}
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("native request did not start")
			}
			if mode == "cancel" {
				cancel()
			}
			if mode == "drain" {
				opts.Lifecycle.BeginDrain()
			}
			select {
			case out := <-done:
				if out.e != nil {
					if !errors.Is(out.e, context.Canceled) && !errors.Is(out.e, context.DeadlineExceeded) {
						t.Fatal(out.e)
					}
				} else if out.r == nil || !out.r.IsError {
					t.Fatal("cancellation became successful timeout", out.r)
				}
			case <-ctx.Done():
				t.Fatal("watch did not cancel")
			}
			select {
			case <-released:
			case <-ctx.Done():
				t.Fatal("native request was not cancelled")
			}
			var records map[string]activityConsumerRecord
			if err := opts.planStore.read("activity-consumers/"+opts.planNamespace, &records); err != nil || len(records) != 1 {
				t.Fatal("cancellation changed progress", err)
			}
			for _, r := range records {
				if r.Revision != 1 || r.Cursor != activityCursor(t, f, 0) {
					t.Fatal(r)
				}
			}
		})
	}
}

func TestActivityConsumerWatchValidation(t *testing.T) {
	_, backend, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	for _, change := range []string{"limit", "negative_limit", "wait", "negative_wait", "name"} {
		args := consumerWatchArgs("worker", 1, 1)
		switch change {
		case "limit":
			args["limit"] = 51
		case "negative_limit":
			args["limit"] = -1
		case "wait":
			args["wait_seconds"] = 46
		case "negative_wait":
			args["wait_seconds"] = -1
		case "name":
			args["consumer"] = "../worker"
		}
		out := callTool(t, ctx, cs, watchActivityConsumerTool, args, true)
		if string(out["code"]) != `"invalid_argument"` {
			t.Fatal(out)
		}
	}
	opts.planStore = nil
	cs, ctx = connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	out := callTool(t, ctx, cs, watchActivityConsumerTool, consumerWatchArgs("worker", 1, 1), true)
	if string(out["code"]) != `"durable_state_required"` {
		t.Fatal(out)
	}
}
