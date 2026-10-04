//go:build !no_pg

// adr: 581
package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Exercise the real worker's durable preparation through the materialization
// and publication boundaries; a caller's IDs/counts are never a substitute.
func capturedClonePostgresMaterializationContract(t *testing.T, store *state.PgStore, pool *pgxpool.Pool, lease state.ProjectEnvironmentCloneLease, ids []string, count int) {
	t.Helper()
	ctx := context.Background()
	op := lease.Operation
	clone := state.ProjectEnvironmentClone{AccountID: op.AccountID, ProjectID: op.ProjectID, SourceSlug: op.SourceEnvironment, TargetSlug: op.TargetEnvironment,
		CloneOperationID: op.ID, CloneOperationRevision: op.Revision, ManagedBindingsPrepared: true, PreparedManagedBindingIDs: ids, PreparedManagedSecretCount: count}
	bad := clone
	bad.PreparedManagedBindingIDs = append([]string{}, ids...)
	bad.PreparedManagedBindingIDs[0] = uuid.NewString()
	if _, _, err := store.CloneProjectEnvironment(ctx, bad, api.MustLimitsFor(api.PlanPro)); !errors.Is(err, state.ErrProjectEnvironmentCloneManagedValueProof) {
		t.Fatalf("matching count with forged PostgreSQL preparation materialized: %v", err)
	}
	views, err := store.ProjectEnvironmentCloneBindings(ctx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, prepared, err := store.ProjectEnvironmentClonePostgresBindingForLease(ctx, lease, views[0].Postgres[0].ID)
	if err != nil || prepared == nil {
		t.Fatalf("preparation before materialization: %v", err)
	}
	for _, fault := range []struct {
		name, change, restore string
		value                 any
	}{
		{"provider_identity", "update managed_postgres_bindings set provider_identity_id='changed' where id=$1", "update managed_postgres_bindings set provider_identity_id=$2 where id=$1", prepared.Binding.ProviderIdentityID},
		{"generation", "update managed_postgres_bindings set credential_generation=2 where id=$1", "update managed_postgres_bindings set credential_generation=$2 where id=$1", int64(1)},
		{"key", "update managed_postgres_bindings set environment_key='WRONG_DATABASE_URL' where id=$1", "update managed_postgres_bindings set environment_key=$2 where id=$1", prepared.Binding.EnvironmentKey},
		{"reservation_hash", "update project_environment_clone_postgres_bindings set reservation_hash=repeat('f',64) where target_binding_id=$1", "update project_environment_clone_postgres_bindings set reservation_hash=$2 where target_binding_id=$1", ""},
		{"ciphertext", "update app_secrets set ciphertext='\\x6368616e676564' where managed_postgres_binding_id=$1", "update app_secrets set ciphertext=$2 where managed_postgres_binding_id=$1", prepared.Secret.Ciphertext},
	} {
		t.Run("materialization_"+fault.name, func(t *testing.T) {
			if fault.name == "reservation_hash" {
				if err := pool.QueryRow(ctx, "select reservation_hash from project_environment_clone_postgres_bindings where target_binding_id=$1", prepared.Binding.ID).Scan(&fault.value); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := pool.Exec(ctx, fault.change, prepared.Binding.ID); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := pool.Exec(ctx, fault.restore, prepared.Binding.ID, fault.value); err != nil {
					t.Error(err)
				}
			}()
			if _, _, err := store.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(api.PlanPro)); !errors.Is(err, state.ErrProjectEnvironmentCloneManagedValueProof) {
				t.Fatalf("changed %s passed PostgreSQL preparation proof: %v", fault.name, err)
			}
			if _, err := store.ProjectEnvironmentBySlug(ctx, op.AccountID, op.ProjectID, op.TargetEnvironment); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("changed preparation created target environment: %v", err)
			}
		})
	}
	clonePostgresPreparationConcurrentChangeContract(t, store, pool, clone, *prepared)
	if _, result, err := store.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(api.PlanPro)); err != nil || result.SecretsCopied != 2 || result.VariablesCopied != 2 {
		t.Fatalf("authentic PostgreSQL materialization lost customer values: %+v, %v", result, err)
	}
	resources := append([]state.ProjectEnvironmentCloneResource{}, op.Resources...)
	appIDs := map[string]string{}
	for _, resource := range resources {
		if resource.Kind == "variables" {
			appIDs[resource.Name] = resource.SourceID
		}
	}
	for i, resource := range resources {
		if resource.Kind != "workload" {
			continue
		}
		appID := appIDs[resource.Name]
		spec, err := store.ProjectEnvironmentWorkloadSpec(ctx, op.AccountID, op.ProjectID, op.TargetEnvironment, appID)
		if err != nil {
			t.Fatal(err)
		}
		deployment, err := store.CreateDeploymentForEnvironmentClone(ctx, op.AccountID, op.ProjectID, op.ID, op.Revision, appID, spec.Hash)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLiveDark(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		resources[i].TargetID, resources[i].Status = deployment.ID, "ready"
	}
	advance := func() error {
		_, err := store.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationPublishing, op.Revision, resources, "")
		return err
	}
	if err := advance(); !errors.Is(err, state.ErrProjectEnvironmentCloneResourcePublicationProof) {
		t.Fatalf("authentic PostgreSQL values did not reach independent resource guard: %v", err)
	}
	if _, err := pool.Exec(ctx, "update app_secrets set ciphertext='\\x6368616e676564' where managed_postgres_binding_id=$1", prepared.Binding.ID); err != nil {
		t.Fatal(err)
	}
	if err := advance(); !errors.Is(err, state.ErrProjectEnvironmentCloneManagedValueProof) {
		t.Fatalf("publication accepted altered PostgreSQL envelope: %v", err)
	}
	if _, err := pool.Exec(ctx, "update app_secrets set ciphertext=$2 where managed_postgres_binding_id=$1", prepared.Binding.ID, prepared.Secret.Ciphertext); err != nil {
		t.Fatal(err)
	}
	// Check every workload before the resource guard, including one later in
	// the catalogue. Ready receipts cannot justify extra or edited values.
	for _, view := range views {
		for _, fault := range []struct{ name, change, restore string }{
			{"changed_variable", "update app_envs set value='edited' where app_id=$1 and scope=$2 and key='CAPTURED'", "update app_envs set value='captured-value' where app_id=$1 and scope=$2 and key='CAPTURED'"},
			{"extra_variable", "insert into app_envs(account_id,app_id,scope,key,value) select account_id,id,$2,'EXTRA','extra' from apps where id=$1", "delete from app_envs where app_id=$1 and scope=$2 and key='EXTRA'"},
		} {
			if _, err := pool.Exec(ctx, fault.change, view.AppID, op.TargetEnvironment); err != nil {
				t.Fatal(err)
			}
			err := advance()
			if !errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrProjectEnvironmentCloneResourcePublicationProof) || !strings.Contains(err.Error(), "target values differ") {
				t.Fatalf("%s on %s passed value publication: %v", fault.name, view.AppID, err)
			}
			if _, err := pool.Exec(ctx, fault.restore, view.AppID, op.TargetEnvironment); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := store.ActiveProjectReleaseSet(ctx, op.AccountID, op.ProjectID, op.TargetEnvironment); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("incomplete full isolation acquired serving release: %v", err)
	}
}

// Provider maintenance does not take the app intent lock. The materialization
// transaction must lock the actual binding and reject a concurrent identity edit.
func clonePostgresPreparationConcurrentChangeContract(t *testing.T, store *state.PgStore, pool *pgxpool.Pool, clone state.ProjectEnvironmentClone, prepared state.ProjectEnvironmentClonePostgresBindingPreparation) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := writer.Exec(ctx, "update managed_postgres_bindings set provider_identity_id='concurrent-change' where id=$1", prepared.Binding.ID); err != nil {
		t.Fatal(err)
	}
	writerPID := int32(writer.Conn().PgConn().PID())
	result := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		_, _, err := store.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(api.PlanPro))
		result <- err
	}()
	defer func() { cancel(); _ = writer.Rollback(context.WithoutCancel(ctx)); workers.Wait() }()
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, `select exists(select 1 from pg_stat_activity where datname=current_database()
		 and $1::integer=any(pg_blocking_pids(pid)) and query like '%LockProjectEnvironmentClonePostgresBinding%')`, writerPID).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("materialization bypassed in-flight binding edit: %v", err)
		case <-ctx.Done():
			t.Fatal("materialization did not reach binding-row verification")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, state.ErrConflict) {
		t.Fatalf("materialization accepted changed provider identity: %v", err)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, clone.AccountID, clone.ProjectID, clone.TargetSlug); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("concurrent binding edit created target: %v", err)
	}
	if _, err := pool.Exec(ctx, "update managed_postgres_bindings set provider_identity_id=$2 where id=$1", prepared.Binding.ID, prepared.Binding.ProviderIdentityID); err != nil {
		t.Fatal(err)
	}
}
