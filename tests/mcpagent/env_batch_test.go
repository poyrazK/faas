// adr: 20261103171917
package mcpagent_test

import (
	"errors"
	"maps"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAtomicEnvBatchContract(t *testing.T) {
	t.Run("memory", func(t *testing.T) { atomicEnvBatchContract(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { atomicEnvBatchContract(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}

func newBatchApp(t *testing.T, store state.Store) (state.Account, state.App) {
	t.Helper()
	account, err := store.CreateAccount(t.Context(), "batch-"+uuid.NewString()+"@example.test", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "batch-app-" + uuid.NewString()[:8], Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	return account, app
}

func atomicEnvBatchContract(t *testing.T, store state.Store) {
	t.Helper()
	account, app := newBatchApp(t, store)
	batch := store.(state.EnvBatchStore)
	config := store.(state.ConfigRevisionStore)
	zero := int64(0)
	request := api.EnvBatchRequest{RequestID: uuid.NewString(), ExpectedRevision: &zero, Values: map[string]string{"FIRST": "one", "SECOND": "two"}}
	change := state.EnvBatchChange{AccountID: account.ID, AppID: app.ID, Scope: "default", Actor: "test-agent", Guard: true, Limits: api.MustLimitsFor(api.PlanFree), Request: request}
	first, err := batch.ApplyEnvBatch(t.Context(), change)
	if err != nil {
		t.Fatal(err)
	}
	if first.ConfigChange.Revision != 2 || first.ConfigChange.Guard == nil {
		t.Fatalf("batch receipt=%+v", first)
	}
	variables, _, err := config.ConfigState(t.Context(), account.ID, app.ID, "default")
	if err != nil || !maps.Equal(variables, request.Values) {
		t.Fatalf("batch values=%v err=%v", variables, err)
	}
	// Two agents reviewed the same version. Exactly one transaction commits.
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, value := range []string{"writer-a", "writer-b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			revision := first.ConfigChange.Revision
			next := change
			next.Request = api.EnvBatchRequest{RequestID: uuid.NewString(), ExpectedRevision: &revision, Values: map[string]string{"CONCURRENT": value}}
			_, err := batch.ApplyEnvBatch(t.Context(), next)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	won, lost := 0, 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, state.ErrEnvBatchRevisionConflict):
			lost++
		default:
			t.Fatalf("concurrent batch: %v", err)
		}
	}
	if won != 1 || lost != 1 {
		t.Fatalf("concurrent commits=%d conflicts=%d", won, lost)
	}
	// The first HTTP response could have been lost. Replay still returns its
	// original receipt after another agent has changed the current revision.
	replay, err := batch.ApplyEnvBatch(t.Context(), change)
	if err != nil || replay.ConfigChange.Revision != first.ConfigChange.Revision {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	history, err := config.ListConfigRevisions(t.Context(), app.ID, "default", 20)
	if err != nil || len(history) != 3 {
		t.Fatalf("replay appended history: %d %v", len(history), err)
	}
	tampered := change
	tampered.Request.Values = map[string]string{"FIRST": "different"}
	if _, err := batch.ApplyEnvBatch(t.Context(), tampered); !errors.Is(err, state.ErrEnvBatchRequestConflict) {
		t.Fatalf("request ID reuse=%v", err)
	}
	// Global quota counts all environments, not just the selected scope.
	for i := 0; i < change.Limits.EnvVarsMax-3; i++ {
		if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "other", "VAR"+string(rune('A'+i)), "other"); err != nil {
			t.Fatal(err)
		}
	}
	before, _, _ := config.ConfigState(t.Context(), account.ID, app.ID, "default")
	head := history[0].Revision
	quota := change
	quota.Request = api.EnvBatchRequest{RequestID: uuid.NewString(), ExpectedRevision: &head, Values: map[string]string{"EXTRA_A": "a", "EXTRA_B": "b"}}
	var quotaErr *state.EnvBatchQuotaError
	if _, err := batch.ApplyEnvBatch(t.Context(), quota); !errors.As(err, &quotaErr) {
		t.Fatalf("quota batch=%v", err)
	}
	after, _, _ := config.ConfigState(t.Context(), account.ID, app.ID, "default")
	if !maps.Equal(before, after) {
		t.Fatal("quota refusal partially changed variables")
	}
	// An old writer can commit values before its history recorder finishes.
	// A revision number alone must not allow the batch to overwrite that edit.
	if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "default", "FIRST", "legacy edit"); err != nil {
		t.Fatal(err)
	}
	stale := change
	stale.Request = api.EnvBatchRequest{RequestID: uuid.NewString(), ExpectedRevision: &head, Values: map[string]string{"FIRST": "overwrite"}}
	if _, err := batch.ApplyEnvBatch(t.Context(), stale); !errors.Is(err, state.ErrEnvBatchRevisionConflict) {
		t.Fatalf("legacy edit was overwritten: %v", err)
	}
	initialAccount, initialApp := newBatchApp(t, store)
	if err := store.UpsertAppEnvInScope(t.Context(), initialAccount.ID, initialApp.ID, "default", "FIRST", "first legacy edit"); err != nil {
		t.Fatal(err)
	}
	initial := change
	initial.AccountID, initial.AppID = initialAccount.ID, initialApp.ID
	initial.Request = api.EnvBatchRequest{RequestID: uuid.NewString(), ExpectedRevision: &zero, Values: map[string]string{"FIRST": "overwrite"}}
	if _, err := batch.ApplyEnvBatch(t.Context(), initial); !errors.Is(err, state.ErrEnvBatchRevisionConflict) {
		t.Fatalf("unrecorded first write was overwritten: %v", err)
	}
	baseline, err := config.ListConfigRevisions(t.Context(), initialApp.ID, "default", 20)
	if err != nil || len(baseline) != 1 || baseline[0].Source != state.ConfigSourceBaseline || baseline[0].Variables["FIRST"] != "first legacy edit" {
		t.Fatalf("legacy baseline missing: %+v %v", baseline, err)
	}
	initial.Request.RequestID = uuid.NewString()
	initial.Request.ExpectedRevision = &baseline[0].Revision
	if _, err := batch.ApplyEnvBatch(t.Context(), initial); err != nil {
		t.Fatalf("reviewed legacy scope could not advance: %v", err)
	}
}

func TestAtomicEnvBatchPostgresRollsBackEveryEffect(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, app := newBatchApp(t, store)
	// Inject a database failure after the first sorted key was written.
	_, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_test_batch_value() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.key='ZZ_FAIL' THEN RAISE EXCEPTION 'injected batch write failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER zz_test_batch_failure BEFORE INSERT OR UPDATE ON app_envs FOR EACH ROW EXECUTE FUNCTION reject_test_batch_value()`)
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	change := state.EnvBatchChange{AccountID: account.ID, AppID: app.ID, Scope: "default", Actor: "fault-test", Guard: true, Limits: api.MustLimitsFor(api.PlanFree), Request: api.EnvBatchRequest{RequestID: uuid.NewString(), ExpectedRevision: &zero, Values: map[string]string{"AA_FIRST": "one", "ZZ_FAIL": "two"}}}
	if _, err := store.ApplyEnvBatch(t.Context(), change); err == nil {
		t.Fatal("injected failure was ignored")
	}
	values, _, err := store.ConfigState(t.Context(), account.ID, app.ID, "default")
	if err != nil || len(values) != 0 {
		t.Fatalf("transaction left values: %v %v", values, err)
	}
	history, err := store.ListConfigRevisions(t.Context(), app.ID, "default", 20)
	if err != nil || len(history) != 0 {
		t.Fatalf("transaction left history: %v %v", history, err)
	}
	var receipts, guards int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM app_env_batch_receipts),(SELECT count(*) FROM app_config_guards)`).Scan(&receipts, &guards); err != nil {
		t.Fatal(err)
	}
	if receipts != 0 || guards != 0 {
		t.Fatalf("transaction left receipts=%d guards=%d", receipts, guards)
	}
	if _, err := pool.Exec(t.Context(), `DROP TRIGGER zz_test_batch_failure ON app_envs; DROP FUNCTION reject_test_batch_value()`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ApplyEnvBatch(t.Context(), change); err != nil {
		t.Fatalf("rolled-back request ID could not be retried: %v", err)
	}
}

func TestAtomicEnvBatchReceiptFailureRollsBackInvalidation(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	account, app := newBatchApp(t, store)
	_, err := pool.Exec(t.Context(), `CREATE FUNCTION reject_test_batch_receipt() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected receipt failure'; END $$;
CREATE TRIGGER zz_test_batch_failure BEFORE INSERT ON app_env_batch_receipts FOR EACH ROW EXECUTE FUNCTION reject_test_batch_receipt()`)
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	change := state.EnvBatchChange{AccountID: account.ID, AppID: app.ID, Scope: "default", Actor: "receipt-fault", Guard: true, Limits: api.MustLimitsFor(api.PlanFree), Request: api.EnvBatchRequest{RequestID: uuid.NewString(), ExpectedRevision: &zero, Values: map[string]string{"FIRST": "one"}}}
	if _, err := store.ApplyEnvBatch(t.Context(), change); err == nil {
		t.Fatal("receipt failure ignored")
	}
	var effects int
	err = pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM app_envs WHERE app_id=$1)+(SELECT count(*) FROM app_config_revisions WHERE app_id=$1)+(SELECT count(*) FROM app_config_guards WHERE app_id=$1)+(SELECT count(*) FROM app_runtime_config_changes WHERE app_id=$1)+(SELECT count(*) FROM app_runtime_config_scope_changes WHERE app_id=$1)`, app.ID).Scan(&effects)
	if err != nil || effects != 0 {
		t.Fatalf("failed receipt left %d effects: %v", effects, err)
	}
}

func TestAtomicEnvBatchMigrationReplayAndRollback(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	raw, err := migrations.FS.ReadFile("20261103171930575_atomic_environment_batches.sql")
	if err != nil {
		t.Fatal(err)
	}
	beforeDown, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing Down")
	}
	_, up, ok := strings.Cut(beforeDown, "-- +goose Up")
	if !ok {
		t.Fatal("missing Up")
	}
	for _, body := range []string{up, down, down, up, up} {
		if _, err := pool.Exec(t.Context(), body); err != nil {
			t.Fatal(err)
		}
	}
	store := state.NewPgStore(pool)
	account, app := newBatchApp(t, store)
	zero := int64(0)
	_, err = store.ApplyEnvBatch(t.Context(), state.EnvBatchChange{AccountID: account.ID, AppID: app.ID, Scope: "default", Actor: "migration-test", Limits: api.MustLimitsFor(api.PlanFree), Request: api.EnvBatchRequest{RequestID: uuid.NewString(), ExpectedRevision: &zero, Values: map[string]string{"FIRST": "one"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), down); err == nil {
		t.Fatal("Down erased active batch history")
	}
	var receipts int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM app_env_batch_receipts`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("refused rollback changed receipts: %d %v", receipts, err)
	}
}
