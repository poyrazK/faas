// adr: 466 — durable admission fences do not constitute writer-drain evidence.
package managedpostgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/state"
)

func admissionCutoverFixture(t *testing.T) (*PostgresStore, *state.PgStore, Cutover, state.ComputeNode) {
	t.Helper()
	s, pool, ctx, account := postgresStoreFixture(t)
	ps := state.NewPgStore(pool)
	app, err := ps.CreateApp(ctx, state.App{AccountID: account, Slug: "fence-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	source := postgresReadyDatabase(t, s, account, "source", now.Add(-time.Hour))
	input := postgresTestDatabase(account, "restore", now.Add(-time.Hour))
	input.RestoreSourceDatabaseID, input.RestoreSourceResourceID, input.RestorePointInTime = source.ID, source.ProviderResourceID, now.Add(-time.Minute)
	target, _, err := s.Reserve(ctx, input, 10)
	if err != nil {
		t.Fatal(err)
	}
	target, err = s.Claim(ctx, account, target.ID, "restore", StateProvisioning, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordProviderResource(ctx, target.ID, "restore", "restored-resource", now); err != nil {
		t.Fatal(err)
	}
	target, err = s.FinishProvision(ctx, target.ID, "restore", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"DATABASE_URL", "MIGRATION_DATABASE_URL"} {
		b := testBinding(account, source.ID, app.ID, uuid.NewString(), now)
		b.EnvironmentKey, b.Scope = key, "default"
		if key == "MIGRATION_DATABASE_URL" {
			b.Access = CredentialMigration
		}
		if _, _, err = s.ReserveBinding(ctx, b); err != nil {
			t.Fatal(err)
		}
		claimed, err := s.ClaimBinding(ctx, account, b.ID, "binding", BindingStateProvisioning, now, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		ref, err := (&postgresBindingCredentialSink{store: ps}).Put(ctx, claimed, bindingTestMaterial())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.FinishBindingProvision(ctx, b.ID, "binding", "provider-role", ref, now); err != nil {
			t.Fatal(err)
		}
	}
	c, _, err := s.ReserveCutover(ctx, PrepareCutoverRequest{ID: uuid.NewString(), AccountID: account, AppID: app.ID, Scope: "default", SourceDatabaseID: source.ID, TargetDatabaseID: target.ID}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range c.Credentials {
		claim, err := s.ClaimCutover(ctx, account, c.ID, uuid.NewString(), now, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SaveCutoverCredential(ctx, claim, m, SealedCredential{ProviderIdentityID: "role-" + m.ID, Ref: "ref-" + m.ID, Ciphertext: []byte("age-envelope"), Kid: "recipient", ValueHash: "hash"}, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.RequestCutoverVerification(ctx, account, c.ID, now); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimCutover(ctx, account, c.ID, "verify", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range claim.Credentials {
		if err = s.SaveCutoverVerification(ctx, claim, m, now); err != nil {
			t.Fatal(err)
		}
	}
	c, err = s.GetCutover(ctx, account, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	role := "compute-node"
	node, err := ps.CreateComputeNode(ctx, state.ComputeNode{Name: "fence-" + uuid.NewString(), TargetURL: "tcp://127.0.0.1:50051", VPCPUs: 4, MemMB: 4096, MaxConcurrency: 20, AdmissionCeilingMB: 4096, VCPUBudget: 4, Active: true, Role: &role})
	if err != nil {
		t.Fatal(err)
	}
	return s, ps, c, node
}

func TestPostgresCutoverAdmissionFenceCancellation(t *testing.T) {
	s, ps, c, node := admissionCutoverFixture(t)
	ctx := context.Background()
	warm, err := ps.CreateInstance(ctx, c.AppID, "", string(state.StateWarm), 256, node.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	// Worker residency now reserves a concrete deployment before admission.
	deployment, err := ps.CreateDeployment(ctx, state.Deployment{AppID: c.AppID,
		Scope: "default", Kind: state.DeploymentKindImage, Status: state.DeployLive,
		ImageDigest: "sha256:" + strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	fence, err := s.FenceCutoverAdmission(ctx, c.AccountID, c.ID)
	if err != nil || fence.FencedAt.IsZero() {
		t.Fatalf("fence: %+v %v", fence, err)
	}
	repeat, err := s.FenceCutoverAdmission(ctx, c.AccountID, c.ID)
	if err != nil || repeat != fence {
		t.Fatalf("idempotency: %+v %v", repeat, err)
	}
	if _, err = s.FenceCutoverAdmission(ctx, uuid.NewString(), c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant isolation: %v", err)
	}
	for _, mode := range []string{"normal", "mirror", "worker", "job"} {
		if _, err = ps.CreateInstanceWithMode(ctx, c.AppID, deployment.ID, "cold_booting", 256, node.ID, "", mode); !errors.Is(err, state.ErrManagedPostgresAdmissionFenced) {
			t.Fatalf("admitted %s: %v", mode, err)
		}
	}
	if _, err = ps.PublishInstanceRuntime(ctx, warm.ID, "warm", "fenced-netns", "10.0.0.1", 20001); !errors.Is(err, state.ErrManagedPostgresAdmissionFenced) {
		t.Fatalf("warm promotion: %v", err)
	}
	if err = ps.UpdateInstanceState(ctx, warm.ID, "parked"); err != nil {
		t.Fatal("cleanup blocked", err)
	}
	if _, err = s.pool.Exec(ctx, `UPDATE apps SET managed_postgres_admission_cutover_id=NULL,managed_postgres_admission_fenced_at=NULL WHERE id=$1`, c.AppID); err == nil {
		t.Fatal("live fence released manually")
	}
	_, down := cutoverMigrationStatements(t, "20261001175350459_managed_postgres_cutover_admission_fence.sql")
	if _, err = s.pool.Exec(ctx, down); err == nil {
		t.Fatal("rollback discarded a live admission fence")
	}
	now := time.Now().UTC()
	if _, err = s.RequestCutoverVerification(ctx, c.AccountID, c.ID, now); err != nil {
		t.Fatal("refresh fenced credentials", err)
	}
	if repeat, err = s.FenceCutoverAdmission(ctx, c.AccountID, c.ID); err != nil || repeat != fence {
		t.Fatal("refresh lost existing barrier", err)
	}
	if _, err = s.CancelCutover(ctx, c.AccountID, c.ID, now); err != nil {
		t.Fatal(err)
	}
	for i, m := range c.Credentials {
		claim, err := s.ClaimCutover(ctx, c.AccountID, c.ID, uuid.NewString(), now, now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if err = s.RevokeCutoverCredential(ctx, claim, m, now); err != nil {
			t.Fatal(err)
		}
		fenced, err := ps.ManagedPostgresAdmissionFenced(ctx, c.AppID)
		if err != nil || fenced != (i < len(c.Credentials)-1) {
			t.Fatalf("revocation %d fence=%v: %v", i, fenced, err)
		}
	}
	if _, err = ps.CreateInstance(ctx, c.AppID, "", "cold_booting", 256, node.ID, ""); err != nil {
		t.Fatal("cancel did not resume admission", err)
	}
}

func TestPostgresCutoverAdmissionRejectsStaleVerification(t *testing.T) {
	s, _, c, _ := admissionCutoverFixture(t)
	ctx := context.Background()
	// A fresh group stamp cannot hide a stale member, nor can a future stamp
	// extend the wall-clock evidence window.
	for _, age := range []time.Duration{-6 * time.Minute, time.Minute} {
		if _, err := s.pool.Exec(ctx, `UPDATE managed_postgres_cutover_credentials SET verified_at=$2 WHERE id=$1`, c.Credentials[0].ID, time.Now().Add(age)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.FenceCutoverAdmission(ctx, c.AccountID, c.ID); !errors.Is(err, ErrConflict) {
			t.Fatalf("age %s: %v", age, err)
		}
	}
}

func TestPostgresCutoverAdmissionFencesOldTransactions(t *testing.T) {
	for _, isolation := range []pgx.TxIsoLevel{pgx.ReadCommitted, pgx.RepeatableRead} {
		t.Run(string(isolation), func(t *testing.T) {
			s, _, c, node := admissionCutoverFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			blocker, err := s.pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Rollback(context.Background()) }()
			if _, err = blocker.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, c.AppID); err != nil {
				t.Fatal(err)
			}
			old, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: isolation})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = old.Rollback(context.Background()) }()
			// Establish the old snapshot before the fence UPDATE commits.
			if _, err = old.Exec(ctx, `SELECT status FROM apps WHERE id=$1`, c.AppID); err != nil {
				t.Fatal(err)
			}
			pid := old.Conn().PgConn().PID()
			done := make(chan error, 1)
			go func() {
				_, insertErr := old.Exec(ctx, `INSERT INTO instances(app_id,state,ram_mb,node_id) VALUES($1,'cold_booting',256,$2)`, c.AppID, node.ID)
				done <- insertErr
			}()
			waitForAdmissionLock(t, ctx, s, pid)
			if _, err = blocker.Exec(ctx, `UPDATE apps SET managed_postgres_admission_cutover_id=$2,managed_postgres_admission_fenced_at=clock_timestamp() WHERE id=$1`, c.AppID, c.ID); err != nil {
				t.Fatal(err)
			}
			if err = blocker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err = <-done
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || (pgErr.ConstraintName != "managed_postgres_cutover_admission_fenced" && pgErr.Code != "40001") {
				t.Fatalf("old transaction admitted a VM: %v", err)
			}
		})
	}
}

func TestPostgresCutoverAdmissionRechecksEvidenceAfterLockWait(t *testing.T) {
	s, _, c, _ := admissionCutoverFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	blocker, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err = blocker.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, c.AppID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.FenceCutoverAdmission(ctx, c.AccountID, c.ID); done <- err }()
	waitForAdmissionAppLock(t, ctx, s)
	// Evidence that was fresh when the call began is no longer valid after
	// the lock wait. Keep the intent's timestamp fresh to check every member.
	if _, err = blocker.Exec(ctx, `UPDATE managed_postgres_cutover_credentials SET verified_at=clock_timestamp()-interval '6 minutes' WHERE id=$1`, c.Credentials[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, ErrConflict) {
		t.Fatalf("stale post-lock evidence accepted: %v", err)
	}
}

func TestPostgresCutoverAdmissionExpiredCancellationCannotReleaseFence(t *testing.T) {
	s, ps, c, _ := admissionCutoverFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.FenceCutoverAdmission(ctx, c.AccountID, c.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := s.CancelCutover(ctx, c.AccountID, c.ID, now); err != nil {
		t.Fatal(err)
	}
	first, err := s.ClaimCutover(ctx, c.AccountID, c.ID, "first-revoke", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeCutoverCredential(ctx, first, c.Credentials[0], now); err != nil {
		t.Fatal(err)
	}
	last, err := s.ClaimCutover(ctx, c.AccountID, c.ID, "last-revoke", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err = blocker.Exec(ctx, `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, c.AppID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.RevokeCutoverCredential(ctx, last, c.Credentials[1], now) }()
	waitForAdmissionAppLock(t, ctx, s)
	// Still later than the caller's pre-lock timestamp, but earlier than
	// the database clock at credential save. No replacement worker is needed
	// to demonstrate that the old token no longer grants permission to save.
	if _, err = blocker.Exec(ctx, `UPDATE managed_postgres_cutovers SET lease_until=$2 WHERE id=$1`, c.ID, now.Add(time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, ErrConflict) {
		t.Fatalf("expired cancellation saved: %v", err)
	}
	if fenced, err := ps.ManagedPostgresAdmissionFenced(ctx, c.AppID); err != nil || !fenced {
		t.Fatal("expired worker reopened admission", err)
	}
	now = time.Now().UTC()
	recovered, err := s.ClaimCutover(ctx, c.AccountID, c.ID, "recovered-revoke", now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeCutoverCredential(ctx, recovered, c.Credentials[1], now); err != nil {
		t.Fatal(err)
	}
	if fenced, err := ps.ManagedPostgresAdmissionFenced(ctx, c.AppID); err != nil || fenced {
		t.Fatal("recovered cancellation kept barrier", err)
	}
}

func waitForAdmissionAppLock(t *testing.T, ctx context.Context, s *PostgresStore) {
	t.Helper()
	for {
		var waiting bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%LockManagedPostgresCutoverAdmissionApp%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("fence did not reach app lock")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func waitForAdmissionLock(t *testing.T, ctx context.Context, s *PostgresStore, pid uint32) {
	t.Helper()
	for {
		var waiting bool
		if err := s.pool.QueryRow(ctx, `SELECT coalesce(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("admission did not reach lock", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func cutoverMigrationStatements(t *testing.T, name string) (string, string) {
	t.Helper()
	migration, err := migrations.FS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(migration), "-- +goose Down")
	body := func(part string) string {
		return strings.Split(strings.Split(part, "-- +goose StatementBegin")[1], "-- +goose StatementEnd")[0]
	}
	return body(parts[0]), body(parts[1])
}

// adr: 493 — serving receipts prove the eligible source keys actually delivered.
// adr: 462 — release-only migration credentials cannot be serving evidence.
func TestPostgresRuntimeConfigReceiptServingCredentialAudience(t *testing.T) {
	s, ps, c, _ := admissionCutoverFixture(t)
	ctx := t.Context()
	rows, err := ps.ListAppSecretsInScope(ctx, c.AccountID, c.AppID, "default")
	if err != nil {
		t.Fatal(err)
	}
	versions := map[string]int64{}
	for _, row := range rows {
		versions[row.Key] = row.DeliveryVersion
	}
	boundary, _, err := state.RuntimeConfigChangedAtForScope(ctx, ps, c.AppID, "default")
	if err != nil {
		t.Fatal(err)
	}
	serving := state.RuntimeConfigInputs{Scope: "default", Boundary: boundary, Variables: map[string]string{},
		SecretVersions: map[string]int64{"default/DATABASE_URL": versions["DATABASE_URL"]},
		SecretRefs:     map[string]string{"DATABASE_URL": "secret:DATABASE_URL"}, AllSecrets: true}
	assertAudience := func() {
		t.Helper()
		for _, implicit := range []bool{false, true} {
			inputs := serving
			if implicit {
				inputs.SecretRefs = map[string]string{}
			}
			if fresh, err := ps.RuntimeConfigInputsFresh(ctx, c.AppID, inputs); err != nil || !fresh {
				t.Fatalf("serving implicit=%v fresh=%v err=%v", implicit, fresh, err)
			}
		}
		for _, alias := range []bool{false, true} {
			forged := serving
			forged.AllSecrets = false
			forged.SecretVersions = map[string]int64{"default/DATABASE_URL": versions["DATABASE_URL"],
				"default/MIGRATION_DATABASE_URL": versions["MIGRATION_DATABASE_URL"]}
			forged.SecretRefs = map[string]string{"DATABASE_URL": "secret:DATABASE_URL"}
			if alias {
				forged.SecretRefs["SCHEMA_DSN"] = "secret:MIGRATION_DATABASE_URL"
			}
			if fresh, err := ps.RuntimeConfigInputsFresh(ctx, c.AppID, forged); err != nil || fresh {
				t.Fatalf("migration alias=%v fresh=%v err=%v", alias, fresh, err)
			}
		}
	}
	assertAudience()
	_, down := cutoverMigrationStatements(t, "20261003174600000_environment_runtime_receipt_secret_audience.sql")
	if _, err = s.pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	assertAudience()
}
