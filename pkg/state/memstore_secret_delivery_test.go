package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreAppSecretDeliveryVersionFence(t *testing.T) {
	store, ctx, account, app := memValueHashFixture(t)
	const scope = "prod"
	const key = "DATABASE_URL"
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, scope, key, "kid-1", "1111111111111111", []byte("cipher-1")); err != nil {
		t.Fatalf("seed secret: %v", err)
	}

	first := mustAppSecret(t, store, account.ID, app.ID, scope, key)
	if first.DeliveryVersion != 1 || first.DeliveryStatus != state.SecretDeliveryPending {
		t.Fatalf("initial delivery = version %d status %q, want 1/pending", first.DeliveryVersion, first.DeliveryStatus)
	}
	if updated, err := store.RecordAppSecretDelivery(ctx, deliveryResult(account.ID, app.ID, key, 1, state.SecretDeliveryDelivered)); err != nil || updated != 1 {
		t.Fatalf("record v1 delivered: updated=%d err=%v", updated, err)
	}
	delivered := mustAppSecret(t, store, account.ID, app.ID, scope, key)
	if delivered.DeliveredVersion != 1 || delivered.DeliveryStatus != state.SecretDeliveryDelivered || delivered.LastDeliveredAt == nil {
		t.Fatalf("delivered v1 metadata = %+v", delivered)
	}

	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, scope, key, "kid-1", "2222222222222222", []byte("cipher-2")); err != nil {
		t.Fatalf("rotate secret: %v", err)
	}
	rotated := mustAppSecret(t, store, account.ID, app.ID, scope, key)
	if rotated.DeliveryVersion != 2 || rotated.DeliveredVersion != 1 || rotated.DeliveryStatus != state.SecretDeliveryPending {
		t.Fatalf("rotated delivery = current %d delivered %d status %q, want 2/1/pending", rotated.DeliveryVersion, rotated.DeliveredVersion, rotated.DeliveryStatus)
	}
	if updated, err := store.RecordAppSecretDelivery(ctx, deliveryResult(account.ID, app.ID, key, 1, state.SecretDeliveryDelivered)); err != nil || updated != 0 {
		t.Fatalf("stale v1 completion: updated=%d err=%v, want 0/nil", updated, err)
	}
	if got := mustAppSecret(t, store, account.ID, app.ID, scope, key); got.DeliveryStatus != state.SecretDeliveryPending {
		t.Fatalf("stale completion changed current status to %q", got.DeliveryStatus)
	}

	failed := deliveryResult(account.ID, app.ID, key, 2, state.SecretDeliveryFailed)
	failed.ErrorCode = "runtime_start_failed"
	if updated, err := store.RecordAppSecretDelivery(ctx, failed); err != nil || updated != 1 {
		t.Fatalf("record v2 failed: updated=%d err=%v", updated, err)
	}
	if got := mustAppSecret(t, store, account.ID, app.ID, scope, key); got.DeliveryStatus != state.SecretDeliveryFailed || got.LastDeliveryErrorCode != "runtime_start_failed" {
		t.Fatalf("failed status = %q/%q", got.DeliveryStatus, got.LastDeliveryErrorCode)
	}
	if updated, err := store.RecordAppSecretDelivery(ctx, deliveryResult(account.ID, app.ID, key, 2, state.SecretDeliveryDelivered)); err != nil || updated != 1 {
		t.Fatalf("record v2 delivered: updated=%d err=%v", updated, err)
	}
	final := mustAppSecret(t, store, account.ID, app.ID, scope, key)
	if final.DeliveryStatus != state.SecretDeliveryDelivered || final.DeliveredVersion != 2 || final.LastDeliveryErrorCode != "" {
		t.Fatalf("final delivery metadata = %+v", final)
	}
}

func TestMemStoreAppSecretResealPreservesDeliveryVersion(t *testing.T) {
	store, ctx, account, app := memValueHashFixture(t)
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, "prod", "TOKEN", "old", "1111111111111111", []byte("cipher-1")); err != nil {
		t.Fatal(err)
	}
	if err := store.ResealAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, "prod", "TOKEN", "new", "1111111111111111", []byte("cipher-2")); err != nil {
		t.Fatal(err)
	}
	got := mustAppSecret(t, store, account.ID, app.ID, "prod", "TOKEN")
	if got.DeliveryVersion != 1 {
		t.Fatalf("reseal advanced delivery version to %d, want 1", got.DeliveryVersion)
	}
}

func TestMemStoreAppSecretRuntimeReloadVersionFence(t *testing.T) {
	store, ctx, account, app := memValueHashFixture(t)
	const scope, key = "prod", "DATABASE_URL"
	firstRuntime, err := store.CreateInstance(ctx, app.ID, "deployment-1", string(state.StateRunning), 256, "node-1", "")
	if err != nil {
		t.Fatalf("create first runtime: %v", err)
	}
	secondRuntime, err := store.CreateInstance(ctx, app.ID, "deployment-1", string(state.StateRunning), 256, "node-1", "")
	if err != nil {
		t.Fatalf("create second runtime: %v", err)
	}
	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, scope, key, "kid-1", "1111111111111111", []byte("cipher-1")); err != nil {
		t.Fatalf("seed secret: %v", err)
	}
	result := state.AppSecretRuntimeReloadResult{
		AccountID: account.ID, AppID: app.ID, InstanceID: firstRuntime.ID, Revision: strings.Repeat("a", 64),
		Projection: state.SecretReloadProjectionUpdated, Signal: state.SecretReloadSignalSent,
		AttemptedAt: time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC),
		Candidates:  []state.AppSecretDeliveryCandidate{{Scope: scope, Key: key, Version: 1}},
	}
	if updated, err := store.RecordAppSecretRuntimeReload(ctx, result); err != nil || updated != 1 {
		t.Fatalf("record live reload v1: updated=%d err=%v", updated, err)
	}
	got := mustAppSecret(t, store, account.ID, app.ID, scope, key)
	if got.LastRuntimeReloadVersion != 1 || got.LastRuntimeReloadProjection != state.SecretReloadProjectionUpdated ||
		got.LastRuntimeReloadSignal != state.SecretReloadSignalSent || got.LastRuntimeReloadAt == nil {
		t.Fatalf("runtime reload metadata = %+v", got)
	}
	result.InstanceID = secondRuntime.ID
	result.Signal = state.SecretReloadSignalQueued
	if updated, err := store.RecordAppSecretRuntimeReload(ctx, result); err != nil || updated != 1 {
		t.Fatalf("record second runtime v1: updated=%d err=%v", updated, err)
	}
	observations, err := store.ListAppSecretRuntimeReloadObservations(ctx, account.ID, app.ID, scope)
	if err != nil || len(observations) != 2 || !hasRuntimeObservation(observations, firstRuntime.ID, 1) || !hasRuntimeObservation(observations, secondRuntime.ID, 1) {
		t.Fatalf("runtime observations = %+v, %v; want both active runtimes", observations, err)
	}

	if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, scope, key, "kid-1", "2222222222222222", []byte("cipher-2")); err != nil {
		t.Fatalf("rotate secret: %v", err)
	}
	if updated, err := store.RecordAppSecretRuntimeReload(ctx, result); !errors.Is(err, state.ErrConflict) || updated != 0 {
		t.Fatalf("stale runtime reload v1: updated=%d err=%v, want conflict", updated, err)
	}
	got = mustAppSecret(t, store, account.ID, app.ID, scope, key)
	if got.DeliveryVersion != 2 || got.LastRuntimeReloadVersion != 1 {
		t.Fatalf("stale reload status was attributed to v2: current=%d observed=%d", got.DeliveryVersion, got.LastRuntimeReloadVersion)
	}
	result.InstanceID = firstRuntime.ID
	result.Signal = state.SecretReloadSignalSent
	result.Candidates[0].Version = 2
	result.AttemptedAt = result.AttemptedAt.Add(time.Minute)
	if updated, err := store.RecordAppSecretRuntimeReload(ctx, result); err != nil || updated != 1 {
		t.Fatalf("record first runtime v2: updated=%d err=%v", updated, err)
	}
	observations, err = store.ListAppSecretRuntimeReloadObservations(ctx, account.ID, app.ID, scope)
	if err != nil || len(observations) != 2 || !hasRuntimeObservation(observations, firstRuntime.ID, 2) || !hasRuntimeObservation(observations, secondRuntime.ID, 1) {
		t.Fatalf("runtime observations after rotation = %+v, %v; want current and stale versions", observations, err)
	}
	ack := state.AppSecretRuntimeReloadAckResult{
		AccountID: account.ID, AppID: app.ID, InstanceID: firstRuntime.ID, Revision: strings.Repeat("b", 64),
		Status: state.SecretApplicationReloadAckApplied, AttemptedAt: result.AttemptedAt.Add(time.Second),
		Candidates: []state.AppSecretDeliveryCandidate{{Scope: scope, Key: key, Version: 2}},
	}
	if updated, err := store.RecordAppSecretRuntimeReloadAck(ctx, ack); err != nil || updated != 1 {
		t.Fatalf("record app acknowledgement v2: updated=%d err=%v", updated, err)
	}
	observations, err = store.ListAppSecretRuntimeReloadObservations(ctx, account.ID, app.ID, scope)
	if err != nil || !hasApplicationAck(observations, firstRuntime.ID, 2, state.SecretApplicationReloadAckApplied) {
		t.Fatalf("runtime observations with app ack = %+v, %v", observations, err)
	}
	ack.Candidates[0].Version = 1
	if updated, err := store.RecordAppSecretRuntimeReloadAck(ctx, ack); !errors.Is(err, state.ErrConflict) || updated != 0 {
		t.Fatalf("stale app acknowledgement v1: updated=%d err=%v, want conflict", updated, err)
	}
}

func hasRuntimeObservation(observations []state.AppSecretRuntimeReloadObservation, instanceID string, version int64) bool {
	for _, observation := range observations {
		if observation.InstanceID == instanceID && observation.Version == version {
			return true
		}
	}
	return false
}

func hasApplicationAck(observations []state.AppSecretRuntimeReloadObservation, instanceID string, version int64, status state.SecretApplicationReloadAckStatus) bool {
	for _, observation := range observations {
		if observation.InstanceID == instanceID && observation.ApplicationAckVersion == version && observation.ApplicationAck == status && observation.ApplicationAckAt != nil {
			return true
		}
	}
	return false
}

func deliveryResult(accountID, appID, key string, version int64, status state.SecretDeliveryStatus) state.AppSecretDeliveryResult {
	return state.AppSecretDeliveryResult{
		AccountID: accountID, AppID: appID, WakeID: "wake-1", InstanceID: "instance-1",
		Status: status, AttemptedAt: time.Date(2026, 9, 22, 16, 30, 0, 0, time.UTC),
		Candidates: []state.AppSecretDeliveryCandidate{{Scope: "prod", Key: key, Version: version}},
	}
}

func mustAppSecret(t *testing.T, store *state.MemStore, accountID, appID, scope, key string) *state.AppSecret {
	t.Helper()
	got, err := store.GetAppSecretInScope(t.Context(), accountID, appID, scope, key)
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	return got
}
