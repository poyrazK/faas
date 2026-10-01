// adr: 391 — native preparation, lease fencing, cancellation, and rollback.
package managedpostgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresCutoverPinsStageAndCancel(t *testing.T) {
	store, pool, ctx, account := postgresStoreFixture(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	app, err := state.NewPgStore(pool).CreateApp(ctx, state.App{AccountID: account, Slug: "cutover-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	source := postgresReadyDatabase(t, store, account, "source", now.Add(-time.Hour))
	targetInput := postgresTestDatabase(account, "restore", now.Add(-time.Hour))
	targetInput.RestoreSourceDatabaseID = source.ID
	targetInput.RestoreSourceResourceID = source.ProviderResourceID
	targetInput.RestorePointInTime = now.Add(-time.Minute)
	target, _, err := store.Reserve(ctx, targetInput, 10)
	if err != nil {
		t.Fatal(err)
	}
	target, err = store.Claim(ctx, account, target.ID, "restore", StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RecordProviderResource(ctx, target.ID, "restore", "restored-resource", now); err != nil {
		t.Fatal(err)
	}
	target, err = store.FinishProvision(ctx, target.ID, "restore", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, access := range []CredentialAccess{CredentialReadWrite, CredentialMigration} {
		b := testBinding(account, source.ID, app.ID, uuid.NewString(), now)
		b.Access = access
		b.Scope = "default"
		if access == CredentialMigration {
			b.EnvironmentKey = "MIGRATION_DATABASE_URL"
		}
		if _, _, err = store.ReserveBinding(ctx, b); err != nil {
			t.Fatal(err)
		}
		claimed, err := store.ClaimBinding(ctx, account, b.ID, "binding", BindingStateProvisioning, now, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		sink := &postgresBindingCredentialSink{store: state.NewPgStore(pool)}
		ref, err := sink.Put(ctx, claimed, bindingTestMaterial())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.FinishBindingProvision(ctx, b.ID, "binding", "provider-role", ref, now); err != nil {
			t.Fatal(err)
		}
	}
	r := PrepareCutoverRequest{ID: uuid.NewString(), AccountID: account, AppID: app.ID, Scope: "default", SourceDatabaseID: source.ID, TargetDatabaseID: target.ID}
	var wg sync.WaitGroup
	results := make(chan Cutover, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _, err := store.ReserveCutover(ctx, r, now)
			if err != nil {
				errs <- err
			} else {
				results <- c
			}
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("reservation retry failed: %v", err)
	}
	if len(results) != 8 {
		t.Fatal("idempotent reservation lost results")
	}
	c := <-results
	if len(c.Credentials) != 2 {
		t.Fatalf("did not stage all binding modes: %+v", c)
	}
	if _, err = store.ClaimDelete(ctx, account, target.ID, "delete", now, now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("target delete: %v", err)
	}
	if _, _, err = store.BeginBindingRotation(ctx, account, c.Credentials[0].SourceBindingID, uuid.NewString(), now); !errors.Is(err, ErrConflict) {
		t.Fatalf("rotation: %v", err)
	}
	if _, err = store.ClaimBinding(ctx, account, c.Credentials[0].SourceBindingID, "delete-binding", BindingStateDeleting, now, now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("binding delete: %v", err)
	}
	b := testBinding(account, source.ID, app.ID, uuid.NewString(), now)
	b.EnvironmentKey = "EXTRA_URL"
	if _, _, err = store.ReserveBinding(ctx, b); !errors.Is(err, ErrConflict) {
		t.Fatalf("new binding: %v", err)
	}
	claim, err := store.ClaimCutover(ctx, account, c.ID, "first", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ClaimCutover(ctx, account, c.ID, "duplicate", now, now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate lease won")
	}
	sealed := SealedCredential{ProviderIdentityID: "stage-role", Ref: "stage-ref", Ciphertext: []byte("age-envelope"), Kid: "recipient", ValueHash: "hmac"}
	now = now.Add(time.Minute + time.Second)
	recovered, err := store.ClaimCutover(ctx, account, c.ID, "recovered", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveCutoverCredential(ctx, claim, c.Credentials[0], sealed, now); !errors.Is(err, ErrConflict) {
		t.Fatal("expired worker wrote staged secret")
	}
	if err = store.SaveCutoverCredential(ctx, recovered, c.Credentials[0], sealed, now); err != nil {
		t.Fatal(err)
	}
	claim, err = store.ClaimCutover(ctx, account, c.ID, "second", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveCutoverCredential(ctx, claim, c.Credentials[1], sealed, now); err != nil {
		t.Fatal(err)
	}
	c, err = store.GetCutover(ctx, account, c.ID)
	if err != nil || c.State != CutoverPrepared {
		t.Fatalf("prepare: %+v %v", c, err)
	}
	if _, err = store.GetCutover(ctx, uuid.NewString(), c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-account cutover leaked")
	}
	now = testPostgresCutoverVerification(t, store, c, now)
	var published int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM app_secrets WHERE managed_postgres_binding_id=ANY($1::uuid[])`, []string{c.Credentials[0].ID, c.Credentials[1].ID}).Scan(&published); err != nil || published != 0 {
		t.Fatal("staged envelope reached app_secrets")
	}
	migration, err := migrations.FS.ReadFile("20261001144956825_managed_postgres_cutover_preparation.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(migration), "-- +goose Down")
	down := strings.Split(strings.Split(parts[1], "-- +goose StatementBegin")[1], "-- +goose StatementEnd")[0]
	if _, err = pool.Exec(ctx, down); err == nil {
		t.Fatal("rollback discarded live staged provider identities")
	}
	if err = state.NewPgStore(pool).MarkAccountDeletionPending(ctx, account); err == nil {
		t.Fatal("account deletion bypassed live resource protection")
	}
	// Seed an already-deleted owner as a restored/legacy catalog could contain.
	// Current deletion guards must still reject this transition for live resources.
	if _, err = pool.Exec(ctx, `ALTER TABLE accounts DISABLE TRIGGER account_managed_postgres_databases_guard`); err != nil {
		t.Fatal(err)
	}
	if err = state.NewPgStore(pool).MarkAccountDeletionPending(ctx, account); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `ALTER TABLE accounts ENABLE TRIGGER account_managed_postgres_databases_guard`); err != nil {
		t.Fatal(err)
	}
	due, err := store.DueCutovers(ctx, false, 20, now)
	if err != nil || len(due) != 1 || due[0].State != CutoverCancelling {
		t.Fatalf("deleted-account cleanup was not discovered with provisioning disabled: %+v %v", due, err)
	}
	if _, err = store.CancelCutover(ctx, account, c.ID, now); err != nil {
		t.Fatal(err)
	}
	for i, member := range c.Credentials {
		claim, err = store.ClaimCutover(ctx, account, c.ID, "cleanup-"+member.ID, now, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if err = store.RevokeCutoverCredential(ctx, claim, member, now); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err = store.ClaimDelete(ctx, account, target.ID, "early-delete", now, now.Add(time.Minute)); !errors.Is(err, ErrConflict) {
				t.Fatal("cleanup unpinned target before all members revoked")
			}
		}
	}
	c, err = store.GetCutover(ctx, account, c.ID)
	if err != nil || c.State != CutoverCancelled {
		t.Fatalf("cancel: %+v %v", c, err)
	}
	for _, member := range c.Credentials {
		if len(member.Sealed.Ciphertext) != 0 {
			t.Fatal("revoked credential retained ciphertext")
		}
	}
	// An app deleted after preparation must also discover cleanup with the gate closed.
	if _, err = pool.Exec(ctx, `UPDATE accounts SET status='active' WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	r.ID = uuid.NewString()
	appCutover, _, err := store.ReserveCutover(ctx, r, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = state.NewPgStore(pool).DeleteApp(ctx, app.ID); err == nil {
		t.Fatal("app deletion bypassed live binding protection")
	}
	if _, err = pool.Exec(ctx, `ALTER TABLE apps DISABLE TRIGGER app_managed_postgres_bindings_guard`); err != nil {
		t.Fatal(err)
	}
	if err = state.NewPgStore(pool).DeleteApp(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `ALTER TABLE apps ENABLE TRIGGER app_managed_postgres_bindings_guard`); err != nil {
		t.Fatal(err)
	}
	due, err = store.DueCutovers(ctx, false, 20, now)
	if err != nil || len(due) != 1 || due[0].ID != appCutover.ID || due[0].State != CutoverCancelling {
		t.Fatalf("deleted-app cleanup was not discovered: %v", err)
	}
	for _, member := range appCutover.Credentials {
		claim, err = store.ClaimCutover(ctx, account, appCutover.ID, "app-cleanup-"+member.ID, now, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if err = store.RevokeCutoverCredential(ctx, claim, member, now); err != nil {
			t.Fatal(err)
		}
	}
	// Account erasure prunes binding tombstones before database tombstones.
	if _, err = pool.Exec(ctx, `DELETE FROM app_secrets WHERE account_id=$1`, account); err != nil {
		t.Fatal(err)
	}
	for _, member := range c.Credentials {
		binding, err := store.ClaimBinding(ctx, account, member.SourceBindingID, "erase", BindingStateDeleting, now, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.FinishBindingDelete(ctx, binding.ID, binding.LeaseToken, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = pool.Exec(ctx, `DELETE FROM managed_postgres_bindings WHERE account_id=$1 AND state='deleted'`, account); err != nil {
		t.Fatalf("cancelled credential history blocked binding tombstone erasure: %v", err)
	}
	if _, err = store.ClaimDelete(ctx, account, target.ID, "delete", now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Later migrations own dependent app fences. Roll back in version order
	// rather than dropping the preparation table out from under its foreign key.
	fenceUp, fenceDown := cutoverMigrationStatements(t, "20261001175350459_managed_postgres_cutover_admission_fence.sql")
	taskFenceUp, taskFenceDown := cutoverMigrationStatements(t, "20261001184558948_managed_postgres_cutover_task_fence.sql")
	if _, err = pool.Exec(ctx, taskFenceDown); err != nil {
		t.Fatalf("rollback task admission fence after cleanup: %v", err)
	}
	if _, err = pool.Exec(ctx, fenceDown); err != nil {
		t.Fatalf("rollback admission fence after cleanup: %v", err)
	}
	if _, err = pool.Exec(ctx, down); err != nil {
		t.Fatalf("rollback after cleanup: %v", err)
	}
	up := strings.Split(strings.Split(parts[0], "-- +goose StatementBegin")[1], "-- +goose StatementEnd")[0]
	if _, err = pool.Exec(ctx, up); err != nil {
		t.Fatalf("reapply migration: %v", err)
	}
	verificationUp, _ := cutoverMigrationStatements(t, "20261001155101225_managed_postgres_cutover_verification.sql")
	for _, forward := range []string{verificationUp, fenceUp, taskFenceUp} {
		if _, err = pool.Exec(ctx, forward); err != nil {
			t.Fatalf("reapply dependent migration: %v", err)
		}
	}
}

func testPostgresCutoverVerification(t *testing.T, store *PostgresStore, c Cutover, now time.Time) time.Time {
	t.Helper()
	ctx := context.Background()
	verifying, err := store.RequestCutoverVerification(ctx, c.AccountID, c.ID, now)
	if err != nil || verifying.State != CutoverVerifying {
		t.Fatal("verification request", err)
	}
	migration, err := migrations.FS.ReadFile("20261001155101225_managed_postgres_cutover_verification.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(migration), "-- +goose Down")
	down := strings.Split(strings.Split(parts[1], "-- +goose StatementBegin")[1], "-- +goose StatementEnd")[0]
	up := strings.Split(strings.Split(parts[0], "-- +goose StatementBegin")[1], "-- +goose StatementEnd")[0]
	if _, err = store.pool.Exec(ctx, down); err == nil {
		t.Fatal("rollback accepted verifying intent")
	}
	claim, err := store.ClaimCutover(ctx, c.AccountID, c.ID, "probe", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	changed := claim.Credentials[0]
	changed.Sealed.Ciphertext = []byte("replaced")
	if err = store.SaveCutoverVerification(ctx, claim, changed, now); !errors.Is(err, ErrConflict) {
		t.Fatal("envelope mismatch", err)
	}
	if err = store.SaveCutoverVerification(ctx, claim, claim.Credentials[0], now); err != nil {
		t.Fatal(err)
	}
	repeat, err := store.RequestCutoverVerification(ctx, c.AccountID, c.ID, now)
	if err != nil || repeat.LeaseToken != "probe" || repeat.Credentials[0].VerifiedAt.IsZero() {
		t.Fatal("verification idempotency", err)
	}
	if err = store.SaveCutoverVerification(ctx, claim, claim.Credentials[1], now); err != nil {
		t.Fatal(err)
	}
	verified, err := store.GetCutover(ctx, c.AccountID, c.ID)
	if err != nil || !verified.VerificationFresh(now) {
		t.Fatal("verification completion", err)
	}
	if _, err = store.pool.Exec(ctx, down); err == nil {
		t.Fatal("rollback accepted verified intent")
	}
	if _, err = store.RequestCutoverVerification(ctx, uuid.NewString(), c.ID, now); !errors.Is(err, ErrNotFound) {
		t.Fatal("verification tenant isolation", err)
	}
	testPostgresCutoverVerificationLockWait(t, store, c)
	_, err = store.RequestCutoverVerification(ctx, c.AccountID, c.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	claim, err = store.ClaimCutover(ctx, c.AccountID, c.ID, "cancelled-probe", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := store.CancelCutover(ctx, c.AccountID, c.ID, now)
	if err != nil || !cancelled.VerifiedAt.IsZero() {
		t.Fatal("cancel timestamp constraint", err)
	}
	if err = store.SaveCutoverVerification(ctx, claim, claim.Credentials[0], now); !errors.Is(err, ErrConflict) {
		t.Fatal("cancelled probe saved", err)
	}
	if _, err = store.pool.Exec(ctx, down); err != nil {
		t.Fatal("rollback after cancel", err)
	}
	if _, err = store.pool.Exec(ctx, up); err != nil {
		t.Fatal("reapply verification", err)
	}
	return now.Add(time.Minute + time.Second)
}

// A caller's pre-lock timestamp cannot keep a SQL lease alive across a wait.
func testPostgresCutoverVerificationLockWait(t *testing.T, store *PostgresStore, c Cutover) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	now := time.Now().UTC()
	if _, err := store.RequestCutoverVerification(ctx, c.AccountID, c.ID, now); err != nil {
		t.Fatal(err)
	}
	until := now.Add(100 * time.Millisecond)
	claim, err := store.ClaimCutover(ctx, c.AccountID, c.ID, "blocked-probe", now, until)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = tx.Exec(ctx, "SELECT id FROM managed_postgres_cutovers WHERE id=$1 FOR UPDATE", c.ID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- store.SaveCutoverVerification(ctx, claim, claim.Credentials[0], now) }()
	time.Sleep(max(time.Until(until)+20*time.Millisecond, 0))
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, ErrConflict) {
		t.Fatal("lock wait extended expired lease", err)
	}
	current, err := store.GetCutover(ctx, c.AccountID, c.ID)
	if err != nil || !current.Credentials[0].VerifiedAt.IsZero() {
		t.Fatal("late evidence persisted", err)
	}
}
