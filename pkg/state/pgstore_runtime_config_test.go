//go:build !no_pg

package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func pgRuntimeConfigStore(t *testing.T) (*state.PgStore, context.Context) {
	t.Helper()
	pool := pgtest.Open(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		CREATE TABLE runtime_config_entries (
			id uuid PRIMARY KEY,
			config_key text NOT NULL,
			scope text NOT NULL,
			scope_id text NOT NULL,
			desired_value jsonb NOT NULL,
			 effective_value jsonb,
			version bigint NOT NULL,
			rollout_percent smallint NOT NULL DEFAULT 100,
			rollout_state text NOT NULL DEFAULT 'stable',
			rollout_auto_promote boolean NOT NULL DEFAULT false,
			 apply_mode text NOT NULL,
			status text NOT NULL,
			last_error text,
			actor_id uuid,
			reason text,
			updated_at timestamptz NOT NULL DEFAULT now(),
			applied_at timestamptz,
			UNIQUE (config_key, scope, scope_id)
		);
		CREATE TABLE runtime_config_revisions (
			id bigserial PRIMARY KEY,
			entry_id uuid NOT NULL REFERENCES runtime_config_entries(id) ON DELETE CASCADE,
			config_key text NOT NULL,
			scope text NOT NULL,
			scope_id text NOT NULL,
			 version bigint NOT NULL,
			 rollout_percent smallint NOT NULL DEFAULT 100,
			 rollout_auto_promote boolean NOT NULL DEFAULT false,
			 old_value jsonb,
			new_value jsonb NOT NULL,
			actor_id uuid,
			reason text,
			created_at timestamptz NOT NULL DEFAULT now()
		);
		CREATE TABLE runtime_config_operations (
			id uuid PRIMARY KEY,
			config_key text NOT NULL,
			scope text NOT NULL,
			scope_id text NOT NULL,
			config_version bigint NOT NULL,
			desired_value jsonb NOT NULL,
			effective_value jsonb,
			apply_mode text NOT NULL,
			status text NOT NULL DEFAULT 'pending',
			phase text NOT NULL DEFAULT 'queued',
			error text,
			actor_id uuid,
			reason text NOT NULL DEFAULT '',
			target_count integer NOT NULL DEFAULT 0,
			applied_count integer NOT NULL DEFAULT 0,
			failed_count integer NOT NULL DEFAULT 0,
			requested_at timestamptz NOT NULL DEFAULT now(),
			started_at timestamptz,
			finished_at timestamptz
		);
	`)
	if err != nil {
		t.Fatalf("create runtime config fixture: %v", err)
	}
	return state.NewPgStore(pool), ctx
}

func TestPgStoreRuntimeConfigCRUDAndRevisionHistory(t *testing.T) {
	store, ctx := pgRuntimeConfigStore(t)
	actor := "11111111-1111-1111-1111-111111111111"

	if _, err := store.GetRuntimeConfig(ctx, "missing", state.RuntimeConfigScopeGlobal, ""); !errors.Is(err, state.ErrRuntimeConfigNotFound) {
		t.Fatalf("missing runtime config = %v, want not found", err)
	}
	if _, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{Key: "bad", DesiredValue: nil}); err == nil {
		t.Fatal("nil JSON unexpectedly accepted")
	}
	if _, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{Key: "bad", DesiredValue: json.RawMessage("{")}); err == nil {
		t.Fatal("invalid JSON unexpectedly accepted")
	}

	row, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: "request_read_timeout", DesiredValue: json.RawMessage(`"30s"`), ActorID: actor, Reason: "initial",
	})
	if err != nil {
		t.Fatalf("initial upsert: %v", err)
	}
	if row.Version != 1 || row.Scope != state.RuntimeConfigScopeGlobal || row.ApplyMode != state.RuntimeConfigApplyHot || row.Status != state.RuntimeConfigPending || row.ActorID != actor {
		t.Fatalf("initial runtime config = %#v", row)
	}
	nonZero := int64(1)
	if _, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: "new", DesiredValue: json.RawMessage(`true`), ExpectedVersion: &nonZero,
	}); !errors.Is(err, state.ErrRuntimeConfigConflict) {
		t.Fatalf("create with non-zero expected version = %v, want conflict", err)
	}
	stale := int64(99)
	if _, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: row.Key, DesiredValue: json.RawMessage(`"60s"`), ExpectedVersion: &stale,
	}); !errors.Is(err, state.ErrRuntimeConfigConflict) {
		t.Fatalf("stale update = %v, want conflict", err)
	}

	row, err = store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: row.Key, DesiredValue: json.RawMessage(`"60s"`), ApplyMode: state.RuntimeConfigApplyGraceful,
		ActorID: actor, Reason: "increase", ExpectedVersion: &row.Version,
	})
	if err != nil || row.Version != 2 || row.ApplyMode != state.RuntimeConfigApplyGraceful {
		t.Fatalf("versioned update = %#v, %v", row, err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, row.Version, nil, ""); err != nil {
		t.Fatalf("mark applied with nil value: %v", err)
	}
	row, err = store.GetRuntimeConfig(ctx, row.Key, row.Scope, row.ScopeID)
	if err != nil || row.Status != state.RuntimeConfigApplied || string(row.EffectiveValue) != "null" || row.AppliedAt == nil {
		t.Fatalf("applied runtime config = %#v, %v", row, err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, 99, json.RawMessage(`"bad"`), ""); !errors.Is(err, state.ErrRuntimeConfigConflict) {
		t.Fatalf("stale mark applied = %v, want conflict", err)
	}

	row, err = store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: row.Key, DesiredValue: json.RawMessage(`"90s"`), ExpectedVersion: &row.Version,
	})
	if err != nil {
		t.Fatalf("failed-apply update: %v", err)
	}
	if err := store.MarkRuntimeConfigApplied(ctx, row.Key, row.Scope, row.ScopeID, row.Version, json.RawMessage(`"90s"`), strings.Repeat("x", 2048)); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	row, err = store.GetRuntimeConfig(ctx, row.Key, row.Scope, row.ScopeID)
	if err != nil || row.Status != state.RuntimeConfigFailed || len(row.LastError) != 1024 || row.AppliedAt != nil {
		t.Fatalf("failed runtime config = %#v, %v", row, err)
	}

	if _, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: "node_limit", Scope: state.RuntimeConfigScopeNode, ScopeID: "node-a", DesiredValue: json.RawMessage(`10`),
	}); err != nil {
		t.Fatalf("scoped upsert: %v", err)
	}
	all, err := store.ListRuntimeConfigs(ctx, "", "")
	if err != nil || len(all) != 2 {
		t.Fatalf("all runtime configs = %#v, %v", all, err)
	}
	nodeOnly, err := store.ListRuntimeConfigs(ctx, state.RuntimeConfigScopeNode, "node-a")
	if err != nil || len(nodeOnly) != 1 || nodeOnly[0].Key != "node_limit" {
		t.Fatalf("node runtime configs = %#v, %v", nodeOnly, err)
	}
	revisions, err := store.ListRuntimeConfigRevisions(ctx, "request_read_timeout", state.RuntimeConfigScopeGlobal, "", 1)
	if err != nil || len(revisions) != 1 || revisions[0].Version != 3 || string(revisions[0].OldValue) != `"60s"` || string(revisions[0].NewValue) != `"90s"` {
		t.Fatalf("latest revisions = %#v, %v", revisions, err)
	}
	revisions, err = store.ListRuntimeConfigRevisions(ctx, "request_read_timeout", state.RuntimeConfigScopeGlobal, "", 0)
	if err != nil || len(revisions) != 3 {
		t.Fatalf("default revisions = %#v, %v", revisions, err)
	}
	revisions, err = store.ListRuntimeConfigRevisions(ctx, "request_read_timeout", state.RuntimeConfigScopeGlobal, "", 201)
	if err != nil || len(revisions) != 3 {
		t.Fatalf("capped revisions = %#v, %v", revisions, err)
	}
	revision, err := store.GetRuntimeConfigRevision(ctx, "request_read_timeout", state.RuntimeConfigScopeGlobal, "", 1)
	if err != nil || revision.Version != 1 || string(revision.NewValue) != `"30s"` {
		t.Fatalf("first revision = %#v, %v", revision, err)
	}
	if _, err := store.GetRuntimeConfigRevision(ctx, "request_read_timeout", state.RuntimeConfigScopeGlobal, "", 99); !errors.Is(err, state.ErrRuntimeConfigNotFound) {
		t.Fatalf("missing revision = %v, want ErrRuntimeConfigNotFound", err)
	}
}

func TestPgStoreRuntimeConfigOperationLifecycleAndGuards(t *testing.T) {
	store, ctx := pgRuntimeConfigStore(t)
	config, err := store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: "rolling_limit", DesiredValue: json.RawMessage(`10`), ApplyMode: state.RuntimeConfigApplyRolling,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if _, err := store.CreateRuntimeConfigOperation(ctx, state.RuntimeConfig{ApplyMode: state.RuntimeConfigApplyHot, DesiredValue: json.RawMessage(`true`)}, "", ""); err == nil {
		t.Fatal("hot operation unexpectedly accepted")
	}
	if _, err := store.CreateRuntimeConfigOperation(ctx, state.RuntimeConfig{ApplyMode: state.RuntimeConfigApplyRolling, DesiredValue: json.RawMessage("{")}, "", ""); err == nil {
		t.Fatal("invalid operation JSON unexpectedly accepted")
	}
	if _, err := store.GetRuntimeConfigOperation(ctx, "00000000-0000-0000-0000-000000000001"); !errors.Is(err, state.ErrRuntimeConfigNotFound) {
		t.Fatalf("missing operation = %v, want not found", err)
	}
	if _, err := store.ClaimPendingRuntimeConfigOperation(ctx); !errors.Is(err, state.ErrRuntimeConfigNotFound) {
		t.Fatalf("claim with no operations = %v, want not found", err)
	}

	op, err := store.CreateRuntimeConfigOperation(ctx, config, "", "rolling apply")
	if err != nil {
		t.Fatalf("create operation: %v", err)
	}
	claimed, err := store.ClaimPendingRuntimeConfigOperation(ctx)
	if err != nil || claimed.ID != op.ID || claimed.Status != state.RuntimeConfigOperationRunning || claimed.StartedAt == nil {
		t.Fatalf("claimed operation = %#v, %v", claimed, err)
	}
	if _, err := store.ClaimPendingRuntimeConfigOperation(ctx); !errors.Is(err, state.ErrRuntimeConfigNotFound) {
		t.Fatalf("second claim = %v, want not found", err)
	}
	if err := store.MarkRuntimeConfigOperationSucceeded(ctx, op.ID, nil, 1, 1); err != nil {
		t.Fatalf("mark succeeded: %v", err)
	}
	got, err := store.GetRuntimeConfigOperation(ctx, op.ID)
	if err != nil || got.Status != state.RuntimeConfigOperationSucceeded || got.FinishedAt == nil || got.AppliedCount != 1 || got.TargetCount != 1 || string(got.EffectiveValue) != "null" {
		t.Fatalf("succeeded operation = %#v, %v", got, err)
	}
	config, err = store.GetRuntimeConfig(ctx, config.Key, config.Scope, config.ScopeID)
	if err != nil || config.Status != state.RuntimeConfigApplied || string(config.EffectiveValue) != `null` {
		t.Fatalf("config after operation = %#v, %v", config, err)
	}
	if err := store.MarkRuntimeConfigOperationSucceeded(ctx, op.ID, nil, 0, 0); !errors.Is(err, state.ErrRuntimeConfigNotFound) {
		t.Fatalf("re-succeed terminal operation = %v, want not found", err)
	}

	config, err = store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: config.Key, DesiredValue: json.RawMessage(`20`), ApplyMode: state.RuntimeConfigApplyBreakGlass,
		ExpectedVersion: &config.Version,
	})
	if err != nil {
		t.Fatalf("second config update: %v", err)
	}
	op, err = store.CreateRuntimeConfigOperation(ctx, config, "", "break glass")
	if err != nil {
		t.Fatalf("create second operation: %v", err)
	}
	if _, err := store.ClaimPendingRuntimeConfigOperation(ctx); err != nil {
		t.Fatalf("claim second operation: %v", err)
	}
	if err := store.MarkRuntimeConfigOperationFailed(ctx, op.ID, "apply", strings.Repeat("f", 2048)); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	got, err = store.GetRuntimeConfigOperation(ctx, op.ID)
	if err != nil || got.Status != state.RuntimeConfigOperationFailed || len(got.Error) != 1024 || got.FinishedAt == nil {
		t.Fatalf("failed operation = %#v, %v", got, err)
	}
	if err := store.MarkRuntimeConfigOperationBlocked(ctx, op.ID, "again", "ignored"); !errors.Is(err, state.ErrRuntimeConfigNotFound) {
		t.Fatalf("re-block terminal operation = %v, want not found", err)
	}

	config, err = store.UpsertRuntimeConfig(ctx, state.RuntimeConfigUpdate{
		Key: config.Key, DesiredValue: json.RawMessage(`30`), ApplyMode: state.RuntimeConfigApplyBreakGlass,
		ExpectedVersion: &config.Version,
	})
	if err != nil {
		t.Fatalf("third config update: %v", err)
	}
	op, err = store.CreateRuntimeConfigOperation(ctx, config, "", "blocked")
	if err != nil {
		t.Fatalf("create third operation: %v", err)
	}
	if _, err := store.ClaimPendingRuntimeConfigOperation(ctx); err != nil {
		t.Fatalf("claim third operation: %v", err)
	}
	if err := store.MarkRuntimeConfigOperationBlocked(ctx, op.ID, "approval", strings.Repeat("b", 2048)); err != nil {
		t.Fatalf("mark blocked: %v", err)
	}
	got, err = store.GetRuntimeConfigOperation(ctx, op.ID)
	if err != nil || got.Status != state.RuntimeConfigOperationBlocked || len(got.Error) != 1024 {
		t.Fatalf("blocked operation = %#v, %v", got, err)
	}
}
