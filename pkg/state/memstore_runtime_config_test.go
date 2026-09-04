package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestMemStoreRuntimeConfigCRUDAndRevisionHistory(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()

	if _, err := store.GetRuntimeConfig(ctx, "missing", RuntimeConfigScopeGlobal, ""); !errors.Is(err, ErrRuntimeConfigNotFound) {
		t.Fatalf("missing runtime config = %v, want ErrRuntimeConfigNotFound", err)
	}
	if got, err := store.ListRuntimeConfigs(ctx, "", ""); err != nil || len(got) != 0 {
		t.Fatalf("empty runtime config list = %#v, %v", got, err)
	}
	if _, err := store.UpsertRuntimeConfig(ctx, RuntimeConfigUpdate{Key: "bad", DesiredValue: json.RawMessage("{")}); err == nil {
		t.Fatal("invalid JSON unexpectedly accepted")
	}
	if _, err := store.UpsertRuntimeConfig(ctx, RuntimeConfigUpdate{Key: "nil", DesiredValue: nil}); err == nil {
		t.Fatal("nil JSON unexpectedly accepted")
	}
	nonZero := int64(1)
	if _, err := store.UpsertRuntimeConfig(ctx, RuntimeConfigUpdate{
		Key: "new", DesiredValue: json.RawMessage(`true`), ExpectedVersion: &nonZero,
	}); !errors.Is(err, ErrRuntimeConfigConflict) {
		t.Fatalf("create with non-zero expected version = %v, want conflict", err)
	}

	row, err := store.UpsertRuntimeConfig(ctx, RuntimeConfigUpdate{
		Key: "request_read_timeout", DesiredValue: json.RawMessage(`"30s"`),
		ActorID: "actor-1", Reason: "initial", // exercise default global/hot values
	})
	if err != nil {
		t.Fatalf("initial upsert: %v", err)
	}
	if row.ID == "" || row.Version != 1 || row.Scope != RuntimeConfigScopeGlobal || row.ApplyMode != RuntimeConfigApplyHot || row.Status != RuntimeConfigPending {
		t.Fatalf("initial runtime config = %#v", row)
	}

	stale := int64(99)
	if _, err := store.UpsertRuntimeConfig(ctx, RuntimeConfigUpdate{
		Key: row.Key, Scope: row.Scope, ScopeID: row.ScopeID,
		DesiredValue: json.RawMessage(`"60s"`), ExpectedVersion: &stale,
	}); !errors.Is(err, ErrRuntimeConfigConflict) {
		t.Fatalf("stale update = %v, want conflict", err)
	}

	row, err = store.UpsertRuntimeConfig(ctx, RuntimeConfigUpdate{
		Key: row.Key, Scope: row.Scope, ScopeID: row.ScopeID,
		DesiredValue: json.RawMessage(`"60s"`), ApplyMode: RuntimeConfigApplyGraceful,
		ActorID: "actor-2", Reason: "increase", ExpectedVersion: &row.Version,
	})
	if err != nil {
		t.Fatalf("versioned update: %v", err)
	}
	if row.Version != 2 || row.ApplyMode != RuntimeConfigApplyGraceful || row.Status != RuntimeConfigPending {
		t.Fatalf("updated runtime config = %#v", row)
	}

	// The store must return copies of JSON values, not slices backed by the
	// row held under its mutex.
	row.DesiredValue[0] = 'X'
	got, err := store.GetRuntimeConfig(ctx, row.Key, row.Scope, row.ScopeID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if string(got.DesiredValue) != `"60s"` {
		t.Fatalf("stored desired value was mutated through return value: %s", got.DesiredValue)
	}

	if _, err := store.UpsertRuntimeConfig(ctx, RuntimeConfigUpdate{
		Key: "node_limit", Scope: RuntimeConfigScopeNode, ScopeID: "node-a",
		DesiredValue: json.RawMessage(`10`),
	}); err != nil {
		t.Fatalf("scoped upsert: %v", err)
	}
	all, err := store.ListRuntimeConfigs(ctx, "", "")
	if err != nil || len(all) != 2 {
		t.Fatalf("all runtime configs = %#v, %v", all, err)
	}
	nodeOnly, err := store.ListRuntimeConfigs(ctx, RuntimeConfigScopeNode, "node-a")
	if err != nil || len(nodeOnly) != 1 || nodeOnly[0].Key != "node_limit" {
		t.Fatalf("node runtime configs = %#v, %v", nodeOnly, err)
	}
	globalNode, err := store.ListRuntimeConfigs(ctx, RuntimeConfigScopeGlobal, "node-a")
	if err != nil || len(globalNode) != 0 {
		t.Fatalf("global configs for node scope = %#v, %v", globalNode, err)
	}

	if err := store.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, 99, json.RawMessage(`"bad"`), ""); !errors.Is(err, ErrRuntimeConfigConflict) {
		t.Fatalf("mark with stale version = %v, want conflict", err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, row.Version, json.RawMessage(`"60s"`), ""); err != nil {
		t.Fatalf("mark applied: %v", err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, "missing", RuntimeConfigScopeGlobal, "", 1, json.RawMessage(`true`), ""); !errors.Is(err, ErrRuntimeConfigConflict) {
		t.Fatalf("mark missing runtime config = %v, want conflict", err)
	}
	got, err = store.GetRuntimeConfig(ctx, row.Key, row.Scope, row.ScopeID)
	if err != nil || got.Status != RuntimeConfigApplied || string(got.EffectiveValue) != `"60s"` || got.AppliedAt == nil {
		t.Fatalf("applied runtime config = %#v, %v", got, err)
	}

	row, err = store.UpsertRuntimeConfig(ctx, RuntimeConfigUpdate{
		Key: row.Key, Scope: row.Scope, ScopeID: row.ScopeID,
		DesiredValue: json.RawMessage(`"90s"`), ExpectedVersion: &row.Version,
	})
	if err != nil {
		t.Fatalf("failed-apply update: %v", err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, row.Version, json.RawMessage(`"90s"`), "daemon rejected value"); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	got, err = store.GetRuntimeConfig(ctx, row.Key, row.Scope, row.ScopeID)
	if err != nil || got.Status != RuntimeConfigFailed || got.LastError != "daemon rejected value" || got.AppliedAt != nil {
		t.Fatalf("failed runtime config = %#v, %v", got, err)
	}

	revisions, err := store.ListRuntimeConfigRevisions(ctx, row.Key, row.Scope, row.ScopeID, 1)
	if err != nil || len(revisions) != 1 || revisions[0].Version != 3 || string(revisions[0].OldValue) != `"60s"` || string(revisions[0].NewValue) != `"90s"` {
		t.Fatalf("latest runtime config revision = %#v, %v", revisions, err)
	}
	revisions, err = store.ListRuntimeConfigRevisions(ctx, row.Key, row.Scope, row.ScopeID, 0)
	if err != nil || len(revisions) != 3 {
		t.Fatalf("default runtime config revisions = %#v, %v", revisions, err)
	}
	revisions, err = store.ListRuntimeConfigRevisions(ctx, row.Key, row.Scope, row.ScopeID, 201)
	if err != nil || len(revisions) != 3 {
		t.Fatalf("capped runtime config revisions = %#v, %v", revisions, err)
	}
	revision, err := store.GetRuntimeConfigRevision(ctx, row.Key, row.Scope, row.ScopeID, 1)
	if err != nil || revision.Version != 1 || string(revision.NewValue) != `"30s"` {
		t.Fatalf("first runtime config revision = %#v, %v", revision, err)
	}
	if _, err := store.GetRuntimeConfigRevision(ctx, row.Key, row.Scope, row.ScopeID, 99); !errors.Is(err, ErrRuntimeConfigNotFound) {
		t.Fatalf("missing runtime config revision = %v, want ErrRuntimeConfigNotFound", err)
	}
}

func TestMemStoreRuntimeConfigOperationGuardsAndTerminalPaths(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	config, err := store.UpsertRuntimeConfig(ctx, RuntimeConfigUpdate{
		Key: "rolling_limit", DesiredValue: json.RawMessage(`10`), ApplyMode: RuntimeConfigApplyRolling,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := store.CreateRuntimeConfigOperation(ctx, RuntimeConfig{ApplyMode: RuntimeConfigApplyHot, DesiredValue: json.RawMessage(`true`)}, "", ""); err == nil {
		t.Fatal("hot operation unexpectedly accepted")
	}
	if _, err := store.CreateRuntimeConfigOperation(ctx, RuntimeConfig{ApplyMode: RuntimeConfigApplyRolling, DesiredValue: json.RawMessage("{")}, "", ""); err == nil {
		t.Fatal("invalid operation JSON unexpectedly accepted")
	}
	if _, err := store.GetRuntimeConfigOperation(ctx, "missing"); !errors.Is(err, ErrRuntimeConfigNotFound) {
		t.Fatalf("missing operation = %v, want not found", err)
	}
	if _, err := store.ClaimPendingRuntimeConfigOperation(ctx); !errors.Is(err, ErrRuntimeConfigNotFound) {
		t.Fatalf("claim with no operations = %v, want not found", err)
	}

	op, err := store.CreateRuntimeConfigOperation(ctx, config, "actor", "rolling apply")
	if err != nil {
		t.Fatalf("create operation: %v", err)
	}
	claimed, err := store.ClaimPendingRuntimeConfigOperation(ctx)
	if err != nil || claimed.ID != op.ID || claimed.StartedAt == nil || claimed.Status != RuntimeConfigOperationRunning {
		t.Fatalf("claimed operation = %#v, %v", claimed, err)
	}
	longError := strings.Repeat("e", 2048)
	if err := store.MarkRuntimeConfigOperationFailed(ctx, op.ID, "apply", longError); err != nil {
		t.Fatalf("mark operation failed: %v", err)
	}
	failed, err := store.GetRuntimeConfigOperation(ctx, op.ID)
	if err != nil || failed.Status != RuntimeConfigOperationFailed || len(failed.Error) != 1024 || failed.FinishedAt == nil {
		t.Fatalf("failed operation = %#v, %v", failed, err)
	}
	if err := store.MarkRuntimeConfigOperationFailed(ctx, op.ID, "again", "ignored"); !errors.Is(err, ErrRuntimeConfigNotFound) {
		t.Fatalf("re-failing terminal operation = %v, want not found", err)
	}

	config, err = store.UpsertRuntimeConfig(ctx, RuntimeConfigUpdate{
		Key: config.Key, DesiredValue: json.RawMessage(`20`), ApplyMode: RuntimeConfigApplyBreakGlass,
		ExpectedVersion: &config.Version,
	})
	if err != nil {
		t.Fatalf("second config update: %v", err)
	}
	op, err = store.CreateRuntimeConfigOperation(ctx, config, "actor", "break glass")
	if err != nil {
		t.Fatalf("create blocked operation: %v", err)
	}
	if _, err := store.ClaimPendingRuntimeConfigOperation(ctx); err != nil {
		t.Fatalf("claim blocked operation: %v", err)
	}
	if err := store.MarkRuntimeConfigOperationBlocked(ctx, op.ID, "approval", "requires two-person approval"); err != nil {
		t.Fatalf("mark blocked: %v", err)
	}
	blocked, err := store.GetRuntimeConfigOperation(ctx, op.ID)
	if err != nil || blocked.Status != RuntimeConfigOperationBlocked || blocked.Error != "requires two-person approval" {
		t.Fatalf("blocked operation = %#v, %v", blocked, err)
	}

	orphan, err := store.CreateRuntimeConfigOperation(ctx, RuntimeConfig{
		Key: "orphan", Scope: RuntimeConfigScopeGlobal, DesiredValue: json.RawMessage(`true`),
		ApplyMode: RuntimeConfigApplyGraceful, Version: 1,
	}, "actor", "orphan")
	if err != nil {
		t.Fatalf("create orphan operation: %v", err)
	}
	if _, err := store.ClaimPendingRuntimeConfigOperation(ctx); err != nil {
		t.Fatalf("claim orphan operation: %v", err)
	}
	if err := store.MarkRuntimeConfigOperationSucceeded(ctx, orphan.ID, nil, 0, 0); err != nil {
		t.Fatalf("succeed orphan operation: %v", err)
	}
}

func TestMemStoreRuntimeConfigOperationCanBeBlockedBeforeClaim(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	config, err := store.UpsertRuntimeConfig(ctx, RuntimeConfigUpdate{
		Key: "graceful_limit", DesiredValue: json.RawMessage(`10`), ApplyMode: RuntimeConfigApplyGraceful,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	op, err := store.CreateRuntimeConfigOperation(ctx, config, "actor", "controller unavailable")
	if err != nil {
		t.Fatalf("create operation: %v", err)
	}
	if err := store.MarkRuntimeConfigOperationBlocked(ctx, op.ID, "controller_unavailable", "no controller"); err != nil {
		t.Fatalf("mark pending operation blocked: %v", err)
	}
	blocked, err := store.GetRuntimeConfigOperation(ctx, op.ID)
	if err != nil || blocked.Status != RuntimeConfigOperationBlocked || blocked.FinishedAt == nil {
		t.Fatalf("blocked operation = %#v, %v", blocked, err)
	}
	row, err := store.GetRuntimeConfig(ctx, config.Key, config.Scope, config.ScopeID)
	if err != nil || row.Status != RuntimeConfigBlocked || row.LastError != "no controller" {
		t.Fatalf("blocked config row = %#v, %v", row, err)
	}
}
