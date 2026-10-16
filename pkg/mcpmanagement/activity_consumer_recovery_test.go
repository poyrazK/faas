package mcpmanagement

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func consumerRecoveryArgs(name, token string, limit int) map[string]any {
	args := consumerArgs(name)
	args["recovery_token"], args["limit"] = token, limit
	return args
}
func decodeConsumerRecovery(t *testing.T, out map[string]json.RawMessage) activityConsumerRecoveryResult {
	t.Helper()
	raw, _ := json.Marshal(out)
	var page activityConsumerRecoveryResult
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	return page
}
func encodeConsumerRecoveryToken(t *testing.T, token activityConsumerRecoveryToken) string {
	t.Helper()
	raw, err := json.Marshal(token)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
func noConsumerRecoveryProgress(t *testing.T, out map[string]json.RawMessage, code string) {
	t.Helper()
	if string(out["code"]) != `"`+code+`"` {
		t.Fatal(out)
	}
	for _, key := range []string{"entries", "recovery_token", "activity_cursor", "acknowledgment", "generation"} {
		if out[key] != nil {
			t.Fatalf("failure exposed %s", key)
		}
	}
}

func TestActivityConsumerRecoveryRestartAndReplay(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, store, path, key := consumerOptions(t, opts)
	seedSchedule(t, f, time.Now().Add(time.Hour))
	seedSchedule(t, f, time.Now().Add(2*time.Hour))
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	catalog, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range catalog.Tools {
		if tool.Name == recoverActivityConsumerTool && (!tool.Annotations.ReadOnlyHint || *tool.Annotations.DestructiveHint) {
			t.Fatal("recovery advertised mutation")
		}
	}
	first := decodeConsumerRecovery(t, callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("worker", "", 1), false))
	if first.Complete || first.RecoveryToken == "" || len(first.Entries) != 1 || first.Acknowledgment != nil || first.ActivityCursor != "" || first.Revision != 0 || first.Generation != "" {
		t.Fatal(first)
	}
	token, err := decodeConsumerRecoveryToken(first.RecoveryToken)
	if err != nil || token.Recovery.Checkpoint != activityCursor(t, f, 2) {
		t.Fatal(token, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("recovery created progress", err)
	}
	cs.Close()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := OpenStateStore(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	opts.planStore = next
	// This event sorts before the baseline continuation. The original checkpoint
	// must replay it after baseline processing, even though listing misses it.
	added := seedSchedule(t, f, time.Now().Add(30*time.Minute))
	cs, ctx = connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	last := decodeConsumerRecovery(t, callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("worker", first.RecoveryToken, 2), false))
	if !last.Complete || last.RecoveryToken != "" || len(last.Entries) != 1 || last.ActivityCursor != token.Recovery.Checkpoint || last.Acknowledgment == nil {
		t.Fatal(last)
	}
	if last.Acknowledgment.ExpectedRevision != 0 || last.Acknowledgment.ExpectedGeneration != "" || last.Acknowledgment.BaselineRebuilt || last.Acknowledgment.Cursor != last.ActivityCursor {
		t.Fatal(last.Acknowledgment)
	}
	current := decodeConsumer(t, callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("worker"), false))
	if current.Revision != 0 {
		t.Fatal("baseline recovery acknowledged implicitly")
	}
	retry := decodeConsumerRecovery(t, callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("worker", first.RecoveryToken, 2), false))
	if !reflect.DeepEqual(last, retry) {
		t.Fatal("unchanged retry changed baseline")
	}
	callTool(t, ctx, cs, ackActivityConsumerTool, last.Acknowledgment, false)
	resumed := decodeConsumerWatch(t, callTool(t, ctx, cs, watchActivityConsumerTool, consumerWatchArgs("worker", 10, 1), false))
	if resumed.Page == nil || len(resumed.Page.Entries) != 1 || resumed.Page.Entries[0].ScheduleID != added.ScheduleID {
		t.Fatal("recovery skipped earlier-sorting concurrent event", resumed)
	}
	if f.writes != 0 {
		t.Fatal("recovery sent native writes")
	}
}

func TestActivityConsumerRecoveryExpiredCheckpointAndOvertakenBaseline(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	seedSchedule(t, f, time.Now().Add(time.Hour))
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	if err := f.store.PruneEnvironmentCacheScheduleActivity(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	seedSchedule(t, f, time.Now().Add(2*time.Hour))
	first := decodeConsumerRecovery(t, callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("worker", "", 1), false))
	if first.Complete || first.Revision != 1 || first.Acknowledgment != nil {
		t.Fatal(first)
	}
	seedSchedule(t, f, time.Now().Add(3*time.Hour))
	if err := f.store.PruneEnvironmentCacheScheduleActivity(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	out := callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("worker", first.RecoveryToken, 1), true)
	noConsumerRecoveryProgress(t, out, "recovery_restart_required")
	if out["activity_recovery"] == nil {
		t.Fatal("missing validated restart guidance")
	}
	unchanged := decodeConsumer(t, callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("worker"), false))
	if unchanged.Revision != 1 || unchanged.Cursor != activityCursor(t, f, 0) || unchanged.Status != "cursor_expired" {
		t.Fatal(unchanged)
	}
	rebuilt := decodeConsumerRecovery(t, callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("worker", "", 50), false))
	if !rebuilt.Complete || rebuilt.Acknowledgment == nil || !rebuilt.Acknowledgment.BaselineRebuilt || rebuilt.Acknowledgment.ExpectedRevision != 1 {
		t.Fatal(rebuilt)
	}
	saved := decodeConsumer(t, callTool(t, ctx, cs, ackActivityConsumerTool, rebuilt.Acknowledgment, false))
	if saved.Revision != 2 || saved.Status != "ready" || saved.Cursor != activityCursor(t, f, 3) {
		t.Fatal(saved)
	}
}

func TestActivityConsumerRecoverySavedCursorExpiresDuringBaseline(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	seedSchedule(t, f, time.Now().Add(time.Hour))
	cutoff := time.Now()
	seedSchedule(t, f, time.Now().Add(2*time.Hour))
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	first := decodeConsumerRecovery(t, callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("worker", "", 1), false))
	if first.Complete {
		t.Fatal("expected a paginated baseline")
	}
	if err := f.store.PruneEnvironmentCacheScheduleActivity(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	expired := decodeConsumer(t, callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("worker"), false))
	if expired.Status != "cursor_expired" || expired.Revision != 1 {
		t.Fatal(expired)
	}
	complete := decodeConsumerRecovery(t, callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("worker", first.RecoveryToken, 1), false))
	if !complete.Complete || complete.Acknowledgment == nil || !complete.Acknowledgment.BaselineRebuilt || complete.ActivityCursor != activityCursor(t, f, 2) {
		t.Fatal(complete)
	}
	saved := decodeConsumer(t, callTool(t, ctx, cs, ackActivityConsumerTool, complete.Acknowledgment, false))
	if saved.Status != "ready" || saved.Revision != 2 || saved.Cursor != complete.ActivityCursor {
		t.Fatal(saved)
	}
}

func TestActivityConsumerRecoveryGuardsBetweenPages(t *testing.T) {
	for _, retire := range []bool{false, true} {
		t.Run(map[bool]string{false: "ack", true: "retire"}[retire], func(t *testing.T) {
			f, backend, opts := newEnvironmentScheduleFixture(t)
			opts, _, _, _ = consumerOptions(t, opts)
			seedSchedule(t, f, time.Now().Add(time.Hour))
			seedSchedule(t, f, time.Now().Add(2*time.Hour))
			cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
			first := decodeConsumerRecovery(t, callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("worker", "", 1), false))
			code := "checkpoint_conflict"
			if retire {
				callTool(t, ctx, cs, retireActivityConsumerTool, consumerRetirement("worker", 1, ""), false)
				code = "consumer_generation_conflict"
			} else {
				callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 1), 1, false), false)
			}
			noConsumerRecoveryProgress(t, callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("worker", first.RecoveryToken, 1), true), code)
		})
	}
}

func TestActivityConsumerRecoveryRejectsForeignAndMalformedTokens(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	seedSchedule(t, f, time.Now().Add(time.Hour))
	seedSchedule(t, f, time.Now().Add(2*time.Hour))
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	first := decodeConsumerRecovery(t, callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("worker", "", 1), false))
	original, err := decodeConsumerRecoveryToken(first.RecoveryToken)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"binding", "revision", "generation", "endpoint", "account", "app", "project", "environment", "scope", "checkpoint", "ahead", "after", "version", "inner_version"} {
		token := original
		code := "invalid_argument"
		switch field {
		case "binding":
			token.Binding = strings.Repeat("x", 64)
		case "revision":
			token.Revision = 1
			code = "checkpoint_conflict"
		case "generation":
			token.Generation = "cg_" + strings.Repeat("a", 43)
			code = "consumer_generation_conflict"
		case "endpoint":
			token.Recovery.Endpoint = "https://other.example"
		case "account":
			token.Recovery.Account = "other"
		case "app":
			token.Recovery.App = "other"
		case "project":
			token.Recovery.Project = "other"
		case "environment":
			token.Recovery.Environment = "other"
		case "scope":
			token.Recovery.Scope = "other"
		case "checkpoint":
			token.Recovery.Checkpoint = "forged"
		case "ahead":
			token.Recovery.Checkpoint = activityCursor(t, f, 3)
			code = "conflict"
		case "after":
			token.Recovery.After = "forged"
		case "version":
			token.Version = 2
		case "inner_version":
			token.Recovery.Version = 2
		}
		noConsumerRecoveryProgress(t, callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("worker", encodeConsumerRecoveryToken(t, token), 1), true), code)
	}
	noConsumerRecoveryProgress(t, callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("other", first.RecoveryToken, 1), true), "invalid_argument")
	other := opts
	other.planNamespace = "different-connection"
	otherClient, otherCtx := connectConfiguredTestServer(t, backend, other, "2026-07-28")
	noConsumerRecoveryProgress(t, callTool(t, otherCtx, otherClient, recoverActivityConsumerTool, consumerRecoveryArgs("worker", first.RecoveryToken, 1), true), "invalid_argument")
	raw := decodeBaseline(t, callTool(t, ctx, cs, recoverActivityTool, recoveryArgs("", 1), false))
	bad := []string{"forged", raw.RecoveryToken, strings.Repeat("x", api.EnvironmentCacheActivityRecoveryTokenMaxBytes+1), base64.RawURLEncoding.EncodeToString([]byte(`{} {}`))}
	data, _ := json.Marshal(original)
	bad = append(bad, base64.RawURLEncoding.EncodeToString(append(data[:len(data)-1], []byte(`,"unknown":true}`)...)))
	for _, token := range bad {
		noConsumerRecoveryProgress(t, callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("worker", token, 1), true), "invalid_argument")
	}
	for _, limit := range []int{-1, 51} {
		noConsumerRecoveryProgress(t, callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("worker", "", limit), true), "invalid_argument")
	}
}

func TestActivityConsumerRecoveryChangesDuringPage(t *testing.T) {
	for _, mode := range []string{"ack", "retire", "revoke", "deny", "retention", "cancel", "drain"} {
		t.Run(mode, func(t *testing.T) {
			f, base, opts := newEnvironmentScheduleFixture(t)
			opts, store, _, _ := consumerOptions(t, opts)
			opts.Lifecycle = NewLifecycle()
			if mode == "revoke" {
				opts.grantID, opts.accountID, opts.planNamespace = "grant", f.account.ID, "grant"
				snapshot := oauthSnapshot{Grants: map[string]storedGrant{"grant": {ID: "grant", AccountID: f.account.ID, Active: true, Scopes: []string{scopeRead}, Expires: time.Now().Add(time.Hour), Environments: opts.Environments}}}
				if err := store.update("oauth", snapshot); err != nil {
					t.Fatal(err)
				}
			}
			var armed, deny atomic.Bool
			entered, release := make(chan struct{}), make(chan struct{})
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if deny.Load() && strings.HasSuffix(r.URL.Path, "/activity") {
					api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Denied", "Denied"))
					return
				}
				if r.URL.Path == "/v1/apps/alpha/cache/schedules" && armed.CompareAndSwap(true, false) {
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
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
			a, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			b, _ := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
			callTool(t, ctx, a, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
			seedSchedule(t, f, time.Now().Add(time.Hour))
			seedSchedule(t, f, time.Now().Add(2*time.Hour))
			armed.Store(true)
			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			type answer struct {
				r *mcp.CallToolResult
				e error
			}
			done := make(chan answer, 1)
			go func() {
				r, e := a.CallTool(callCtx, &mcp.CallToolParams{Name: recoverActivityConsumerTool, Arguments: consumerRecoveryArgs("worker", "", 1)})
				done <- answer{r, e}
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("baseline did not reach list")
			}
			code := ""
			switch mode {
			case "ack":
				callTool(t, ctx, b, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 1), 1, false), false)
				code = "checkpoint_conflict"
			case "retire":
				callTool(t, ctx, b, retireActivityConsumerTool, consumerRetirement("worker", 1, ""), false)
				code = "consumer_generation_conflict"
			case "revoke":
				if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}); err != nil {
					t.Fatal(err)
				}
				code = "mcp_connection_revoked"
			case "deny":
				deny.Store(true)
				code = "insufficient_scope"
			case "retention":
				seedSchedule(t, f, time.Now().Add(3*time.Hour))
				if err := f.store.PruneEnvironmentCacheScheduleActivity(ctx, time.Now().Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
				code = "recovery_restart_required"
			case "cancel":
				cancel()
			case "drain":
				opts.Lifecycle.BeginDrain()
				code = "cancelled"
			}
			close(release)
			select {
			case out := <-done:
				if mode == "cancel" {
					if out.e == nil && (out.r == nil || !out.r.IsError) {
						t.Fatal("cancelled read succeeded")
					}
					return
				}
				if out.e != nil {
					t.Fatal(out.e)
				}
				if out.r == nil || !out.r.IsError {
					t.Fatal("stale page returned", out.r)
				}
				raw, _ := json.Marshal(out.r.StructuredContent)
				var fields map[string]json.RawMessage
				_ = json.Unmarshal(raw, &fields)
				noConsumerRecoveryProgress(t, fields, code)
			case <-ctx.Done():
				t.Fatal("baseline did not finish")
			}
		})
	}
}

func TestActivityConsumerRecoveryEmptyAndStateRequired(t *testing.T) {
	_, backend, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	page := decodeConsumerRecovery(t, callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("worker", "", 0), false))
	if !page.Complete || page.Entries == nil || len(page.Entries) != 0 || page.Acknowledgment == nil || page.Revision != 0 {
		t.Fatal(page)
	}
	opts.planStore = nil
	cs, ctx = connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	noConsumerRecoveryProgress(t, callTool(t, ctx, cs, recoverActivityConsumerTool, consumerRecoveryArgs("worker", "", 1), true), "durable_state_required")
}
