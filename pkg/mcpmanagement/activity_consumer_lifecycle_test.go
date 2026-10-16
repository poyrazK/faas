package mcpmanagement

import (
	"encoding/base64"
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

func consumerRetirement(name string, revision int64, generation string) map[string]any {
	args := consumerArgs(name)
	args["expected_revision"] = revision
	args["expected_generation"] = generation
	return args
}

func ackInGeneration(name, cursor string, revision int64, generation string) map[string]any {
	args := consumerAck(name, cursor, revision, false)
	args["expected_generation"] = generation
	return args
}

func consumerListArgs(after string, limit int) map[string]any {
	return map[string]any{"app": "alpha", "project": "shop", "environment": "staging", "after": after, "limit": limit}
}

func decodeConsumerList(t *testing.T, out map[string]json.RawMessage) activityConsumerListResult {
	t.Helper()
	raw, _ := json.Marshal(out)
	var result activityConsumerListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestActivityConsumerListScopeAndPagination(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, store, _, _ := consumerOptions(t, opts)
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	catalog, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range catalog.Tools {
		if tool.Name == retireActivityConsumerTool && (tool.Annotations == nil || tool.Annotations.ReadOnlyHint || tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint || !tool.Annotations.IdempotentHint) {
			t.Fatal("retirement lacks destructive local mutation annotation")
		}
	}
	for _, name := range []string{"charlie", "alpha", "bravo"} {
		callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck(name, activityCursor(t, f, 0), 0, false), false)
	}
	_, manager, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	// One namespace can hold multiple targets. The public list must filter every
	// binding field, rather than expose all grant-local names.
	foreign := activityConsumerBinding{Endpoint: backend.URL, Account: "another-account", App: f.app.ID, Project: f.project.ID, Environment: f.env.ID, Scope: "staging", Consumer: "private"}
	cursor := api.EncodeEnvironmentCacheActivityCursor(api.EnvironmentCacheActivityCursor{AccountID: foreign.Account, AppID: foreign.App, ProjectID: foreign.Project, EnvironmentID: foreign.Environment})
	if _, err = manager.consumerRecord(ctx, activityConsumerArgs{}, foreign, &activityConsumerAckArgs{Cursor: cursor}); err != nil {
		t.Fatal(err)
	}
	first := decodeConsumerList(t, callTool(t, ctx, cs, listActivityConsumersTool, consumerListArgs("", 1), false))
	if len(first.Entries) != 1 || first.Entries[0].Consumer != "alpha" || first.NextCursor == "" || first.Generation != "" {
		t.Fatal(first)
	}
	last := decodeConsumerList(t, callTool(t, ctx, cs, listActivityConsumersTool, consumerListArgs(first.NextCursor, 2), false))
	if len(last.Entries) != 2 || last.Entries[0].Consumer != "bravo" || last.Entries[1].Consumer != "charlie" || last.NextCursor != "" {
		t.Fatal(last)
	}
	raw, _ := json.Marshal(last)
	if strings.Contains(string(raw), `"cursor":`) || strings.Contains(string(raw), "private") {
		t.Fatal("list exposed cursors or another target")
	}
	for _, limit := range []int{-1, 51} {
		callTool(t, ctx, cs, listActivityConsumersTool, consumerListArgs("", limit), true)
	}
	for _, after := range []string{"invalid", strings.Repeat("x", api.MCPActivityConsumerListCursorMaxBytes+1), base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"b":"` + strings.Repeat("a", 64) + `","a":"alpha","extra":true}`))} {
		callTool(t, ctx, cs, listActivityConsumersTool, consumerListArgs(after, 1), true)
	}
	other := opts
	other.planNamespace = "other-connection"
	otherClient, otherCtx := connectConfiguredTestServer(t, backend, other, "2026-07-28")
	callTool(t, otherCtx, otherClient, listActivityConsumersTool, consumerListArgs(first.NextCursor, 1), true)
	empty := decodeConsumerList(t, callTool(t, otherCtx, otherClient, listActivityConsumersTool, consumerListArgs("", 0), false))
	if empty.Entries == nil || len(empty.Entries) != 0 {
		t.Fatal(empty)
	}
	callTool(t, ctx, cs, retireActivityConsumerTool, consumerRetirement("alpha", 1, ""), false)
	out := callTool(t, ctx, cs, listActivityConsumersTool, consumerListArgs(first.NextCursor, 1), true)
	if string(out["code"]) != `"consumer_generation_conflict"` {
		t.Fatal("old list continuation survived retirement", out)
	}
	if !store.available() || f.writes != 0 {
		t.Fatal("listing/retirement affected platform or journal health")
	}
}

func TestActivityConsumerRetireRestartAndRecreate(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, store, path, key := consumerOptions(t, opts)
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	oldAck := consumerAck("worker", activityCursor(t, f, 0), 0, false)
	callTool(t, ctx, cs, ackActivityConsumerTool, oldAck, false)
	before, err := storeSize(store)
	if err != nil {
		t.Fatal(err)
	}
	oldRetire := consumerRetirement("worker", 1, "")
	retired := callTool(t, ctx, cs, retireActivityConsumerTool, oldRetire, false)
	var generation string
	if err = json.Unmarshal(retired["generation"], &generation); err != nil || generation == "" {
		t.Fatal(retired, err)
	}
	retry := callTool(t, ctx, cs, retireActivityConsumerTool, oldRetire, false)
	if string(retry["generation"]) != string(retired["generation"]) {
		t.Fatal("retirement retry rotated generation twice")
	}
	// An older generation-unaware bridge must not reopen this journal and
	// resurrect a consumer. v1 AAD authentication must now fail.
	encrypted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	nonceSize := store.aead.NonceSize()
	if _, err = store.aead.Open(nil, encrypted[:nonceSize], encrypted[nonceSize:], []byte(stateFormat)); err == nil {
		t.Fatal("retired journal still writable by legacy bridges")
	}
	after, err := storeSize(store)
	if err != nil || after >= before {
		t.Fatal("retirement failed to reclaim bytes", before, after, err)
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
	missing := decodeConsumer(t, callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("worker"), false))
	if missing.Revision != 0 || missing.Cursor != "" || missing.Generation != generation {
		t.Fatal("retirement fence lost on restart", missing)
	}
	for _, ack := range []map[string]any{oldAck, consumerAck("worker", activityCursor(t, f, 0), 1, false)} {
		out := callTool(t, ctx, cs, ackActivityConsumerTool, ack, true)
		if string(out["code"]) != `"consumer_generation_conflict"` {
			t.Fatal("stale ack resurrected consumer", out)
		}
	}
	fresh := decodeConsumer(t, callTool(t, ctx, cs, ackActivityConsumerTool, ackInGeneration("worker", activityCursor(t, f, 0), 0, generation), false))
	if fresh.Revision != 1 || fresh.Generation != generation {
		t.Fatal(fresh)
	}
	// Same name, revision and cursor as the retired incarnation: generation must
	// still reject the old initial ack and last retirement receipt.
	callTool(t, ctx, cs, ackActivityConsumerTool, oldAck, true)
	callTool(t, ctx, cs, retireActivityConsumerTool, oldRetire, true)
	got := decodeConsumer(t, callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("worker"), false))
	if got.Revision != 1 || got.Generation != generation {
		t.Fatal("old retirement deleted new incarnation", got)
	}
	if f.writes != 0 {
		t.Fatal("retirement mutated platform")
	}
}

func storeSize(store *StateStore) (int, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	raw, err := json.Marshal(store.records)
	return len(raw), err
}

func TestActivityConsumerRetirementFreesCapacityAndBoundsMetadata(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, store, _, _ := consumerOptions(t, opts)
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), opts)
	if err != nil {
		t.Fatal(err)
	}
	base := activityConsumerBinding{Endpoint: backend.URL, Account: f.account.ID, App: f.app.ID, Project: f.project.ID, Environment: f.env.ID, Scope: "staging", Consumer: "worker"}
	cursor := activityCursor(t, f, 0)
	for i := 0; i < api.MCPActivityConsumersPerConnection; i++ {
		b := base
		b.Consumer = fmt.Sprintf("worker-%03d", i)
		if _, err = m.consumerRecord(t.Context(), activityConsumerArgs{}, b, &activityConsumerAckArgs{Cursor: cursor}); err != nil {
			t.Fatal(err)
		}
	}
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("full", cursor, 0, false), true)
	retired := callTool(t, ctx, cs, retireActivityConsumerTool, consumerRetirement("worker-000", 1, ""), false)
	var generation string
	_ = json.Unmarshal(retired["generation"], &generation)
	callTool(t, ctx, cs, ackActivityConsumerTool, ackInGeneration("full", cursor, 0, generation), false)
	for i := 0; i < 10; i++ {
		out := callTool(t, ctx, cs, retireActivityConsumerTool, consumerRetirement("full", 1, generation), false)
		_ = json.Unmarshal(out["generation"], &generation)
		callTool(t, ctx, cs, ackActivityConsumerTool, ackInGeneration("full", cursor, 0, generation), false)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	var records map[string]activityConsumerRecord
	if json.Unmarshal(store.records["activity-consumers/"+opts.planNamespace], &records) != nil || len(records) != api.MCPActivityConsumersPerConnection {
		t.Fatal("capacity not reclaimed/reused")
	}
	var epoch activityConsumerEpoch
	if json.Unmarshal(store.records["activity-consumer-generations/"+opts.planNamespace], &epoch) != nil || epoch.Generation != generation || len(store.records) != 2 {
		t.Fatal("retirements grew unbounded metadata")
	}
}

func TestActivityConsumerRetireAckRaceAndExpiredCursor(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	seedSchedule(t, f, time.Now().Add(time.Hour))
	a, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	b, _ := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	callTool(t, ctx, a, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	var wg sync.WaitGroup
	results := make(chan *mcp.CallToolResult, 2)
	errs := make(chan error, 2)
	for i, client := range []*mcp.ClientSession{a, b} {
		wg.Add(1)
		go func(i int, client *mcp.ClientSession) {
			defer wg.Done()
			name := ackActivityConsumerTool
			args := consumerAck("worker", activityCursor(t, f, 1), 1, false)
			if i == 1 {
				name = retireActivityConsumerTool
				args = consumerRetirement("worker", 1, "")
			}
			result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
			if err != nil {
				errs <- err
			} else {
				results <- result
			}
		}(i, client)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	winners := 0
	for result := range results {
		if !result.IsError {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("ack/retire race had %d winners", winners)
	}
	current := decodeConsumer(t, callTool(t, ctx, a, getActivityConsumerTool, consumerArgs("worker"), false))
	// Create a different consumer at zero, let its cursor expire, and retire it
	// without requiring history or baseline reconstruction.
	callTool(t, ctx, a, ackActivityConsumerTool, ackInGeneration("expired", activityCursor(t, f, 0), 0, current.Generation), false)
	if err := f.store.PruneEnvironmentCacheScheduleActivity(ctx, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	expired := decodeConsumer(t, callTool(t, ctx, a, getActivityConsumerTool, consumerArgs("expired"), false))
	if expired.Status != "cursor_expired" {
		t.Fatal(expired)
	}
	callTool(t, ctx, a, retireActivityConsumerTool, consumerRetirement("expired", expired.Revision, expired.Generation), false)
}

func TestActivityConsumerGetDoesNotReturnRetiredProgress(t *testing.T) {
	f, base, opts := newEnvironmentScheduleFixture(t)
	opts, _, _, _ = consumerOptions(t, opts)
	seedSchedule(t, f, time.Now().Add(time.Hour))
	var block atomic.Bool
	entered := make(chan struct{})
	release := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/activity") && r.URL.Query().Get("after") == activityCursor(t, f, 0) && block.CompareAndSwap(true, false) {
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
	opts.APIBase = backend.URL
	a, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	b, _ := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	callTool(t, ctx, a, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	block.Store(true)
	result := make(chan *mcp.CallToolResult, 1)
	errs := make(chan error, 1)
	go func() {
		r, err := a.CallTool(ctx, &mcp.CallToolParams{Name: getActivityConsumerTool, Arguments: consumerArgs("worker")})
		if err != nil {
			errs <- err
		} else {
			result <- r
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("get never reached saved-cursor validation")
	}
	callTool(t, ctx, b, retireActivityConsumerTool, consumerRetirement("worker", 1, ""), false)
	close(release)
	select {
	case r := <-result:
		if !r.IsError {
			t.Fatal("get returned retired progress")
		}
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("get did not finish")
	}
}

func TestActivityConsumerOAuthCleanupPreservesLocalGeneration(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, store, _, _ := consumerOptions(t, opts)
	opts.planNamespace = "local-fixture"
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	var records map[string]activityConsumerRecord
	if err := store.read("activity-consumers/"+opts.planNamespace, &records); err != nil {
		t.Fatal(err)
	}
	if err := store.update("activity-consumers/remote-grant", records); err != nil {
		t.Fatal(err)
	}
	retired := callTool(t, ctx, cs, retireActivityConsumerTool, consumerRetirement("worker", 1, ""), false)
	var epoch activityConsumerEpoch
	if err := store.read("activity-consumer-generations/"+opts.planNamespace, &epoch); err != nil {
		t.Fatal(err)
	}
	if err := store.update("activity-consumer-generations/remote-grant", epoch); err != nil {
		t.Fatal(err)
	}
	if err := store.update("oauth", oauthSnapshot{Grants: map[string]storedGrant{}}); err != nil {
		t.Fatal(err)
	}
	got := decodeConsumer(t, callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("worker"), false))
	if string(retired["generation"]) != fmt.Sprintf("%q", got.Generation) || got.Generation == "" {
		t.Fatal("OAuth cleanup removed local generation fence")
	}
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), true)
	store.mu.Lock()
	_, hasRecords := store.records["activity-consumers/remote-grant"]
	_, hasGeneration := store.records["activity-consumer-generations/remote-grant"]
	format := store.format
	store.mu.Unlock()
	if hasRecords || hasGeneration || format != retirementStateFormat {
		t.Fatal("OAuth cleanup retained remote metadata or downgraded the journal")
	}
}

func TestActivityConsumerRetirementCommitFailureIsAtomic(t *testing.T) {
	f, backend, opts := newEnvironmentScheduleFixture(t)
	opts, store, path, key := consumerOptions(t, opts)
	cs, ctx := connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	callTool(t, ctx, cs, ackActivityConsumerTool, consumerAck("worker", activityCursor(t, f, 0), 0, false), false)
	blocked := path + ".blocked"
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "block"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.path = blocked
	store.mu.Unlock()
	out := callTool(t, ctx, cs, retireActivityConsumerTool, consumerRetirement("worker", 1, ""), true)
	if string(out["code"]) != `"durable_state_unavailable"` || store.available() {
		t.Fatal("failed retirement was confirmed", out)
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
	cs, ctx = connectConfiguredTestServer(t, backend, opts, "2026-07-28")
	got := decodeConsumer(t, callTool(t, ctx, cs, getActivityConsumerTool, consumerArgs("worker"), false))
	if got.Revision != 1 || got.Generation != "" || got.Cursor != activityCursor(t, f, 0) {
		t.Fatal("failed retirement partially changed disk state", got)
	}
	// Retrying the same retirement now atomically commits deletion and fence.
	callTool(t, ctx, cs, retireActivityConsumerTool, consumerRetirement("worker", 1, ""), false)
	if next.format != retirementStateFormat {
		t.Fatal("retirement failed to upgrade the journal format")
	}
}
