//go:build !no_pg

// adr: 375
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneDatabaseCheckpointFailStore struct {
	*state.PgStore
	fail bool
}

func (s *cloneDatabaseCheckpointFailStore) AdvanceProjectEnvironmentCloneOperation(ctx context.Context, account, project, id, expected, next string, revision int64, resources []state.ProjectEnvironmentCloneResource, code string) (state.ProjectEnvironmentCloneOperation, error) {
	if s.fail && expected == state.CloneOperationCopying && next == expected {
		s.fail = false
		return state.ProjectEnvironmentCloneOperation{}, errors.New("checkpoint unavailable")
	}
	return s.PgStore.AdvanceProjectEnvironmentCloneOperation(ctx, account, project, id, expected, next, revision, resources, code)
}

type cloneDatabaseDeadlineProvider struct {
	environmentClonePostgresProvider
	deadlineObserved bool
}

func (p *cloneDatabaseDeadlineProvider) Restore(ctx context.Context, request managedpostgres.RestoreRequest) (managedpostgres.ObservedDatabase, error) {
	_, p.deadlineObserved = ctx.Deadline()
	return p.environmentClonePostgresProvider.Restore(ctx, request)
}

func TestPGCapturedCloneDatabaseWorkerRecoversUncheckpointedRestore(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(ctx, uuid.NewString()+"@clone-worker.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "clone-worker", ProductionBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	provider := &cloneDatabaseDeadlineProvider{}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"}, MaxDatabasesPerAccount: 3,
		Backends: []managedpostgres.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "captured-clone-worker"}}},
		func(string) string { return "" }, map[string]managedpostgres.Factory{"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return provider, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	databases, err := managedpostgres.NewPostgresStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now().UTC()
	service, err := managedpostgres.NewService(registry, databases, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }, Now: func() time.Time { return clock }})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sink, err := newAppSecretCredentialSink(store, func() *age.X25519Recipient { return identity.Recipient() }, func() []byte { return []byte("0123456789abcdef0123456789abcdef") })
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := managedpostgres.NewBindingService(registry, databases, databases, sink, managedpostgres.BindingServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithManagedPostgres(service, nil, bindings, nil, nil)
	source, err := service.Create(ctx, managedpostgres.CreateRequest{AccountID: acct.ID, Name: "orders", Spec: managedpostgres.Spec{
		Region: "eu", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone,
		ScaleToZero: true, StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"worker-api", "worker-jobs"} {
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: slug, WorkloadName: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := bindings.Create(ctx, managedpostgres.CreateBindingRequest{AccountID: acct.ID, DatabaseID: source.ID, AppID: app.ID, Scope: "default", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite}); err != nil {
			t.Fatal(err)
		}
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:captured"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, d.ID, "/captured.ext4", "layers/"+d.ID+".ext4", 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	op, err := store.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: acct.ID, ProjectID: project.ID, SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "worker", SourceRevisionHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, op.Status, state.CloneOperationCapturing, lease.Operation.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CaptureProjectEnvironmentCloneWorkloads(ctx, acct.ID, project.ID, op.ID, op.Revision); err != nil {
		t.Fatal(err)
	}
	plans, err := srv.capturedProjectEnvironmentDatabasePlans(ctx, op)
	if err != nil || len(plans) != 1 {
		t.Fatalf("captured plans = %+v, %v", plans, err)
	}
	point := clock.Add(-time.Second).Truncate(time.Microsecond)
	resources, err := capturedProjectEnvironmentDatabaseResources(plans, point)
	if err != nil {
		t.Fatal(err)
	}
	op, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, op.ID, op.Status, state.CloneOperationCopying, op.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	lease.Operation = op
	// Later source desired configuration and bindings do not replace the
	// private catalogue. This is test-only mutation of provider intent.
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set storage_limit_bytes = $1 where id = $2", int64(2<<30), source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update managed_postgres_bindings set environment_key = 'LATER_DATABASE_URL' where database_id = $1", source.ID); err != nil {
		t.Fatal(err)
	}
	srv.store = &cloneDatabaseCheckpointFailStore{PgStore: store, fail: true}
	failedLease, ready, err := srv.prepareProjectEnvironmentCloneDatabases(ctx, lease)
	if err == nil || ready || len(provider.restores) != 1 || !provider.deadlineObserved || provider.restores[0].Spec != source.Spec {
		t.Fatalf("uncheckpointed restore = ready %t, error %v, calls %+v", ready, err, provider.restores)
	}
	current, err := store.ProjectEnvironmentCloneOperationByID(ctx, acct.ID, project.ID, op.ID)
	if err != nil || current.Resources[0].TargetID != "" {
		t.Fatalf("checkpoint unexpectedly persisted = %+v, %v", current, err)
	}
	if err := store.ReleaseProjectEnvironmentCloneLease(ctx, failedLease, 0); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.prepareProjectEnvironmentCloneDatabases(ctx, failedLease); !errors.Is(err, state.ErrConflict) || len(provider.restores) != 1 {
		t.Fatalf("stale worker reached provider = %v, calls %d", err, len(provider.restores))
	}
	// A completed copy survives expiration of its original restore window.
	// Recovery reads only the exact operation-owned target reservation.
	clock = clock.Add(2 * time.Hour)
	finished, ready, err := srv.prepareProjectEnvironmentCloneDatabases(ctx, replacement)
	if err != nil || !ready || len(provider.restores) != 1 || finished.Operation.Resources[0].TargetID == "" || finished.Operation.Resources[0].Status != "ready" || finished.Operation.Resources[0].CapturePoint != resources[0].CapturePoint {
		t.Fatalf("restart = %+v, ready %t, error %v, copies %d", finished.Operation.Resources, ready, err, len(provider.restores))
	}
	target, err := service.Get(ctx, acct.ID, finished.Operation.Resources[0].TargetID)
	if err != nil || target.Spec != source.Spec || target.ProviderResourceID == source.ProviderResourceID || target.RestoreSourceDatabaseID != source.ID {
		t.Fatalf("target = %+v, %v", target, err)
	}
	live, err := service.Get(ctx, acct.ID, source.ID)
	if err != nil || live.Spec.StorageLimitBytes != 2<<30 {
		t.Fatalf("production intent was changed: %+v, %v", live, err)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("copy step published a target: %v", err)
	}
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set restore_source_resource_id = 'replaced' where id = $1", target.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.prepareProjectEnvironmentCloneDatabases(ctx, finished); !errors.Is(err, state.ErrConflict) || len(provider.restores) != 1 {
		t.Fatalf("changed lineage accepted: %v, calls %d", err, len(provider.restores))
	}
	// Keep the completed operation delayed while another operation in this
	// project tests takeover in the middle of provider IO.
	if err := store.ReleaseProjectEnvironmentCloneLease(ctx, finished, time.Hour); err != nil {
		t.Fatal(err)
	}
	clock = time.Now().UTC()
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set storage_limit_bytes = $1 where id = $2", source.Spec.StorageLimitBytes, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update managed_postgres_bindings set environment_key = 'DATABASE_URL' where database_id = $1", source.ID); err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneOperation{AccountID: acct.ID, ProjectID: project.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage-takeover", IdempotencyKey: "takeover", SourceRevisionHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	secondLease, err := store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil || secondLease.Operation.ID != second.ID {
		t.Fatalf("claim second operation = %+v, %v", secondLease, err)
	}
	second, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, second.ID, second.Status, state.CloneOperationCapturing, secondLease.Operation.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CaptureProjectEnvironmentCloneWorkloads(ctx, acct.ID, project.ID, second.ID, second.Revision); err != nil {
		t.Fatal(err)
	}
	secondPlans, err := srv.capturedProjectEnvironmentDatabasePlans(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	secondResources, err := capturedProjectEnvironmentDatabaseResources(secondPlans, clock.Add(-time.Second).Truncate(time.Microsecond))
	if err != nil {
		t.Fatal(err)
	}
	second, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, acct.ID, project.ID, second.ID, second.Status, state.CloneOperationCopying, second.Revision, secondResources, "")
	if err != nil {
		t.Fatal(err)
	}
	secondLease.Operation = second
	var takeover state.ProjectEnvironmentCloneLease
	provider.onRestore = func() error {
		if err := store.ReleaseProjectEnvironmentCloneLease(ctx, secondLease, 0); err != nil {
			return err
		}
		var err error
		takeover, err = store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
		return err
	}
	if _, _, err := srv.prepareProjectEnvironmentCloneDatabases(ctx, secondLease); !errors.Is(err, state.ErrConflict) || len(provider.restores) != 2 {
		t.Fatalf("old worker checkpointed after takeover = %v, copies %d", err, len(provider.restores))
	}
	provider.onRestore = nil
	if takeover.Operation.ID != second.ID {
		t.Fatalf("takeover claimed another operation: %+v", takeover.Operation)
	}
	resumed, ready, err := srv.prepareProjectEnvironmentCloneDatabases(ctx, takeover)
	if err != nil || !ready || len(provider.restores) != 2 || resumed.Operation.Resources[0].TargetID == "" {
		t.Fatalf("takeover failed to adopt completed copy = %+v, ready %t, error %v, copies %d", resumed.Operation.Resources, ready, err, len(provider.restores))
	}
}
