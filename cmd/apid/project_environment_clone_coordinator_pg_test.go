//go:build !no_pg

// adr: 569
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneCoordinatorFailureStore struct {
	*state.PgStore
	loseMaterializationAck bool
	loseCaptureAck         bool
}

func (s *cloneCoordinatorFailureStore) AdvanceProjectEnvironmentCloneOperation(ctx context.Context, accountID, projectID, operationID, expected, next string, revision int64, resources []state.ProjectEnvironmentCloneResource, code string) (state.ProjectEnvironmentCloneOperation, error) {
	op, err := s.PgStore.AdvanceProjectEnvironmentCloneOperation(ctx, accountID, projectID, operationID, expected, next, revision, resources, code)
	if err == nil && next == state.CloneOperationCapturing && s.loseCaptureAck {
		s.loseCaptureAck = false
		return state.ProjectEnvironmentCloneOperation{}, errors.New("capture acknowledgement lost")
	}
	return op, err
}

func (s *cloneCoordinatorFailureStore) MaterializeProjectEnvironmentCloneForLease(ctx context.Context, lease state.ProjectEnvironmentCloneLease, ids []string, count int, limits api.Limits) (state.ProjectEnvironment, error) {
	env, err := s.PgStore.MaterializeProjectEnvironmentCloneForLease(ctx, lease, ids, count, limits)
	if err == nil && s.loseMaterializationAck {
		s.loseMaterializationAck = false
		return state.ProjectEnvironment{}, errors.New("materialization acknowledgement lost")
	}
	return env, err
}

type cloneCoordinatorFixture struct {
	srv      *server
	store    *cloneCoordinatorFailureStore
	pool     *pgxpool.Pool
	lease    state.ProjectEnvironmentCloneLease
	notifier *clonePrimeNotifier
	database *cloneDatabaseDeadlineProvider
	objects  *capturedObjectWorkerProvider
	apps     []state.App
}

func newCloneCoordinatorFixture(t *testing.T, withData bool) cloneCoordinatorFixture {
	t.Helper()
	ctx := t.Context()
	_, cleanup := withTestIdentities(t)
	t.Cleanup(cleanup)
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := &cloneCoordinatorFailureStore{PgStore: state.NewPgStore(pool)}
	account, err := store.CreateAccount(ctx, uuid.NewString()+"@coordinator.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "coordinator", ProductionBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	notifier := &clonePrimeNotifier{}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", notifier)
	f := cloneCoordinatorFixture{srv: srv, store: store, pool: pool, notifier: notifier}
	point := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	for _, slug := range []string{"coordinator-api", "coordinator-jobs"} {
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: slug, WorkloadName: slug,
			Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		f.apps = append(f.apps, app)
		if err := store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "production", "CAPTURED", "original"); err != nil {
			t.Fatal(err)
		}
		deployment, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:captured"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, deployment.ID, "/captured.ext4", "layers/"+deployment.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
	}
	if withData {
		f.database = &cloneDatabaseDeadlineProvider{}
		registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"}, MaxDatabasesPerAccount: 3,
			Backends: []managedpostgres.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "coordinator"}}}, func(string) string { return "" },
			map[string]managedpostgres.Factory{"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
				return f.database, nil
			}})
		if err != nil {
			t.Fatal(err)
		}
		databases, err := managedpostgres.NewPostgresStore(pool)
		if err != nil {
			t.Fatal(err)
		}
		service, err := managedpostgres.NewService(registry, databases, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }})
		if err != nil {
			t.Fatal(err)
		}
		sink, err := newAppSecretCredentialSink(store, setSecretRecipient, hostHMACKey)
		if err != nil {
			t.Fatal(err)
		}
		bindings, err := managedpostgres.NewBindingService(registry, databases, databases, sink, managedpostgres.BindingServiceOptions{ProvisioningEnabled: func() bool { return true }})
		if err != nil {
			t.Fatal(err)
		}
		srv.WithManagedPostgres(service, nil, bindings, nil, nil, nil)
		source, err := service.Create(ctx, managedpostgres.CreateRequest{AccountID: account.ID, Name: "orders", Spec: managedpostgres.Spec{Region: "eu", PostgresMajor: 17,
			Class: managedpostgres.ClassDevelopment, Availability: managedpostgres.AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		for _, app := range f.apps {
			if _, err := bindings.Create(ctx, managedpostgres.CreateBindingRequest{AccountID: account.ID, DatabaseID: source.ID, AppID: app.ID, Scope: "production", EnvironmentKey: "DATABASE_URL", Access: managedpostgres.CredentialReadWrite}); err != nil {
				t.Fatal(err)
			}
		}
		f.objects = &capturedObjectWorkerProvider{environmentSnapshotProvider: &environmentSnapshotProvider{
			versions: []objectstorage.ObjectVersion{{Key: "data.json", VersionID: "v1", Size: 3, LastModified: point.Add(-time.Second)}, {Key: "data.json", VersionID: "v2", Size: 3, LastModified: point.Add(time.Second)}},
			bodies:   map[string]string{"v1": "old", "v2": "new"}}}
		config := objectstorage.BackendConfig{ID: "storage", Driver: "fixture", Region: "us-east-1", Namespace: "coordinator", Endpoint: "https://storage.example.test", S3Region: "us-east-1"}
		objects, err := objectstorage.NewRegistry(objectstorage.Config{DefaultRegion: config.Region, Defaults: map[string]string{config.Region: config.ID}, Backends: []objectstorage.BackendConfig{config}}, func(string) string { return "" },
			map[string]objectstorage.Factory{"fixture": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
				return f.objects, nil
			}})
		if err != nil {
			t.Fatal(err)
		}
		srv.WithObjectStorage(objects)
		if err := srv.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
			t.Fatal(err)
		}
		backend, err := objects.Default(config.Region)
		if err != nil {
			t.Fatal(err)
		}
		bucket, err := store.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: f.apps[0].ID, Name: "assets", Scope: "production",
			Region: config.Region, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "gregale-" + strings.ReplaceAll(uuid.NewString(), "-", "")}, objects.MaxBucketsPerApp)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimObjectBucket(ctx, account.ID, f.apps[0].ID, bucket.ID, "source", "provisioning"); err != nil {
			t.Fatal(err)
		}
		if err := store.FinishObjectBucket(ctx, bucket.ID, "source", "ready"); err != nil {
			t.Fatal(err)
		}
		capturedCloneObjectWorkerSourceCredentials(t, srv, store, account, f.apps[0], []state.ObjectBucket{bucket})
	}
	op, err := store.CreateCapturedProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneCaptureRequest{AccountID: account.ID, ProjectID: project.ID,
		SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "coordinator"})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	views, err := store.ProjectEnvironmentCloneWorkloads(ctx, account.ID, project.ID, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	resources := []state.ProjectEnvironmentCloneResource{{Kind: "source_revision", Name: "production", SourceVersion: op.SourceRevisionHash, Status: "ready"},
		{Kind: "project_config", Name: "production", SourceVersion: views[0].SourceProjectConfigHash, Status: "captured"}}
	for _, view := range views {
		resources = append(resources, state.ProjectEnvironmentCloneResource{Kind: "workload", Name: view.WorkloadSlug, SourceID: view.SourceDeploymentID, SourceVersion: view.SourceHash, Status: "captured"})
		for _, kind := range []string{"variables", "secrets"} {
			resources = append(resources, state.ProjectEnvironmentCloneResource{Kind: kind, Name: view.WorkloadSlug, SourceID: view.AppID, TargetID: view.AppID, SourceVersion: view.SourceValuesHash, Status: "captured"})
		}
	}
	if withData {
		plans, err := srv.capturedProjectEnvironmentDatabasePlans(ctx, op)
		if err != nil {
			t.Fatal(err)
		}
		dbResources, err := capturedProjectEnvironmentDatabaseResources(plans, point)
		if err != nil {
			t.Fatal(err)
		}
		objectPlans, err := srv.capturedProjectEnvironmentObjectPlans(ctx, op)
		if err != nil {
			t.Fatal(err)
		}
		objectResources, err := capturedProjectEnvironmentObjectResources(objectPlans, point)
		if err != nil {
			t.Fatal(err)
		}
		resources = append(resources, dbResources...)
		resources = append(resources, objectResources...)
	}
	lease.Operation, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, op.Status, state.CloneOperationCapturing, lease.Operation.Revision, resources, "")
	if err != nil {
		t.Fatal(err)
	}
	if withData {
		lease, err = srv.captureProjectEnvironmentCloneObjects(ctx, lease)
		if err != nil {
			t.Fatal(err)
		}
	}
	lease.Operation, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, account.ID, project.ID, op.ID, state.CloneOperationCapturing, state.CloneOperationCopying, lease.Operation.Revision, lease.Operation.Resources, "")
	if err != nil {
		t.Fatal(err)
	}
	f.lease = lease
	return f
}

func (f cloneCoordinatorFixture) makeRunnable(t *testing.T) {
	t.Helper()
	if err := f.store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
		t.Fatal(err)
	}
}

func (f cloneCoordinatorFixture) retryNow(t *testing.T) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), "update project_environment_clone_operations set next_attempt_at=clock_timestamp() where id=$1 and lease_token is null", f.lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
}

func (f cloneCoordinatorFixture) targets(t *testing.T) []state.ProjectEnvironmentCloneWorkload {
	t.Helper()
	op := f.lease.Operation
	views, err := f.store.ProjectEnvironmentCloneWorkloads(t.Context(), op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		t.Fatal(err)
	}
	return views
}

func TestPGCloneCoordinatorResumesPreparationAcrossCommitAndHandoffFailures(t *testing.T) {
	f := newCloneCoordinatorFixture(t, true)
	f.makeRunnable(t)
	f.store.loseMaterializationAck = true
	if err := f.srv.processNextProjectEnvironmentClone(t.Context(), f.store); err == nil {
		t.Fatal("missing committed materialization acknowledgement failure")
	}
	op := f.lease.Operation
	env, err := f.store.ProjectEnvironmentBySlug(t.Context(), op.AccountID, op.ProjectID, op.TargetEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range f.targets(t) {
		if view.TargetDeploymentID != "" {
			t.Fatal("deployment primed before configuration acknowledgement")
		}
	}
	f.retryNow(t)
	f.notifier.fail = true
	if err := f.srv.processNextProjectEnvironmentClone(t.Context(), f.store); err == nil {
		t.Fatal("missing deployment handoff failure")
	}
	f.retryNow(t)
	if err := f.srv.processNextProjectEnvironmentClone(t.Context(), f.store); err != nil {
		t.Fatal(err)
	}
	after, err := f.store.ProjectEnvironmentBySlug(t.Context(), op.AccountID, op.ProjectID, op.TargetEnvironment)
	if err != nil || after.ID != env.ID {
		t.Fatalf("retry changed target lifetime: %v", err)
	}
	for _, view := range f.targets(t) {
		if view.TargetDeploymentID == "" {
			t.Fatal("coordinator omitted workload")
		}
		if err := f.store.MarkDeploymentLiveDark(t.Context(), view.TargetDeploymentID); err != nil {
			t.Fatal(err)
		}
	}
	f.retryNow(t)
	if err := f.srv.processNextProjectEnvironmentClone(t.Context(), f.store); !errors.Is(err, state.ErrProjectEnvironmentCloneResourcePublicationProof) {
		t.Fatalf("missing independent data-publication gate: %v", err)
	}
	finished, err := f.store.ProjectEnvironmentCloneOperationByID(t.Context(), op.AccountID, op.ProjectID, op.ID)
	if err != nil || finished.Status != state.CloneOperationCopying || finished.TargetReleaseSetID != "" {
		t.Fatalf("unproved data clone advertised complete: %+v, %v", finished, err)
	}
	for _, resource := range finished.Resources {
		if resource.Status != "ready" || (resource.Kind == "managed_postgres" || resource.Kind == "object_storage" || resource.Kind == "workload") && resource.SourceID == resource.TargetID {
			t.Fatalf("resource was omitted or shared: %+v", resource)
		}
	}
	if len(f.database.restores) != 1 || f.objects.copyCalls != 1 || f.objects.lastCopiedVersion != "v1" || len(f.objects.created) != 1 || !f.database.deadlineObserved {
		t.Fatal("retry recreated resources, recopied data, or lost provider deadline")
	}
	for _, app := range f.apps {
		deployments, err := f.store.ListDeploymentsForApp(t.Context(), app.ID, 0, 0)
		if err != nil || len(deployments) != 2 {
			t.Fatalf("retry duplicated deployment: %v", err)
		}
		values, err := f.store.ListAppEnvInScope(t.Context(), op.AccountID, app.ID, "stage")
		if err != nil || len(values) != 1 || values[0].Value != "original" {
			t.Fatalf("materialization omitted frozen values: %v", err)
		}
	}
	if _, err := f.store.MaterializeProjectEnvironmentCloneForLease(t.Context(), f.lease, nil, 0, api.MustLimitsFor(api.PlanPro)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("previous worker adopted current preparation: %v", err)
	}
}

func TestPGCloneCredentialPreparationRequiresDurableRestoreReceipt(t *testing.T) {
	for _, fault := range []string{"missing_receipt", "changed_spec", "changed_provider_identity", "changed_data_identity"} {
		t.Run(fault, func(t *testing.T) {
			f := newCloneCoordinatorFixture(t, true)
			lease, ready, err := f.srv.prepareProjectEnvironmentCloneDatabases(t.Context(), f.lease)
			if err != nil || !ready {
				t.Fatalf("database preparation = %v, %v", ready, err)
			}
			var databaseID string
			for _, resource := range lease.Operation.Resources {
				if resource.Kind == "managed_postgres" {
					databaseID = resource.TargetID
				}
			}
			if databaseID == "" {
				t.Fatal("database preparation omitted physical target")
			}
			statement := map[string]string{
				"missing_receipt":           `delete from managed_postgres_restore_proofs where database_id=$1`,
				"changed_spec":              `update managed_postgres_databases set storage_limit_bytes=storage_limit_bytes+1 where id=$1`,
				"changed_provider_identity": `update managed_postgres_databases set provider_resource_id='changed-private-resource' where id=$1`,
				"changed_data_identity":     `update managed_postgres_databases set data_resource_id='changed-private-data' where id=$1`,
			}[fault]
			if _, err := f.pool.Exec(t.Context(), statement, databaseID); err != nil {
				t.Fatal(err)
			}
			issuedBefore := len(f.database.issued)
			if _, _, _, err := f.srv.prepareProjectEnvironmentClonePostgresBindings(t.Context(), lease); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("unproved restore created credentials: %v", err)
			}
			if len(f.database.issued) != issuedBefore || len(f.database.restores) != 1 {
				t.Fatal("failed restore receipt issued credentials or repeated the physical copy")
			}
			op := lease.Operation
			if _, err := f.store.ProjectEnvironmentBySlug(t.Context(), op.AccountID, op.ProjectID, op.TargetEnvironment); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("unproved restore materialized a target environment")
			}
		})
	}
}

func TestPGCloneCoordinatorWaitsForEveryWorkloadAndPolicyActivation(t *testing.T) {
	f := newCloneCoordinatorFixture(t, false)
	f.makeRunnable(t)
	if err := f.srv.processNextProjectEnvironmentClone(t.Context(), f.store); err != nil {
		t.Fatal(err)
	}
	views := f.targets(t)
	if err := f.store.MarkDeploymentLiveDark(t.Context(), views[0].TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	f.retryNow(t)
	if err := f.srv.processNextProjectEnvironmentClone(t.Context(), f.store); err != nil {
		t.Fatal(err)
	}
	op := f.lease.Operation
	current, err := f.store.ProjectEnvironmentCloneOperationByID(t.Context(), op.AccountID, op.ProjectID, op.ID)
	if err != nil || current.Status != state.CloneOperationCopying || current.TargetReleaseSetID != "" {
		t.Fatalf("partial workload roster published: %+v, %v", current, err)
	}
	if err := f.store.MarkDeploymentLiveDark(t.Context(), views[1].TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	f.retryNow(t)
	if err := f.srv.processNextProjectEnvironmentClone(t.Context(), f.store); !errors.Is(err, state.ErrProjectEnvironmentCloneWorkPolicyIsolationUnavailable) {
		t.Fatalf("missing independent policy activation guard: %v", err)
	}
	current, err = f.store.ProjectEnvironmentCloneOperationByID(t.Context(), op.AccountID, op.ProjectID, op.ID)
	if err != nil || current.Status != state.CloneOperationCopying || current.TargetReleaseSetID != "" {
		t.Fatalf("unproved policies published release graph: %+v, %v", current, err)
	}
}

func TestPGCloneMaterializationRejectsEditedHeadsAndRecreatedEnvironment(t *testing.T) {
	f := newCloneCoordinatorFixture(t, false)
	ctx := t.Context()
	env, err := f.store.MaterializeProjectEnvironmentCloneForLease(ctx, f.lease, nil, 0, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	op := f.lease.Operation
	app := f.apps[0]
	spec, err := f.store.ProjectEnvironmentWorkloadSpec(ctx, op.AccountID, op.ProjectID, "stage", app.ID)
	if err != nil {
		t.Fatal(err)
	}
	changed := spec.Settings
	changed.MaxConcurrency++
	if _, err := f.store.PutProjectEnvironmentWorkloadSpec(ctx, op.AccountID, op.ProjectID, "stage", app.ID, spec.Revision, changed); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.MaterializeProjectEnvironmentCloneForLease(ctx, f.lease, nil, 0, api.MustLimitsFor(api.PlanPro)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("edited target overwritten or adopted: %v", err)
	}
	if current, err := f.store.ProjectEnvironmentWorkloadSpec(ctx, op.AccountID, op.ProjectID, "stage", app.ID); err != nil || current.Settings.MaxConcurrency != changed.MaxConcurrency {
		t.Fatalf("retry overwrote developer edit: %v", err)
	}
	// Emulate lifecycle deletion/recreation beneath the worker. The receipt has
	// no environment FK, so neither cascade nor slug reuse can erase lineage.
	if _, err := f.pool.Exec(ctx, "delete from project_environments where id=$1", env.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, "insert into project_environments(account_id,project_id,slug) values($1,$2,'stage')", op.AccountID, op.ProjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.MaterializeProjectEnvironmentCloneForLease(ctx, f.lease, nil, 0, api.MustLimitsFor(api.PlanPro)); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("recreated target adopted old capture: %v", err)
	}
}

func TestPGCloneMaterializationRetainsFlagEditsOnReplay(t *testing.T) {
	f := newCloneCoordinatorFixture(t, false)
	ctx := t.Context()
	op := f.lease.Operation
	env, err := f.store.MaterializeProjectEnvironmentCloneForLease(ctx, f.lease, nil, 0, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	scope := state.FeatureFlagScope{AccountID: op.AccountID, ProjectID: op.ProjectID, EnvironmentID: env.ID}
	initial, err := f.store.GetFeatureFlags(ctx, scope, 0)
	if err != nil || initial.Version != 1 {
		t.Fatalf("initial flag copy: %v", err)
	}
	edited, err := f.store.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: initial.Version,
		Config: flags.Config{Flags: []flags.Flag{{Key: "stage_edit", Enabled: true, Default: true}}}, Actor: "developer"})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := f.store.MaterializeProjectEnvironmentCloneForLease(ctx, f.lease, nil, 0, api.MustLimitsFor(api.PlanPro))
	if err != nil || replayed.ID != env.ID {
		t.Fatalf("materialization replay: %v", err)
	}
	current, err := f.store.GetFeatureFlags(ctx, scope, 0)
	if err != nil || current.Version != edited.Version || len(current.Flags) != 1 || current.Flags[0].Key != "stage_edit" {
		t.Fatalf("replay overwrote stage flags: %v", err)
	}
}

func TestPGCloneCoordinatorWaitsForCoordinatedCaptureWithoutCreatingTarget(t *testing.T) {
	f := newCloneCoordinatorFixture(t, true)
	ctx := t.Context()
	op := f.lease.Operation
	if err := f.store.ReleaseProjectEnvironmentCloneLease(ctx, f.lease, time.Hour); err != nil {
		t.Fatal(err)
	}
	pending, err := f.store.CreateCapturedProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneCaptureRequest{
		AccountID: op.AccountID, ProjectID: op.ProjectID, SourceEnvironment: "production", TargetEnvironment: "checkpoint", IdempotencyKey: "checkpoint"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.srv.processNextProjectEnvironmentClone(ctx, f.store); !errors.Is(err, errCloneCheckpointUnavailable) {
		t.Fatalf("pending clone advanced without checkpoint: %v", err)
	}
	current, err := f.store.ProjectEnvironmentCloneOperationByID(ctx, op.AccountID, op.ProjectID, pending.ID)
	if err != nil || current.Status != state.CloneOperationPending || len(current.Resources) != 0 {
		t.Fatalf("worker invented capture identity: %+v, %v", current, err)
	}
	if _, err := f.store.ProjectEnvironmentBySlug(ctx, op.AccountID, op.ProjectID, pending.TargetEnvironment); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("uncoordinated target materialized: %v", err)
	}
	var released, delayed bool
	if err := f.pool.QueryRow(ctx, "select lease_token is null, next_attempt_at>clock_timestamp() from project_environment_clone_operations where id=$1", pending.ID).Scan(&released, &delayed); err != nil || !released || !delayed {
		t.Fatalf("deferred work retained lease or hot retry: %v", err)
	}
}

func TestPGCloneCoordinatorRejectsMixedDataPointsBeforeProviderCopy(t *testing.T) {
	f := newCloneCoordinatorFixture(t, true)
	ctx := t.Context()
	resources := append([]state.ProjectEnvironmentCloneResource(nil), f.lease.Operation.Resources...)
	for i := range resources {
		if resources[i].Kind == "object_storage" {
			point, err := time.Parse(time.RFC3339Nano, resources[i].CapturePoint)
			if err != nil {
				t.Fatal(err)
			}
			resources[i].CapturePoint = point.Add(-time.Second).Format(time.RFC3339Nano)
		}
	}
	raw, err := json.Marshal(resources)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, "update project_environment_clone_operations set resources=$2 where id=$1", f.lease.Operation.ID, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.processProjectEnvironmentCloneLease(ctx, f.store, f.lease); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("mixed data points accepted: %v", err)
	}
	if len(f.database.restores) != 0 || f.objects.copyCalls != 0 {
		t.Fatal("mixed checkpoint reached provider copying")
	}
}
