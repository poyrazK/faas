//go:build !no_pg

// adr: 568
package state_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCloneObjectCredentialPreparationIsAtomicAndRecoverable(t *testing.T) {
	s, _, pool := pgWithPool(t)
	cloneObjectCredentialPreparationContract(t, s, func(clone state.ProjectEnvironmentClone, target state.ObjectS3ComputeBindingCreateRequest) {
		cloneObjectPreparationConcurrentRekeyContract(t, s, pool, clone, target.Credential)
	})
}

// ADR-568: a host rekey can update a prepared signing row independently of
// application intent. Materialization must wait for it and reject stale proof.
func cloneObjectPreparationConcurrentRekeyContract(t *testing.T, s *state.PgStore, pool *pgxpool.Pool, clone state.ProjectEnvironmentClone, credential state.ObjectS3Credential) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := writer.Exec(ctx, `update object_storage_s3_credentials set secret_sealed=$2 where id=$1`, credential.ID, []byte("concurrent-rekey")); err != nil {
		t.Fatal(err)
	}
	var writerPID int32
	if err := writer.QueryRow(ctx, `select pg_backend_pid()`).Scan(&writerPID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		_, _, err := s.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(api.PlanPro))
		result <- err
	}()
	defer func() { cancel(); _ = writer.Rollback(context.WithoutCancel(ctx)); workers.Wait() }()
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, `select exists (select 1 from pg_stat_activity
		 where datname=current_database() and $1::integer=any(pg_blocking_pids(pid))
		 and query like '%LockProjectEnvironmentClonePreparedObjectCredential%')`, writerPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("materialization bypassed in-flight rekey: %v", err)
		case <-ctx.Done():
			t.Fatal("materialization did not reach signing-row verification")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, state.ErrConflict) {
		t.Fatalf("materialization accepted changed signing key: %v", err)
	}
	if _, err := s.ProjectEnvironmentBySlug(ctx, clone.AccountID, clone.ProjectID, clone.TargetSlug); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("concurrent rekey created target environment: %v", err)
	}
	if _, err := pool.Exec(ctx, `update object_storage_s3_credentials set secret_sealed=$2 where id=$1`, credential.ID, credential.SecretSealed); err != nil {
		t.Fatal(err)
	}
}
