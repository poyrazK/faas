package mcpmanagement

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func consumerArgs(name string) map[string]any {
	return map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "consumer": name}
}

func consumerAck(name, cursor string, revision int64, rebuilt bool) map[string]any {
	a := consumerArgs(name)
	a["cursor"], a["expected_revision"], a["baseline_rebuilt"] = cursor, revision, rebuilt
	return a
}

func decodeConsumer(t *testing.T, out map[string]json.RawMessage) activityConsumerResult {
	t.Helper()
	raw, _ := json.Marshal(out)
	var result activityConsumerResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func consumerOptions(t *testing.T, opts Options) (Options, *StateStore, string, string) {
	t.Helper()
	store, path, key := testStateStore(t)
	opts.planStore, opts.planNamespace, opts.AllowWrites = store, "consumer-fixture", false
	g := opts.Environments.Named["shop/staging"]
	g.Write = false
	opts.Environments.Named["shop/staging"] = g
	return opts, store, path, key
}

func activityCursor(t *testing.T, f *environmentCacheFixture, sequence int64) string {
	t.Helper()
	return api.EncodeEnvironmentCacheActivityCursor(api.EnvironmentCacheActivityCursor{AccountID: f.account.ID, AppID: f.app.ID, ProjectID: f.project.ID, EnvironmentID: f.env.ID, Sequence: sequence})
}

func TestActivityConsumerExplicitAckAndRestart(t *testing.T) {
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
		if tool.Name == ackActivityConsumerTool && (tool.Annotations.ReadOnlyHint || *tool.Annotations.DestructiveHint || !tool.Annotations.IdempotentHint) {
			t.Fatal("ack has incorrect effects")
		}
	}
	got := decodeConsumer(t, callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("worker"), false))
	if got.Status != "uninitialized" || got.Revision != 0 || got.Cursor != "" {
		t.Fatal(got)
	}
	page := decodeActivity(t, callTool(t, ctx, cs, "gregale_get_environment_cache_schedule_activity", activityArgs("", 1), false))
	if len(page.Entries) != 1 || !page.HasMore {
		t.Fatal(page)
	}
	// Merely reading or waiting never acknowledges processing.
	got = decodeConsumer(t, callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("worker"), false))
	if got.Revision != 0 {
		t.Fatal("read advanced checkpoint")
	}
	got = decodeConsumer(t, callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", page.Cursor, 0, false), false))
	if got.Revision != 1 || got.Cursor != page.Cursor || got.UpdatedAt == nil || got.Status != "ready" {
		t.Fatal(got)
	}
	// Lost-response retries and current-revision no-ops do not create versions.
	for _, revision := range []int64{0, 1} {
		retry := decodeConsumer(t, callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", page.Cursor, revision, false), false))
		if retry.Revision != got.Revision || !retry.UpdatedAt.Equal(*got.UpdatedAt) {
			t.Fatal("retry changed progress")
		}
	}
	ciphertext, err := os.ReadFile(path)
	if err != nil || bytes.Contains(ciphertext, []byte(page.Cursor)) || bytes.Contains(ciphertext, []byte("worker")) {
		t.Fatal("checkpoint not encrypted", err)
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
	got = decodeConsumer(t, callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("worker"), false))
	if got.Revision != 1 || got.Cursor != page.Cursor {
		t.Fatal("restart lost progress", got)
	}
	remaining := decodeActivity(t, callTool(t, ctx, cs, "gregale_watch_environment_cache_schedule_activity", map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "after": got.Cursor, "limit": 1, "wait_seconds": 1}, false))
	if len(remaining.Entries) != 1 || remaining.Entries[0].Sequence != 2 {
		t.Fatal("resume skipped an unacknowledged event", remaining)
	}
	still := decodeConsumer(t, callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("worker"), false))
	if still.Cursor != got.Cursor {
		t.Fatal("watch advanced durable checkpoint")
	}
	if f.writes != 0 {
		t.Fatal("read-consented ack wrote platform intent")
	}
}

func TestActivityConsumerCASAcrossManagersAndStaleRetry(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	for i := 1; i <= 3; i++ {
		seedSchedule(t, f, time.Now().Add(time.Duration(i)*time.Hour))
	}
	a, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	b, _ := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	var wg sync.WaitGroup
	results := make(chan *mcp.CallToolResult, 2)
	errors := make(chan error, 2)
	for i, client := range []*mcp.ClientSession{a, b} {
		wg.Add(1)
		go func(i int, client *mcp.ClientSession) {
			defer wg.Done()
			r, err := client.CallTool(ctx, &mcp.CallToolParams{Name: ackActivityConsumerTool, Arguments: consumerAck("shared", activityCursor(t, f, int64(i+1)), 0, false)})
			if err != nil {
				errors <- err
				return
			}
			results <- r
		}(i, client)
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
	won, conflicts := 0, 0
	for r := range results {
		if r.IsError {
			raw, _ := json.Marshal(r.StructuredContent)
			if !strings.Contains(string(raw), "checkpoint_conflict") {
				t.Fatal(string(raw))
			}
			conflicts++
		} else {
			won++
		}
	}
	if won != 1 || conflicts != 1 {
		t.Fatalf("winners=%d conflicts=%d", won, conflicts)
	}
	current := decodeConsumer(t, callTool(t, ctx, a, getActivityConsumerTool, consumerArgs("shared"), false))
	if current.Revision != 1 {
		t.Fatal(current)
	}
	last := activityCursor(t, f, 3)
	callTool(t, ctx, a, ackActivityConsumerTool, consumerAck("shared", last, 1, false), false)
	callTool(t, ctx, b, ackActivityConsumerTool, consumerAck("shared", current.Cursor, 0, false), true)
	regress := callTool(t, ctx, b, ackActivityConsumerTool, consumerAck("shared", activityCursor(t, f, 1), 2, false), true)
	if string(regress["code"]) != `"checkpoint_regression"` {
		t.Fatal(regress)
	}
	independent := decodeConsumer(t, callTool(t, ctx, a, getActivityConsumerTool, consumerArgs("another"), false))
	if independent.Revision != 0 {
		t.Fatal("consumers share progress")
	}
	if f.writes != 0 {
		t.Fatal("consumer CAS mutated platform")
	}
}

func TestActivityConsumerExpiryRequiresExplicitBaseline(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	seedSchedule(t, f, time.Now().Add(time.Hour))
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	if err := f.store.PruneEnvironmentCacheScheduleActivity(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	expired := decodeConsumer(t, callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("worker"), false))
	if expired.Status != "cursor_expired" || expired.Revision != 1 || expired.ActivityRecovery == nil || expired.Cursor != activityCursor(t, f, 0) {
		t.Fatal(expired)
	}
	baseline := decodeBaseline(t, callTool(t, ctx, cs, recoverActivityTool, recoveryArgs("", 10), false))
	if !baseline.Complete {
		t.Fatal(baseline)
	}
	denied := callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", baseline.ActivityCursor, 1, false), true)
	if string(denied["code"]) != `"cursor_expired"` {
		t.Fatal(denied)
	}
	reset := decodeConsumer(t, callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", baseline.ActivityCursor, 1, true), false))
	if reset.Status != "ready" || reset.Revision != 2 {
		t.Fatal(reset)
	}
	// A fresh baseline can itself expire before acknowledgment. Never commit it.
	seedSchedule(t, f, time.Now().Add(2*time.Hour))
	if err := f.store.PruneEnvironmentCacheScheduleActivity(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", baseline.ActivityCursor, 2, true), true)
	still := decodeConsumer(t, callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("worker"), false))
	if still.Revision != 2 || still.Status != "cursor_expired" {
		t.Fatal("failed rebase advanced state", still)
	}
}

func TestActivityConsumerScopeAndStorageFailures(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, store, path, _ := consumerOptions(t, opts)
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	for _, name := range []string{"", "../x", "Upper", strings.Repeat("a", 65)} {
		callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs(name), true)
	}
	for _, cursor := range []string{"", "forged", activityCursor(t, f, 1), api.EncodeEnvironmentCacheActivityCursor(api.EnvironmentCacheActivityCursor{AccountID: "foreign", AppID: f.app.ID, ProjectID: f.project.ID, EnvironmentID: f.env.ID})} {
		callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", cursor, 0, false), true)
	}
	for _, rev := range []int64{-1, api.RouteRequirementsMaxRevision} {
		callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), rev, false), true)
	}
	other := opts
	other.planNamespace = "other-grant"
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	second, otherctx := connectConfiguredTestServer(t, backend, other, "2026-07-28")
	if got := decodeConsumer(t, callTool(t, otherctx, second, getActivityConsumerTool, consumerArgs("worker"), false)); got.Revision != 0 {
		t.Fatal("grant progress leaked")
	}
	ephemeral := opts
	ephemeral.planStore = nil
	nostore, nctx := connectConfiguredTestServer(t, backend, ephemeral, "2026-07-28")
	out := callTool(t, nctx, nostore, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), true)
	if string(out["code"]) != `"durable_state_required"` {
		t.Fatal(out)
	}
	seedSchedule(t, f, time.Now().Add(time.Hour))
	// Deterministically fail rename without relying on Unix user permissions.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "block"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	out = callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 1), 1, false), true)
	if string(out["code"]) != `"durable_state_unavailable"` || out["cursor"] != nil {
		t.Fatal("failed commit reported success", out)
	}
	callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("worker"), true)
	if store.available() || f.writes != 0 {
		t.Fatal("storage failure did not disable access")
	}
}

func TestActivityConsumerRevocationDuringAck(t *testing.T) {
	f, base, opts := newEnvironmentScheduleFixture(t)
	opts, store, _, _ := consumerOptions(t, opts)
	opts.grantID, opts.accountID, opts.planNamespace = "grant", f.account.ID, "grant"
	snapshot := oauthSnapshot{Grants: map[string]storedGrant{"grant": {ID: "grant", Active: true, AccountID: f.account.ID, Scopes: []string{scopeRead}, Expires: time.Now().Add(time.Hour), Environments: opts.Environments}}}
	if err := store.update("oauth", snapshot); err != nil {
		t.Fatal(err)
	}
	var revoke atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base.Config.Handler.ServeHTTP(w, r)
		if strings.HasSuffix(r.URL.Path, "/activity") && revoke.CompareAndSwap(true, false) {
			if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}); err != nil {
				t.Error(err)
			}
		}
	}))
	defer backend.Close()
	opts.APIBase = backend.URL
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	seedSchedule(t, f, time.Now().Add(time.Hour))
	revoke.Store(true)
	out := callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 1), 1, false), true)
	if string(out["code"]) != `"mcp_connection_revoked"` {
		t.Fatal(out)
	}
	store.mu.Lock()
	_, exists := store.records["activity-consumers/grant"]
	store.mu.Unlock()
	if exists {
		t.Fatal("in-flight ack resurrected revoked state")
	}
}

func TestActivityConsumerNativeAccessLossAndCancellation(t *testing.T) {
	f, base, opts := newEnvironmentScheduleFixture(t)
	opts, store, _, _ := consumerOptions(t, opts)
	var deny atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if deny.Load() && strings.HasSuffix(r.URL.Path, "/activity") {
			api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Denied", "Denied"))
			return
		}
		base.Config.Handler.ServeHTTP(w, r)
	}))
	defer backend.Close()
	opts.APIBase = backend.URL
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	deny.Store(true)
	for _, tool := range []string{getActivityConsumerTool, ackActivityConsumerTool} {
		args := consumerArgs("worker")
		if tool == ackActivityConsumerTool {
			args = consumerAck("worker", activityCursor(t, f, 0), 1, false)
		}
		out := callTool(t, ctx, cs, tool, args, true)
		if string(out["code"]) != `"insufficient_scope"` {
			t.Fatal(out)
		}
	}
	_, manager, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	bound := activityConsumerBinding{Endpoint: backend.URL, Account: f.account.ID, App: f.app.ID, Project: f.project.ID, Environment: f.env.ID, Scope: "staging", Consumer: "cancelled"}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = manager.consumerRecord(cancelled, activityConsumerArgs{}, bound, &activityConsumerAckArgs{Cursor: activityCursor(t, f, 0)}); err != context.Canceled {
		t.Fatal("cancelled ack committed", err)
	}
	var records map[string]activityConsumerRecord
	if err = store.read("activity-consumers/"+opts.planNamespace, &records); err != nil || len(records) != 1 {
		t.Fatal("denied/cancelled ack changed journal", err)
	}
}

func TestActivityConsumerConsentAndRecreatedTarget(t *testing.T) {
	f, base, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	var recreated atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if recreated.Load() && r.URL.Path == "/v1/projects/shop/environments/staging" {
			_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentResponse{ID: "replacement", ProjectID: f.project.ID, Slug: "staging"})
			return
		}
		base.Config.Handler.ServeHTTP(w, r)
	}))
	defer backend.Close()
	opts.APIBase = backend.URL
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	recreated.Store(true)
	out := callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("worker"), true)
	if string(out["code"]) != `"environment_access_denied"` || out["cursor"] != nil {
		t.Fatal("recreated environment inherited checkpoint", out)
	}
	recreated.Store(false)
	delete(opts.Environments.Named, "shop/staging")
	denied, dctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	for _, tool := range []string{getActivityConsumerTool, ackActivityConsumerTool} {
		args := consumerArgs("worker")
		if tool == ackActivityConsumerTool {
			args = consumerAck("worker", activityCursor(t, f, 0), 1, false)
		}
		out = callTool(t, dctx, denied, tool, args, true)
		if string(out["code"]) != `"environment_access_denied"` || out["cursor"] != nil {
			t.Fatal("missing consent exposed checkpoint", out)
		}
	}
}

func TestActivityConsumerIdentityIsolationAndCapacity(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, store, _, _ := consumerOptions(t, opts)
	_, manager, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !manager.toolWrites[ackActivityConsumerTool] || manager.toolWrites[getActivityConsumerTool] {
		t.Fatal("local mutation has wrong drain behavior")
	}
	original := activityConsumerBinding{Endpoint: backend.URL, Account: f.account.ID, App: f.app.ID, Project: f.project.ID, Environment: f.env.ID, Scope: "staging", Consumer: "worker"}
	makeCursor := func(b activityConsumerBinding) string {
		return api.EncodeEnvironmentCacheActivityCursor(api.EnvironmentCacheActivityCursor{AccountID: b.Account, AppID: b.App, ProjectID: b.Project, EnvironmentID: b.Environment})
	}
	ack := activityConsumerAckArgs{Cursor: makeCursor(original)}
	if _, err = manager.consumerRecord(context.Background(), activityConsumerArgs{}, original, &ack); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"endpoint", "account", "app", "project", "environment", "scope", "consumer"} {
		b := original
		switch field {
		case "endpoint":
			b.Endpoint = "http://another.example"
		case "account":
			b.Account = "another"
		case "app":
			b.App = "another"
		case "project":
			b.Project = "another"
		case "environment":
			b.Environment = "another"
		case "scope":
			b.Scope = "production"
		case "consumer":
			b.Consumer = "another"
		}
		record, readErr := manager.consumerRecord(context.Background(), activityConsumerArgs{}, b, nil)
		if readErr != nil || record.Revision != 0 {
			t.Fatal("identity inherited another checkpoint", field, record, readErr)
		}
	}
	for i := 1; i < api.MCPActivityConsumersPerConnection; i++ {
		b := original
		b.Consumer = fmt.Sprintf("worker-%d", i)
		proposal := activityConsumerAckArgs{Cursor: makeCursor(b)}
		if _, err = manager.consumerRecord(context.Background(), activityConsumerArgs{}, b, &proposal); err != nil {
			t.Fatal(err)
		}
	}
	b := original
	b.Consumer = "one-too-many"
	_, err = manager.consumerRecord(context.Background(), activityConsumerArgs{}, b, &ack)
	if err == nil || manager.toolError(err).StructuredContent.(ToolError).Code != "consumer_capacity" {
		t.Fatal("consumer capacity not enforced", err)
	}
	if !store.available() {
		t.Fatal("capacity error disabled a healthy journal")
	}
	// Existing consumers remain available when capacity is full.
	if record, readErr := manager.consumerRecord(context.Background(), activityConsumerArgs{}, original, &ack); readErr != nil || record.Revision != 1 {
		t.Fatal(record, readErr)
	}
}

func TestActivityConsumerFullJournalPreservesExistingState(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, store, path, _ := consumerOptions(t, opts)
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Represent other journal namespaces consuming the shared byte budget.
	store.mu.Lock()
	store.records["capacity-fixture"] = json.RawMessage(`"` + strings.Repeat("x", maxStateBytes-10) + `"`)
	store.mu.Unlock()
	out := callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("another", activityCursor(t, f, 0), 0, false), true)
	if string(out["code"]) != `"consumer_capacity"` || !store.available() {
		t.Fatal("capacity rejection disabled journal", out)
	}
	got := decodeConsumer(t, callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("worker"), false))
	if got.Revision != 1 || got.Cursor != activityCursor(t, f, 0) {
		t.Fatal("capacity rejection changed existing consumer", got)
	}
	missing := decodeConsumer(t, callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("another"), false))
	if missing.Revision != 0 {
		t.Fatal("rejected consumer was created")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("capacity rejection touched disk", err)
	}
	store.mu.Lock()
	delete(store.records, "capacity-fixture")
	store.mu.Unlock()
}
