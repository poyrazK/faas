//go:build !no_pg

// adr: 590
package state_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneConfigurationFenceFixture struct {
	store     *state.PgStore
	pool      *pgxpool.Pool
	ctx       context.Context
	app       state.App
	dep       state.Deployment
	lease     state.ProjectEnvironmentCloneLease
	flags     state.FeatureFlagVersion
	flagScope state.FeatureFlagScope
}

func newCloneConfigurationFenceFixture(t *testing.T) cloneConfigurationFenceFixture {
	t.Helper()
	s, ctx, pool := pgWithPool(t)
	a, err := s.CreateAccount(ctx, "configuration-fence@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "configuration-fence"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "configuration-fence-api", WorkloadName: "api", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:original"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, d.ID, "/configuration.ext4", "layers/"+d.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAppEnvInScope(ctx, a.ID, app.ID, "production", "VERSION", "original"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAppSecretWithKidAndValueHashInScope(ctx, a.ID, app.ID, "production", "TOKEN", "kid", strings.Repeat("a", 16), []byte("sealed-original")); err != nil {
		t.Fatal(err)
	}
	env, err := s.ProjectEnvironmentBySlug(ctx, a.ID, p.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	flagScope := state.FeatureFlagScope{AccountID: a.ID, ProjectID: p.ID, EnvironmentID: env.ID}
	flags, _ := cloneFlagFixture(t, s, flagScope)
	op, err := s.CreateCapturedProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneCaptureRequest{AccountID: a.ID, ProjectID: p.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "configuration-fence"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil || l.Operation.ID != op.ID {
		t.Fatalf("claim configuration fence: %v", err)
	}
	l.Operation, err = s.AdvanceProjectEnvironmentCloneOperation(ctx, a.ID, p.ID, op.ID, op.Status, state.CloneOperationCapturing, l.Operation.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	return cloneConfigurationFenceFixture{store: s, pool: pool, ctx: ctx, app: app, dep: d, lease: l, flags: flags, flagScope: flagScope}
}

func assertConfigurationFenceError(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, state.ErrProjectEnvironmentCloneConfigurationFenced) {
		return
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55000" || pgErr.ConstraintName != "clone_configuration_write_fenced" {
		t.Fatalf("configuration mutation bypassed the source hold: %v", err)
	}
}

func (f *cloneConfigurationFenceFixture) compensate(t *testing.T) {
	t.Helper()
	op := f.lease.Operation
	var err error
	f.lease.Operation, err = f.store.AdvanceProjectEnvironmentCloneOperation(f.ctx, op.AccountID, op.ProjectID, op.ID, op.Status,
		state.CloneOperationCompensating, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
}

func waitForConfigurationFenceLock(t *testing.T, pool *pgxpool.Pool, prefix string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `select exists(select 1 from pg_stat_activity
            where datname=current_database() and pid<>pg_backend_pid() and wait_event_type='Lock' and strpos(query,$1)>0)`, prefix).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("configuration fence did not reach the expected SQL lock wait")
		}
	}
}

func TestPgCloneConfigurationFenceBlocksCurrentCatalogueAndPhantomRows(t *testing.T) {
	f := newCloneConfigurationFenceFixture(t)
	op := f.lease.Operation
	first, err := f.store.AcquireProjectEnvironmentCloneConfigurationFence(f.ctx, f.lease)
	if err != nil || first.OperationID != op.ID || first.SourceRevisionHash != op.SourceRevisionHash || first.Generation != 2 || first.HeldAt.IsZero() {
		t.Fatalf("source configuration was not held: %+v %v", first, err)
	}
	for _, test := range []struct {
		name string
		edit func() error
	}{
		{"variable_update", func() error {
			return f.store.UpsertAppEnvInScope(f.ctx, op.AccountID, f.app.ID, "production", "VERSION", "changed")
		}},
		{"variable_insert", func() error {
			return f.store.UpsertAppEnvInScope(f.ctx, op.AccountID, f.app.ID, "production", "NEW", "changed")
		}},
		{"variable_delete", func() error {
			_, err := f.pool.Exec(f.ctx, "delete from app_envs where app_id=$1", f.app.ID)
			return err
		}},
		{"secret", func() error {
			return f.store.UpsertAppSecretWithKidAndValueHashInScope(f.ctx, op.AccountID, f.app.ID, "production", "TOKEN", "kid", strings.Repeat("b", 16), []byte("sealed-changed"))
		}},
		{"settings", func() error {
			_, err := f.pool.Exec(f.ctx, "update apps set ram_mb=512 where id=$1", f.app.ID)
			return err
		}},
		{"project", func() error {
			_, err := f.pool.Exec(f.ctx, "update projects set slug='changed' where id=$1", op.ProjectID)
			return err
		}},
		{"environment", func() error {
			_, err := f.pool.Exec(f.ctx, "delete from project_environments where project_id=$1 and slug='production'", op.ProjectID)
			return err
		}},
		{"other_scope_shared_app", func() error {
			return f.store.UpsertAppEnvInScope(f.ctx, op.AccountID, f.app.ID, "other-stage", "NEW", "changed")
		}},
		{"config_version", func() error {
			values, hash, err := api.NormalizeProjectEnvironmentConfig([]byte(`{"changed":true}`))
			if err == nil {
				_, err = f.store.CreateProjectEnvironmentConfigVersion(f.ctx, state.ProjectEnvironmentConfig{AccountID: op.AccountID, ProjectID: op.ProjectID, EnvironmentSlug: "production", Values: values, ConfigHash: hash})
			}
			return err
		}},
		{"flags", func() error {
			flags := f.flags
			flags.Flags[0].Description = "changed"
			_, err := f.store.UpdateFeatureFlags(f.ctx, state.FeatureFlagUpdate{Scope: f.flagScope, ExpectedVersion: flags.Version, Config: flags.Config, Actor: "developer"})
			return err
		}},
		{"artifact", func() error {
			_, err := f.store.CreateDeployment(f.ctx, state.Deployment{AppID: f.app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:changed"})
			return err
		}},
		{"release", func() error {
			_, err := f.store.PublishProjectReleaseSet(f.ctx, op.AccountID, op.ProjectID, "production", 3600, []state.ProjectReleaseMember{{AppID: f.app.ID, DeploymentID: f.dep.ID}})
			return err
		}},
		{"workload_insert", func() error {
			_, err := f.store.CreateApp(f.ctx, state.App{AccountID: op.AccountID, ProjectID: op.ProjectID, Slug: "new-api", WorkloadName: "new-api", Type: state.AppTypeApp})
			return err
		}},
		{"workload_delete", func() error {
			_, err := f.pool.Exec(f.ctx, "update apps set status='deleted' where id=$1", f.app.ID)
			return err
		}},
		{"empty_policy_insert", func() error {
			_, err := f.pool.Exec(f.ctx, "insert into project_environment_route_policies(account_id,project_id,app_id,environment_slug,only_allow_declared_routes,declared_routes) values($1,$2,$3,'production',false,'[]')", op.AccountID, op.ProjectID, f.app.ID)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) { assertConfigurationFenceError(t, test.edit()) })
	}
	if same, err := f.store.AcquireProjectEnvironmentCloneConfigurationFence(f.ctx, f.lease); err != nil || !reflect.DeepEqual(same, first) {
		t.Fatalf("recovery replaced the original hold: %+v %v", same, err)
	}
	if live, err := f.store.ValidateProjectEnvironmentCloneSourceConfigurationForLease(f.ctx, f.lease); err != nil || live.Hash != op.SourceRevisionHash {
		t.Fatalf("rejected mutations changed source configuration: %v", err)
	}
	other, err := f.store.CreateProject(f.ctx, state.Project{AccountID: op.AccountID, Slug: "other-project"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := f.store.CreateApp(f.ctx, state.App{AccountID: op.AccountID, ProjectID: other.ID, Slug: "other-api", WorkloadName: "api", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpsertAppEnvInScope(f.ctx, op.AccountID, app.ID, "production", "VERSION", "allowed"); err != nil {
		t.Fatal(err)
	}
	_, err = f.pool.Exec(f.ctx, "update apps set project_id=$2 where id=$1", app.ID, op.ProjectID)
	assertConfigurationFenceError(t, err)
	_, err = f.pool.Exec(f.ctx, "update apps set project_id=$2 where id=$1", f.app.ID, other.ID)
	assertConfigurationFenceError(t, err)
	if _, err := f.store.AdvanceProjectEnvironmentCloneOperation(f.ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, nil, ""); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("capture advanced with a configuration hold: %v", err)
	}
}

func TestPgCloneConfigurationFenceHandoffAndOwnedAbandonment(t *testing.T) {
	f := newCloneConfigurationFenceFixture(t)
	first, err := f.store.AcquireProjectEnvironmentCloneConfigurationFence(f.ctx, f.lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AbandonProjectEnvironmentCloneConfigurationFence(f.ctx, f.lease); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("capture released its configuration hold: %v", err)
	}
	f.compensate(t)
	stale := f.lease
	if err := f.store.ReleaseProjectEnvironmentCloneLease(f.ctx, f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = f.store.ClaimNextProjectEnvironmentClone(f.ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ProjectEnvironmentCloneConfigurationFenceForLease(f.ctx, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker read hold: %v", err)
	}
	if err := f.store.AbandonProjectEnvironmentCloneConfigurationFence(f.ctx, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale worker released hold: %v", err)
	}
	if current, err := f.store.ProjectEnvironmentCloneConfigurationFenceForLease(f.ctx, f.lease); err != nil || !reflect.DeepEqual(current, first) {
		t.Fatalf("handoff changed original hold: %+v %v", current, err)
	}
	assertConfigurationFenceError(t, f.store.UpsertAppEnvInScope(f.ctx, first.AccountID, f.app.ID, "production", "VERSION", "still-held"))
	if err := f.store.AbandonProjectEnvironmentCloneConfigurationFence(f.ctx, f.lease); err != nil {
		t.Fatal(err)
	}
	if err := f.store.AbandonProjectEnvironmentCloneConfigurationFence(f.ctx, f.lease); err != nil {
		t.Fatalf("committed release did not replay: %v", err)
	}
	if _, err := f.store.ProjectEnvironmentCloneConfigurationFenceForLease(f.ctx, f.lease); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("abandoned hold remained owned: %v", err)
	}
	if err := f.store.UpsertAppEnvInScope(f.ctx, first.AccountID, f.app.ID, "production", "VERSION", "allowed"); err != nil {
		t.Fatal(err)
	}
	op := f.lease.Operation
	if _, err := f.store.AdvanceProjectEnvironmentCloneOperation(f.ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensated, op.Revision, nil, ""); err != nil {
		t.Fatalf("abandoned configuration hold blocked compensation: %v", err)
	}
}

func TestPgCloneConfigurationFenceRejectsDriftAndMissingAuthority(t *testing.T) {
	for _, fault := range []string{"variable", "stale_lease", "missing_root", "missing_clock", "missing_guard"} {
		t.Run(fault, func(t *testing.T) {
			f := newCloneConfigurationFenceFixture(t)
			op := f.lease.Operation
			switch fault {
			case "variable":
				if err := f.store.UpsertAppEnvInScope(f.ctx, op.AccountID, f.app.ID, "production", "VERSION", "changed"); err != nil {
					t.Fatal(err)
				}
			case "stale_lease":
				f.lease.Token = uuid.NewString()
			case "missing_root":
				if _, err := f.pool.Exec(f.ctx, "delete from project_environment_clone_configuration_captures where operation_id=$1", op.ID); err != nil {
					t.Fatal(err)
				}
			case "missing_clock":
				if _, err := f.pool.Exec(f.ctx, "delete from project_environment_clone_configuration_clock"); err != nil {
					t.Fatal(err)
				}
			case "missing_guard":
				if _, err := f.pool.Exec(f.ctx, "delete from project_environment_clone_configuration_guards where project_id=$1", op.ProjectID); err != nil {
					t.Fatal(err)
				}
			}
			if fence, err := f.store.AcquireProjectEnvironmentCloneConfigurationFence(f.ctx, f.lease); err == nil || fence != (state.ProjectEnvironmentCloneConfigurationFence{}) {
				t.Fatalf("unknown source produced a hold: %+v %v", fence, err)
			}
			var held int
			if err := f.pool.QueryRow(f.ctx, "select count(*) from project_environment_clone_configuration_guards where operation_id=$1", op.ID).Scan(&held); err != nil || held != 0 {
				t.Fatalf("failed acquisition committed a hold: %d %v", held, err)
			}
		})
	}
}

func TestPgCloneConfigurationFenceWaitsForAdmittedConfigurationWriter(t *testing.T) {
	f := newCloneConfigurationFenceFixture(t)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(f.ctx)) }()
	if _, err := tx.Exec(f.ctx, "update app_envs set value='changed' where app_id=$1", f.app.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.store.AcquireProjectEnvironmentCloneConfigurationFence(f.ctx, f.lease)
		done <- err
	}()
	waitForConfigurationFenceLock(t, f.pool, "UPDATE project_environment_clone_configuration_clock")
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatalf("pre-existing writer was omitted from live validation: %v", err)
	}
	if err := f.store.UpsertAppEnvInScope(f.ctx, f.app.AccountID, f.app.ID, "production", "VERSION", "allowed"); err != nil {
		t.Fatal(err)
	}
}

func TestPgCloneConfigurationFenceWaitsForAdmittedProjectDeletion(t *testing.T) {
	f := newCloneConfigurationFenceFixture(t)
	writer, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(context.WithoutCancel(f.ctx)) }()
	// Project deletion cascades into the operation. Acquisition cannot own
	// that operation while waiting for an already admitted writer's clock.
	if _, err := writer.Exec(f.ctx, "select generation from project_environment_clone_configuration_clock for share"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.store.AcquireProjectEnvironmentCloneConfigurationFence(f.ctx, f.lease)
		done <- err
	}()
	waitForConfigurationFenceLock(t, f.pool, "UPDATE project_environment_clone_configuration_clock")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := writer.Exec(ctx, "delete from projects where id=$1", f.app.ProjectID); err != nil {
		t.Fatalf("capture deadlocked an admitted project deletion: %v", err)
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrConflict) {
		t.Fatalf("capture retained a deleted source: %v", err)
	}
	var holds int
	if err := f.pool.QueryRow(f.ctx, "select count(*) from project_environment_clone_configuration_guards where operation_id=$1", f.lease.Operation.ID).Scan(&holds); err != nil || holds != 0 {
		t.Fatalf("deleted source retained a capture hold: %d %v", holds, err)
	}
}

func TestPgCloneConfigurationFenceOpenScopeReadsDoNotBlockAdmittedWriter(t *testing.T) {
	for _, phase := range []string{"read", "abandon"} {
		t.Run(phase, func(t *testing.T) {
			f := newCloneConfigurationFenceFixture(t)
			if phase == "abandon" {
				f.compensate(t)
			}
			writer, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = writer.Rollback(context.WithoutCancel(f.ctx)) }()
			if _, err := writer.Exec(f.ctx, "select generation from project_environment_clone_configuration_clock for share"); err != nil {
				t.Fatal(err)
			}
			// A deletion cascade may own the guard before reaching the operation.
			if _, err := writer.Exec(f.ctx, "select generation from project_environment_clone_configuration_guards where project_id=$1 for update", f.app.ProjectID); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if phase == "read" {
				if _, err := f.store.ProjectEnvironmentCloneConfigurationFenceForLease(ctx, f.lease); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("open scope read blocked an admitted writer: %v", err)
				}
			} else if err := f.store.AbandonProjectEnvironmentCloneConfigurationFence(ctx, f.lease); err != nil {
				t.Fatalf("unowned abandonment blocked an admitted writer: %v", err)
			}
			if _, err := writer.Exec(f.ctx, "delete from projects where id=$1", f.app.ProjectID); err != nil {
				t.Fatalf("open scope read blocked project deletion: %v", err)
			}
			if err := writer.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPgCloneConfigurationFenceAvoidsConfigurationRowLockInversion(t *testing.T) {
	for _, table := range []string{"app", "project"} {
		t.Run(table, func(t *testing.T) {
			f := newCloneConfigurationFenceFixture(t)
			writer, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = writer.Rollback(context.WithoutCancel(f.ctx)) }()
			lock, mutation, id := "select id from apps where id=$1 for update", "update apps set max_concurrency=2 where id=$1", f.app.ID
			if table == "project" {
				lock, mutation, id = "select id from projects where id=$1 for update", "update projects set slug='waiting' where id=$1", f.app.ProjectID
			}
			if _, err := writer.Exec(f.ctx, lock, id); err != nil {
				t.Fatal(err)
			}
			blocker, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Rollback(context.WithoutCancel(f.ctx)) }()
			if _, err := blocker.Exec(f.ctx, "select generation from project_environment_clone_configuration_guards where project_id=$1 for share", f.app.ProjectID); err != nil {
				t.Fatal(err)
			}
			acquired := make(chan error, 1)
			go func() {
				_, err := f.store.AcquireProjectEnvironmentCloneConfigurationFence(f.ctx, f.lease)
				acquired <- err
			}()
			// Acquisition must reach its guard even while the writer owns a
			// project/App row. Then the mutation waits for acquisition's clock.
			waitForConfigurationFenceLock(t, f.pool, "UPDATE project_environment_clone_configuration_guards")
			mutated := make(chan error, 1)
			go func() {
				_, err := writer.Exec(f.ctx, mutation, id)
				mutated <- err
			}()
			waitForConfigurationFenceLock(t, f.pool, mutation)
			if err := blocker.Commit(f.ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-acquired; err != nil {
				t.Fatalf("configuration row held by a waiting writer deadlocked acquisition: %v", err)
			}
			assertConfigurationFenceError(t, <-mutated)
			if err := writer.Rollback(f.ctx); err != nil {
				t.Fatal(err)
			}
			if captured, err := f.store.ValidateProjectEnvironmentCloneSourceConfigurationForLease(f.ctx, f.lease); err != nil || captured.Hash != f.lease.Operation.SourceRevisionHash {
				t.Fatalf("waiting mutation changed frozen configuration: %v", err)
			}
		})
	}
}

func TestPgCloneConfigurationFenceRejectsOldTransactionSnapshots(t *testing.T) {
	for _, isolation := range []pgx.TxIsoLevel{pgx.RepeatableRead, pgx.Serializable} {
		t.Run(string(isolation), func(t *testing.T) {
			f := newCloneConfigurationFenceFixture(t)
			tx, err := f.pool.BeginTx(f.ctx, pgx.TxOptions{IsoLevel: isolation})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(f.ctx)) }()
			var generation int64
			if err := tx.QueryRow(f.ctx, "select generation from project_environment_clone_configuration_clock").Scan(&generation); err != nil || generation != 1 {
				t.Fatalf("original synchronization clock: %d %v", generation, err)
			}
			if _, err := f.store.AcquireProjectEnvironmentCloneConfigurationFence(f.ctx, f.lease); err != nil {
				t.Fatal(err)
			}
			_, err = tx.Exec(f.ctx, "update app_envs set value='stale-snapshot' where app_id=$1", f.app.ID)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "40001" {
				t.Fatalf("old transaction hid the committed configuration hold: %v", err)
			}
		})
	}
}

func TestPgCloneConfigurationFenceLeaseExpiryAfterSynchronizationWait(t *testing.T) {
	f := newCloneConfigurationFenceFixture(t)
	blocker, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.WithoutCancel(f.ctx)) }()
	if _, err := blocker.Exec(f.ctx, "select generation from project_environment_clone_configuration_clock for share"); err != nil {
		t.Fatal(err)
	}
	var until time.Time
	if err := f.pool.QueryRow(f.ctx, "update project_environment_clone_operations set lease_until=clock_timestamp()+interval '500 milliseconds' where id=$1 returning lease_until", f.lease.Operation.ID).Scan(&until); err != nil {
		t.Fatal(err)
	}
	f.lease.ExpiresAt = time.Now().Add(time.Minute) // SQL authority wins over caller time
	done := make(chan error, 1)
	go func() {
		_, err := f.store.AcquireProjectEnvironmentCloneConfigurationFence(f.ctx, f.lease)
		done <- err
	}()
	waitForConfigurationFenceLock(t, f.pool, "UPDATE project_environment_clone_configuration_clock")
	timer := time.NewTimer(time.Until(until) + 20*time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-f.ctx.Done():
		t.Fatal(f.ctx.Err())
	}
	if err := blocker.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, state.ErrConflict) {
		t.Fatalf("expired lease acquired a source hold after waiting: %v", err)
	}
	var held int
	if err := f.pool.QueryRow(f.ctx, "select count(*) from project_environment_clone_configuration_guards where operation_id=$1", f.lease.Operation.ID).Scan(&held); err != nil || held != 0 {
		t.Fatalf("expired worker retained configuration hold: %d %v", held, err)
	}
}

func TestPgCloneConfigurationFenceMigrationRoundTripAndOwnedDownRefusal(t *testing.T) {
	raw, err := migrations.FS.ReadFile("20261006133007028_environment_clone_configuration_guards.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("missing configuration guard downgrade")
	}
	// ADR-590's protection successor refines this function. Exercise the
	// actual ordered downgrade/upgrade chain and compare the final schema.
	successorRaw, err := migrations.FS.ReadFile("20261006170801000_object_protection_capture_admission.sql")
	if err != nil {
		t.Fatal(err)
	}
	successor := strings.SplitN(string(successorRaw), "-- +goose Down", 2)
	if len(successor) != 2 {
		t.Fatal("missing protection successor downgrade")
	}
	f := newCloneConfigurationFenceFixture(t)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(f.ctx)) }()
	const shape = `SELECT jsonb_build_array(
        (SELECT jsonb_agg(jsonb_build_array(attrelid::regclass::text,attname,format_type(atttypid,atttypmod),attnotnull)
          ORDER BY attrelid::regclass::text,attnum) FROM pg_attribute
          WHERE attrelid IN ('project_environment_clone_configuration_guards'::regclass,'project_environment_clone_configuration_clock'::regclass)
          AND attnum>0 AND NOT attisdropped),
        (SELECT jsonb_agg(jsonb_build_array(conrelid::regclass::text,conname,pg_get_constraintdef(oid))
          ORDER BY conrelid::regclass::text,conname) FROM pg_constraint
          WHERE conrelid IN ('project_environment_clone_configuration_guards'::regclass,'project_environment_clone_configuration_clock'::regclass)),
        (SELECT jsonb_agg(pg_get_triggerdef(oid) ORDER BY tgrelid::regclass::text,tgname) FROM pg_trigger
          WHERE tgname IN ('clone_configuration_write_fence','initialize_clone_configuration_guard')),
        (SELECT jsonb_agg(pg_get_functiondef(oid) ORDER BY proname) FROM pg_proc
          WHERE pronamespace=current_schema()::regnamespace
          AND proname IN ('guard_clone_configuration_mutation','assert_clone_configuration_mutable','initialize_clone_configuration_guard'))
        )::text`
	var before, after string
	if err := tx.QueryRow(f.ctx, shape).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(f.ctx, successor[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(f.ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(f.ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(f.ctx, successor[0]); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(f.ctx, shape).Scan(&after); err != nil || before != after {
		t.Fatalf("configuration guard migration changed schema on round trip: %v", err)
	}
	var seeded bool
	if err := tx.QueryRow(f.ctx, `SELECT EXISTS(SELECT 1 FROM project_environment_clone_configuration_guards
        WHERE project_id=$1 AND account_id=$2 AND state='open' AND generation=1)`, f.app.ProjectID, f.app.AccountID).Scan(&seeded); err != nil || !seeded {
		t.Fatalf("existing project lost its configuration guard: %v", err)
	}
	if err := tx.Rollback(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AcquireProjectEnvironmentCloneConfigurationFence(f.ctx, f.lease); err != nil {
		t.Fatal(err)
	}
	tx, err = f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(f.ctx, parts[1])
	_ = tx.Rollback(f.ctx)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "clone_configuration_guards_down_no_ownership" {
		t.Fatalf("downgrade discarded an owned source configuration hold: %v", err)
	}
	assertConfigurationFenceError(t, f.store.UpsertAppEnvInScope(f.ctx, f.app.AccountID, f.app.ID, "production", "VERSION", "still-held"))
}
