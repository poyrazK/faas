//go:build !no_pg

package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-581: provider resource definitions and policy rows share the same
// repeatable-read capture as their source values and immutable workloads.
func TestPgCloneBindingCatalogueSurvivesSourceDeletion(t *testing.T) {
	s, _, _ := pgWithPool(t)
	cloneBindingCatalogueSurvivesSourceDeletion(t, s)
}

// ADR-581: incomplete resources abort the entire capture transaction.
func TestPgCloneBindingCatalogueRejectsUnreadyBucket(t *testing.T) {
	s, _, _ := pgWithPool(t)
	cloneBindingCatalogueRejectsUnreadyBucket(t, s)
}

// ADR-581: compute binding ownership is captured with all six envelopes.
func TestPgCloneBindingCatalogueCapturesObjectComputeBinding(t *testing.T) {
	s, _, _ := pgWithPool(t)
	cloneBindingCatalogueCapturesObjectComputeBinding(t, s)
}

// ADR-581: incomplete managed storage envelopes reject the entire capture.
func TestPgCloneBindingCatalogueRejectsMissingObjectEnvelope(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	a, p, app, op := cloneBindingFixture(t, s)
	bucket := cloneBindingBucket(t, s, a, app, "compute", "production", true)
	cloneObjectComputeBindingFixture(t, s, a, app, bucket)
	if _, err := pool.Exec(ctx, `delete from app_secrets where app_id=$1 and scope='production' and key='ASSETS_REGION'`, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision); !errors.Is(err, state.ErrProjectEnvironmentCloneBindingCapture) {
		t.Fatalf("incomplete object binding omitted: %v", err)
	}
	if records, err := s.ProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID); err != nil || len(records) != 0 {
		t.Fatal("incomplete object binding capture committed")
	}
}

func clonePostgresSecretFixture(t *testing.T, pool *pgxpool.Pool, a state.Account, app state.App, scope, name string, generation int64) state.AppSecret {
	t.Helper()
	ctx := context.Background()
	databaseID, bindingID := uuid.NewString(), uuid.NewString()
	ref := "credential:" + bindingID
	if _, err := pool.Exec(ctx, `insert into managed_postgres_databases(id, account_id, name, region, postgres_major,
		service_class, availability, scale_to_zero, storage_limit_bytes, restore_window_seconds, backend_id,
		backend_fingerprint, provider_resource_id, data_resource_id, state, observed_generation)
		values ($1,$2,$3,'us-east-1',16,'production','single_zone',true,1073741824,86400,'postgres',$4,$5,$6,'ready',1)`,
		databaseID, a.ID, name, strings.Repeat("d", 64), "provider:"+databaseID, "provider:"+databaseID+"/branch"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into managed_postgres_bindings(id, account_id, database_id, app_id, scope,
		environment_key, access, provider_identity_id, credential_ref, credential_generation, state)
		values ($1,$2,$3,$4,$5,'DATABASE_URL','read_only',$6,$7,$8,'ready')`,
		bindingID, a.ID, databaseID, app.ID, scope, "identity:"+bindingID, ref, generation); err != nil {
		t.Fatal(err)
	}
	return state.AppSecret{AccountID: a.ID, AppID: app.ID, Scope: scope, Key: "DATABASE_URL", Ciphertext: []byte("sealed-source-credential"), Kid: "test-kid", ValueHash: strings.Repeat("c", 16),
		ManagedPostgresBindingID: bindingID, ManagedCredentialRef: ref, ManagedCredentialGeneration: generation}
}

// ADR-581: restore preparation uses frozen desired database configuration,
// placement and ownership, even after a source credential/database is edited.
func TestPgClonePostgresBindingCatalogueIsFrozen(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	a, p, app, op := cloneBindingFixture(t, s)
	secret := clonePostgresSecretFixture(t, pool, a, app, "production", "captured-db", 3)
	if err := s.PutManagedPostgresSecret(ctx, secret); err != nil {
		t.Fatal(err)
	}
	views, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision)
	if err != nil || len(views) != 1 {
		t.Fatalf("capture PostgreSQL definition: %v", err)
	}
	bindings, err := s.ProjectEnvironmentCloneBindings(ctx, a.ID, p.ID, op.ID)
	if err != nil || len(bindings) != 1 || len(bindings[0].Postgres) != 1 {
		t.Fatalf("read PostgreSQL definition: %v", err)
	}
	binding := bindings[0].Postgres[0]
	if binding.ID != secret.ManagedPostgresBindingID || binding.CredentialGeneration != 3 || binding.Access != "read_only" ||
		binding.DataResourceID != binding.ProviderResourceID+"/branch" || binding.PostgresMajor != 16 || binding.StorageLimitBytes != 1073741824 || binding.RestoreWindowSeconds != 86400 || !binding.ScaleToZero || bindings[0].Hash != views[0].SourceBindingsHash {
		t.Fatal("database configuration or binding ownership omitted from capture")
	}
	before, _ := json.Marshal(bindings)
	if _, err := pool.Exec(ctx, `update managed_postgres_databases set storage_limit_bytes=2147483648, scale_to_zero=false, data_resource_id='replaced-branch' where id=$1`, binding.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update managed_postgres_bindings set access='read_write' where id=$1`, binding.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteManagedPostgresSecret(ctx, secret.ManagedCredentialRef); err != nil {
		t.Fatal(err)
	}
	bindings[0].Postgres[0].Access = "read_write"
	again, err := s.ProjectEnvironmentCloneBindings(ctx, a.ID, p.ID, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(again)
	if string(before) != string(after) {
		t.Fatal("frozen PostgreSQL definitions changed with the source catalogue or caller mutation")
	}
	retry, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision)
	if err != nil || len(retry) != 1 || retry[0].SourceHash != views[0].SourceHash {
		t.Fatalf("retry read live database configuration: %v", err)
	}
}

// ADR-581: inventory starts with catalogue rows, so a binding whose managed
// envelope disappeared cannot be omitted and claimed as an empty resource set.
func TestPgCloneBindingCatalogueRejectsMissingPostgresEnvelope(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	a, p, app, op := cloneBindingFixture(t, s)
	clonePostgresSecretFixture(t, pool, a, app, "production", "orphaned-db", 1)
	if _, err := s.CaptureProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID, op.Revision); !errors.Is(err, state.ErrProjectEnvironmentCloneBindingCapture) {
		t.Fatalf("binding without an envelope omitted: %v", err)
	}
	if records, err := s.ProjectEnvironmentCloneWorkloads(ctx, a.ID, p.ID, op.ID); err != nil || len(records) != 0 {
		t.Fatal("partial binding capture committed")
	}
}
