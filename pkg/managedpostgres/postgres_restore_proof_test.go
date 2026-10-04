// adr: 566
package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func cloneRestoreProofFixture(t *testing.T, status ProviderStatus) (*PostgresStore, *Service, *cloneLineageProvider, Database, Database) {
	t.Helper()
	store, pool, ctx, accountID := postgresStoreFixture(t)
	intent := state.NewPgStore(pool)
	project, err := intent.CreateProject(ctx, state.Project{AccountID: accountID, Slug: "restore-proof"})
	if err != nil {
		t.Fatal(err)
	}
	op, err := intent.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: accountID, ProjectID: project.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "restore-proof", SourceRevisionHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	provider := &cloneLineageProvider{fakeProvider: fakeProvider{capabilities: testCapabilities(), provisionStatus: ProviderStatusReady, inspectStatus: ProviderStatusReady}}
	service, err := NewService(testRegistry(t, provider, nil), store, ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	source, err := service.Create(ctx, CreateRequest{AccountID: accountID, Name: "source", Spec: testSpec()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	input := postgresTestDatabase(accountID, "stage-db", now)
	input.BackendID, input.BackendFingerprint = source.BackendID, source.BackendFingerprint
	input.RestoreSourceDatabaseID, input.RestoreSourceResourceID, input.RestorePointInTime = source.ID, source.ProviderResourceID, now.Add(-time.Minute)
	target, _, err := store.Reserve(ctx, input, 3)
	if err != nil {
		t.Fatal(err)
	}
	// Model the private operation-owned reservation in this control-plane test.
	if _, err := pool.Exec(ctx, `update managed_postgres_databases set environment_clone_operation_id=$2 where id=$1`, target.ID, op.ID); err != nil {
		t.Fatal(err)
	}
	provider.provisionStatus = status
	target, err = store.Get(ctx, accountID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	return store, service, provider, source, target
}

func TestPostgresCloneRestoreReceiptCommitsWithReadinessAndSurvivesLostAck(t *testing.T) {
	store, service, provider, source, target := cloneRestoreProofFixture(t, ProviderStatusReady)
	service.store = &lostRestoreProofAckStore{PostgresStore: store, fail: true}
	if _, err := service.Reconcile(t.Context(), target.AccountID, target.ID); err == nil {
		t.Fatal("lost acknowledgement was reported as successful")
	}
	actual, err := store.Get(t.Context(), target.AccountID, target.ID)
	if err != nil || actual.State != StateReady || actual.ObservedGeneration != 1 || actual.LeaseToken != "" {
		t.Fatalf("committed ready state = %+v, %v", actual, err)
	}
	proof, err := store.GetCloneRestoreProof(t.Context(), target.AccountID, target.ID)
	if err != nil || validateCloneRestoreProof(actual, proof) != nil || proof.SourceDatabaseID != source.ID || proof.Lineage.SourceResourceID != source.ProviderResourceID || !proof.Lineage.PointInTime.Equal(target.RestorePointInTime) {
		t.Fatalf("committed receipt = %+v, %v", proof, err)
	}
	if _, err := store.GetCloneRestoreProof(t.Context(), uuid.NewString(), target.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("another account read the restore receipt")
	}
	// Restart with a new service and no provider memory. Recovery uses the
	// original atomic receipt and never issues a second physical restore.
	provider.lineage = nil
	restarted, err := NewService(service.registry, store, ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	again, err := restarted.Reconcile(t.Context(), target.AccountID, target.ID)
	if err != nil || again.ID != target.ID || provider.restoreCalls != 1 || provider.inspectCalls != 0 {
		t.Fatalf("recovery created or reinspected a target: %+v, %v, calls=%d/%d", again, err, provider.restoreCalls, provider.inspectCalls)
	}
	if _, err := store.pool.Exec(t.Context(), `delete from managed_postgres_restore_proofs where database_id=$1`, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Reconcile(t.Context(), target.AccountID, target.ID); !errors.Is(err, ErrConflict) || provider.restoreCalls != 1 {
		t.Fatalf("legacy ready state substituted for missing receipt: %v", err)
	}
}

type lostRestoreProofAckStore struct {
	*PostgresStore
	fail bool
}

func (s *lostRestoreProofAckStore) FinishCloneRestoreProvision(ctx context.Context, database Database, observed ObservedDatabase, now time.Time) (Database, error) {
	result, err := s.PostgresStore.FinishCloneRestoreProvision(ctx, database, observed, now)
	if err == nil && s.fail {
		s.fail = false
		return Database{}, errors.New("restore receipt acknowledgement lost")
	}
	return result, err
}

func TestPostgresCloneRestoreReceiptRejectsGenerationAndIdentityDrift(t *testing.T) {
	store, service, provider, _, target := cloneRestoreProofFixture(t, ProviderStatusReady)
	ready, err := service.Reconcile(t.Context(), target.AccountID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`update managed_postgres_databases set provider_resource_id='changed' where id=$1`,
		`update managed_postgres_databases set data_resource_id='changed' where id=$1`,
		`update managed_postgres_databases set restore_source_resource_id='changed' where id=$1`,
		`update managed_postgres_databases set restore_point_in_time=restore_point_in_time+interval '1 microsecond' where id=$1`,
		`update managed_postgres_databases set backend_fingerprint=repeat('f',64) where id=$1`,
		`update managed_postgres_databases set storage_limit_bytes=storage_limit_bytes+1 where id=$1`,
		`update managed_postgres_databases set desired_generation=desired_generation+1, observed_generation=observed_generation+1 where id=$1`,
		`update managed_postgres_restore_proofs set spec='{}'::jsonb||jsonb_build_object('Region','other') where database_id=$1`,
	} {
		tx, err := store.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(t.Context(), statement, target.ID); err != nil {
			_ = tx.Rollback(t.Context())
			t.Fatal(err)
		}
		// Validate the same durable query inside the editing transaction.
		id, _ := postgresUUID(target.ID)
		account, _ := postgresUUID(target.AccountID)
		_, err = new(sqlc.Queries).ReadManagedPostgresCloneRestoreProof(t.Context(), tx, sqlc.ReadManagedPostgresCloneRestoreProofParams{DatabaseID: id, AccountID: account})
		_ = tx.Rollback(t.Context())
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("drift retained matching receipt: %v", err)
		}
	}
	if _, err := service.Reconcile(t.Context(), target.AccountID, target.ID); err != nil || provider.restoreCalls != 1 {
		t.Fatalf("rolled-back fixture edit affected original receipt: %v", err)
	}
	if ready.ProviderResourceID == target.RestoreSourceResourceID {
		t.Fatal("restore retained production physical identity")
	}
}

func TestPostgresCloneRestoreReceiptChecksLeaseAfterUnchangedRowLockWait(t *testing.T) {
	store, service, provider, _, target := cloneRestoreProofFixture(t, ProviderStatusPending)
	pending, err := service.Reconcile(t.Context(), target.AccountID, target.ID)
	if err != nil || pending.ProviderResourceID == "" {
		t.Fatalf("pending restore = %+v, %v", pending, err)
	}
	if _, err := store.pool.Exec(t.Context(), `update managed_postgres_databases set retry_at=clock_timestamp() where id=$1`, target.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claimed, err := store.Claim(t.Context(), target.AccountID, target.ID, "proof-worker", StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var expiry time.Time
	if err := store.pool.QueryRow(t.Context(), `update managed_postgres_databases set lease_until=clock_timestamp()+interval '500 milliseconds' where id=$1 returning lease_until`, target.ID).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	locker, err := store.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locker.Rollback(context.WithoutCancel(t.Context())) }()
	if _, err := locker.Exec(t.Context(), `select id from managed_postgres_databases where id=$1 for update`, target.ID); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := store.FinishCloneRestoreProvision(ctx, claimed, ObservedDatabase{ProviderResourceID: claimed.ProviderResourceID, DataResourceID: claimed.ProviderResourceID,
			Status: ProviderStatusReady, Spec: claimed.Spec, RestoreLineage: provider.lineage}, now)
		result <- err
	}()
	blocked := false
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if err := store.pool.QueryRow(ctx, `select exists(select 1 from pg_stat_activity where wait_event_type='Lock'
            and position('ReadProjectEnvironmentCloneDatabaseSource' in query)>0)`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("restore receipt writer never reached the held row lock")
	}
	if wait := time.Until(expiry) + 20*time.Millisecond; wait > 0 {
		time.Sleep(wait)
	}
	if err := locker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrConflict) {
		t.Fatalf("expired worker published readiness after unchanged lock wait: %v", err)
	}
	actual, err := store.Get(ctx, target.AccountID, target.ID)
	if err != nil || actual.State != StateProvisioning || actual.ObservedGeneration != 0 {
		t.Fatalf("failed lease check advanced readiness: %+v, %v", actual, err)
	}
	var count int
	if err := store.pool.QueryRow(ctx, `select count(*) from managed_postgres_restore_proofs where database_id=$1`, target.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("expired worker committed a restore receipt: %d, %v", count, err)
	}
}

func TestPostgresCloneRestoreReceiptConflictRollsBackReadiness(t *testing.T) {
	store, service, provider, _, target := cloneRestoreProofFixture(t, ProviderStatusPending)
	pending, err := service.Reconcile(t.Context(), target.AccountID, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(t.Context(), `update managed_postgres_databases set retry_at=clock_timestamp() where id=$1`, target.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	claimed, err := store.Claim(t.Context(), target.AccountID, target.ID, "proof-worker", StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := json.Marshal(target.Spec)
	if _, err := store.pool.Exec(t.Context(), `insert into managed_postgres_restore_proofs(database_id,account_id,operation_id,
        backend_id,backend_fingerprint,provider_resource_id,source_database_id,source_resource_id,point_in_time,spec,generation,observed_at)
        values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,1,clock_timestamp())`,
		target.ID, target.AccountID, target.EnvironmentCloneOperationID, target.BackendID, target.BackendFingerprint,
		pending.ProviderResourceID, target.RestoreSourceDatabaseID, target.RestoreSourceResourceID,
		target.RestorePointInTime.Add(time.Second), spec); err != nil {
		t.Fatal(err)
	}
	_, err = store.FinishCloneRestoreProvision(t.Context(), claimed, ObservedDatabase{ProviderResourceID: claimed.ProviderResourceID, DataResourceID: claimed.ProviderResourceID,
		Status: ProviderStatusReady, Spec: claimed.Spec, RestoreLineage: provider.lineage}, now)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting original receipt was adopted or overwritten: %v", err)
	}
	actual, err := store.Get(t.Context(), target.AccountID, target.ID)
	if err != nil || actual.State != StateProvisioning || actual.ObservedGeneration != 0 || actual.LeaseToken != claimed.LeaseToken {
		t.Fatalf("failed receipt insertion partially committed readiness: %+v, %v", actual, err)
	}
	var originalPoint time.Time
	if err := store.pool.QueryRow(t.Context(), `select point_in_time from managed_postgres_restore_proofs where database_id=$1`, target.ID).Scan(&originalPoint); err != nil || !originalPoint.Equal(target.RestorePointInTime.Add(time.Second)) {
		t.Fatal("conflicting receipt was replaced")
	}
}
