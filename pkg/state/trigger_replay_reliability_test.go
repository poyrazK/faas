package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/pressly/goose/v3"
)

func seedReplayReliability(t *testing.T, store state.Store) (state.Account, state.App, sqlc.Trigger) {
	t.Helper()
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "replay-guards-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: strings.ReplaceAll(uuid.NewString(), "-", ""), AccountID: account.ID, Slug: "guards-" + uuid.NewString()[:8], RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, app.ID, "kafka", "orders", true, []byte(`{}`), "", 10, 1000, 5, 1<<20, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	return account, app, trigger
}

func seedReplayReliabilityRecord(t *testing.T, store state.Store, trigger sqlc.Trigger, item string, omitted bool) (string, int64) {
	t.Helper()
	metadata := []byte(`{}`)
	if omitted {
		metadata = []byte(`{"payload_omitted_bytes":2000000}`)
	}
	id, err := store.InsertTriggerRecord(t.Context(), trigger.ID.String(), item, []byte(`{"id":1}`), nil, metadata)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := store.(state.TriggerBatchClaimer).ClaimTriggerRecordsByItems(t.Context(), trigger.ID.String(), []string{item})
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim=%+v error=%v", claims, err)
	}
	return id, claims[0].ClaimGeneration
}

func TestExternalTriggerReplayGuards(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store state.Store = state.NewMemStore()
			if backend == "postgres" {
				store = state.NewPgStore(pgtest.OpenMigrated(t))
			}
			account, app, trigger := seedReplayReliability(t, store)
			t.Run("active_claim", func(t *testing.T) {
				id, generation := seedReplayReliabilityRecord(t, store, trigger, "orders/0/1", false)
				if err := store.RetryTriggerRecordByOperator(t.Context(), app.ID, trigger.ID.String(), id); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("retry active claim=%v", err)
				}
				if err := store.(state.TriggerClaimFinisher).CompleteClaimedTriggerRecord(t.Context(), id, generation); err != nil {
					t.Fatalf("refused replay changed active claim: %v", err)
				}
			})
			omitted, gen := seedReplayReliabilityRecord(t, store, trigger, "orders/0/2", true)
			if err := store.(state.TriggerClaimFinisher).RouteClaimedTriggerDeadLetter(t.Context(), omitted, gen, trigger.ID.String(), "payload_too_large", []byte(`{}`)); err != nil {
				t.Fatal(err)
			}
			if err := store.RetryTriggerRecordByOperator(t.Context(), app.ID, trigger.ID.String(), omitted); !errors.Is(err, state.ErrDeadLetterNotReplayable) {
				t.Fatalf("retry omitted payload=%v", err)
			}
			entries, err := store.ListDeadLetterEvents(t.Context(), app.ID, 10, "")
			if err != nil || len(entries) != 1 {
				t.Fatalf("dead letters=%+v error=%v", entries, err)
			}
			if _, err = store.ReplayDeadLetterEvent(t.Context(), account.ID, app.ID, entries[0].ID); !errors.Is(err, state.ErrDeadLetterNotReplayable) {
				t.Fatalf("unified replay omitted payload=%v", err)
			}
			valid, generation := seedReplayReliabilityRecord(t, store, trigger, "orders/0/3", false)
			if err = store.(state.TriggerClaimFinisher).RouteClaimedTriggerDeadLetter(t.Context(), valid, generation, trigger.ID.String(), "max_attempts", []byte(`{}`)); err != nil {
				t.Fatal(err)
			}
			count, err := store.ReplayDeadLetterEvents(t.Context(), account.ID, app.ID, 10)
			if err != nil || count != 1 {
				t.Fatalf("bulk replay should skip omitted payload: count=%d error=%v", count, err)
			}
			ready, err := store.(state.ExternalTriggerReplayStore).ListDueExternalTriggerReplays(t.Context(), trigger.ID.String(), 10)
			if err != nil || len(ready) != 1 || ready[0].ID.String() != valid {
				t.Fatalf("durable replays=%+v error=%v", ready, err)
			}
		})
	}
}

func TestExternalTriggerReplayMigrationPreservesRecoveryAndRedactsSecrets(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, app, trigger := seedReplayReliability(t, store)
	id, generation := seedReplayReliabilityRecord(t, store, trigger, "orders/0/9", false)
	if err := store.RouteClaimedTriggerDeadLetter(t.Context(), id, generation, trigger.ID.String(), "max_attempts", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	entries, err := store.ListDeadLetterEvents(t.Context(), app.ID, 10, "")
	if err != nil || len(entries) != 1 {
		t.Fatal("seed dead letter", err)
	}
	if _, err = store.ReplayDeadLetterEvent(t.Context(), account.ID, app.ID, entries[0].ID); err != nil {
		t.Fatal(err)
	}
	// Simulate the previous release: successful unified replay, no durable marker.
	if _, err = pool.Exec(t.Context(), `UPDATE trigger_records SET operator_replay=false WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	sqlDB := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = sqlDB.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS,
		goose.WithDisableVersioning(true), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	const version = 20261111160000419
	if _, err = provider.ApplyVersion(t.Context(), version, false); err != nil {
		t.Fatal(err)
	}
	const secret = "synthetic-migration-token-8821"
	if err = store.RecordTriggerConsumerHealth(t.Context(), trigger.ID.String(), state.TriggerConsumerHealthObservation{LastPollAt: time.Now(), Error: "connect nats://" + secret + "@broker.example:4222: connection refused"}); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.ApplyVersion(t.Context(), version, true); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.ApplyVersion(t.Context(), version, true); err != nil {
		t.Fatalf("interrupted upgrade cannot resume: %v", err)
	}
	health, err := store.TriggerConsumerHealth(t.Context(), trigger.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(health.LastError, secret) || !strings.Contains(health.LastError, "connection refused") {
		t.Fatal("migration did not redact credentials while preserving failure context")
	}
	ready, err := store.ListDueExternalTriggerReplays(t.Context(), trigger.ID.String(), 10)
	if err != nil || len(ready) != 1 {
		t.Fatalf("old acknowledged replay not recovered: count=%d error=%v", len(ready), err)
	}
	if _, err = provider.ApplyVersion(t.Context(), version, false); err == nil {
		t.Fatal("downgrade discarded unsettled durable replay")
	}
}
