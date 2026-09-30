//go:build !no_pg

// adr: 375
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

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneDatabaseCheckpointFailStore struct {
	*state.PgStore
	fail               bool
	failReservationAck bool
}

func (s *cloneDatabaseCheckpointFailStore) ReserveProjectEnvironmentCloneDatabase(ctx context.Context, lease state.ProjectEnvironmentCloneLease, sourceID string, limit int) (state.ProjectEnvironmentCloneDatabaseTarget, bool, error) {
	target, created, err := s.PgStore.ReserveProjectEnvironmentCloneDatabase(ctx, lease, sourceID, limit)
	if err == nil && s.failReservationAck {
		s.failReservationAck = false
		return state.ProjectEnvironmentCloneDatabaseTarget{}, false, errors.New("reservation acknowledgement lost")
	}
	return target, created, err
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
	issued           []managedpostgres.CredentialRequest
	onIssue          func(managedpostgres.CredentialRequest) error
}

func (p *cloneDatabaseDeadlineProvider) Capabilities() managedpostgres.Capabilities {
	c := p.environmentClonePostgresProvider.Capabilities()
	c.CredentialAccess = []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialReadOnly}
	return c
}

func (p *cloneDatabaseDeadlineProvider) IssueCredentials(ctx context.Context, request managedpostgres.CredentialRequest) (managedpostgres.CredentialMaterial, error) {
	if _, ok := ctx.Deadline(); !ok {
		return managedpostgres.CredentialMaterial{}, errors.New("credential provider missing deadline")
	}
	p.issued = append(p.issued, request)
	if p.onIssue != nil {
		if err := p.onIssue(request); err != nil {
			return managedpostgres.CredentialMaterial{}, err
		}
	}
	role := managedpostgres.EndpointPooled
	if request.Access == managedpostgres.CredentialReadOnly {
		role = managedpostgres.EndpointReadOnly
	}
	return managedpostgres.CredentialMaterial{ProviderIdentityID: "identity-" + request.IdentityKey, Username: "u_" + request.IdentityKey, Password: "password-" + request.IdentityKey,
		Database: "gregale", TLSMode: "require", Endpoints: []managedpostgres.Endpoint{{Role: role, Host: request.ProviderResourceID + ".db.example.com", Port: 5432}}}, nil
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
	var serviceClockOffset time.Duration
	service, err := managedpostgres.NewService(registry, databases, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }, Now: func() time.Time { return time.Now().UTC().Add(serviceClockOffset) }})
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
		access := managedpostgres.CredentialReadWrite
		if slug == "worker-jobs" {
			access = managedpostgres.CredentialReadOnly
		}
		if _, err := bindings.Create(ctx, managedpostgres.CreateBindingRequest{AccountID: acct.ID, DatabaseID: source.ID, AppID: app.ID, Scope: "default", EnvironmentKey: "DATABASE_URL", Access: access}); err != nil {
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
	for _, fault := range []string{"token", "revision", "status", "account", "project"} {
		bad := lease
		switch fault {
		case "token":
			bad.Token = uuid.NewString()
		case "revision":
			bad.Operation.Revision++
		case "status":
			bad.Operation.Status = state.CloneOperationPublishing
		case "account":
			bad.Operation.AccountID = uuid.NewString()
		case "project":
			bad.Operation.ProjectID = uuid.NewString()
		}
		if _, _, err := store.ReserveProjectEnvironmentCloneDatabase(ctx, bad, source.ID, 3); err == nil {
			t.Fatalf("%s authority reserved private database", fault)
		}
		if _, err := store.ProjectEnvironmentCloneDatabaseForLease(ctx, bad, source.ID); err == nil {
			t.Fatalf("%s authority read private database", fault)
		}
	}
	for _, fault := range []string{"hash", "missing", "duplicate", "shared_target", "point", "status", "unexpected"} {
		bad := append([]state.ProjectEnvironmentCloneResource{}, resources...)
		switch fault {
		case "hash":
			bad[0].SourceVersion = strings.Repeat("f", 64)
		case "missing":
			bad = []state.ProjectEnvironmentCloneResource{}
		case "duplicate":
			bad = append(bad, bad[0])
		case "shared_target":
			bad[0].TargetID = source.ID
		case "point":
			bad[0].CapturePoint = point.Add(time.Nanosecond).Format(time.RFC3339Nano)
		case "status":
			bad[0].Status = "failed"
		case "unexpected":
			bad[0].Name, bad[0].SourceID = uuid.NewString(), uuid.NewString()
		}
		raw, _ := json.Marshal(bad)
		if _, err := pool.Exec(ctx, "update project_environment_clone_operations set resources=$2 where id=$1", op.ID, raw); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.ReserveProjectEnvironmentCloneDatabase(ctx, lease, source.ID, 3); err == nil {
			t.Fatalf("%s durable roster reserved private database", fault)
		}
	}
	raw, _ := json.Marshal(resources)
	if _, err := pool.Exec(ctx, "update project_environment_clone_operations set resources=$2 where id=$1", op.ID, raw); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReserveProjectEnvironmentCloneDatabase(ctx, lease, source.ID, 1); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("private database bypassed account quota: %v", err)
	}
	if _, err := databases.FindByName(ctx, acct.ID, plans[0].name); !errors.Is(err, managedpostgres.ErrNotFound) || len(provider.restores) != 0 {
		t.Fatalf("rejected reservation wrote a target or restored data: %v", err)
	}
	for _, mutation := range []string{
		"update managed_postgres_databases set restore_window_seconds=0 where id=$1",
		"update managed_postgres_databases set restore_window_seconds=1 where id=$1",
		"update managed_postgres_databases set state='updating' where id=$1",
		"update managed_postgres_databases set provider_resource_id='replaced' where id=$1",
		"update managed_postgres_databases set backend_fingerprint=repeat('f',64) where id=$1",
	} {
		if _, err := pool.Exec(ctx, mutation, source.ID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.ReserveProjectEnvironmentCloneDatabase(ctx, lease, source.ID, 3); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("source identity/availability/retention drift accepted: %s: %v", mutation, err)
		}
		if _, err := pool.Exec(ctx, "update managed_postgres_databases set state='ready', restore_window_seconds=$2, provider_resource_id=$3, backend_fingerprint=$4 where id=$1",
			source.ID, source.Spec.RestoreWindowSeconds, source.ProviderResourceID, source.BackendFingerprint); err != nil {
			t.Fatal(err)
		}
	}
	assertCloneDatabaseReservationExpiresDuringAccountWait(t, ctx, pool, store, lease, source.ID)
	// Later source desired configuration and bindings do not replace the
	// private catalogue. This is test-only mutation of provider intent.
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set storage_limit_bytes = $1 where id = $2", int64(2<<30), source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "update managed_postgres_bindings set environment_key = 'LATER_DATABASE_URL' where database_id = $1", source.ID); err != nil {
		t.Fatal(err)
	}
	srv.store = &cloneDatabaseCheckpointFailStore{PgStore: store, fail: true, failReservationAck: true}
	reservationLease, ready, err := srv.prepareProjectEnvironmentCloneDatabases(ctx, lease)
	if err == nil || !strings.Contains(err.Error(), "reservation acknowledgement lost") || ready || len(provider.restores) != 0 {
		t.Fatalf("lost reservation response reached provider: ready %t, error %v, calls %+v", ready, err, provider.restores)
	}
	reserved, err := databases.FindByName(ctx, acct.ID, plans[0].name)
	if err != nil || reserved.EnvironmentCloneOperationID != op.ID || reserved.State != managedpostgres.StateProvisioning || !reserved.RestorePointInTime.Equal(point) {
		t.Fatalf("ownership and restore intent did not commit together: %+v, %v", reserved, err)
	}
	for _, mutation := range []string{
		"update managed_postgres_databases set environment_clone_operation_id=null where id=$1",
		"update managed_postgres_databases set environment_clone_operation_id='00000000-0000-0000-0000-000000000001' where id=$1",
		"update managed_postgres_databases set name='changed-private-name' where id=$1",
	} {
		if _, err := pool.Exec(ctx, mutation, reserved.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ProjectEnvironmentCloneDatabaseForLease(ctx, reservationLease, source.ID); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("changed reservation identity accepted: %s: %v", mutation, err)
		}
		if _, _, err := store.ReserveProjectEnvironmentCloneDatabase(ctx, reservationLease, source.ID, 3); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("changed reservation identity replaced/adopted: %s: %v", mutation, err)
		}
		if _, err := pool.Exec(ctx, "update managed_postgres_databases set environment_clone_operation_id=$2, name=$3 where id=$1", reserved.ID, op.ID, reserved.Name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `insert into managed_postgres_databases(id, account_id, name, region, postgres_major, service_class, availability, scale_to_zero,
		storage_limit_bytes, restore_window_seconds, backend_id, backend_fingerprint, restore_source_database_id, restore_source_resource_id,
		restore_point_in_time, environment_clone_operation_id)
		select $2, account_id, 'duplicate-private-target', region, postgres_major, service_class, availability, scale_to_zero,
		storage_limit_bytes, restore_window_seconds, backend_id, backend_fingerprint, restore_source_database_id, restore_source_resource_id,
		restore_point_in_time, environment_clone_operation_id from managed_postgres_databases where id=$1`, reserved.ID, uuid.NewString()); err == nil {
		t.Fatal("duplicate operation/source database reservation accepted")
	}
	provider.onRestore = func() error {
		if len(provider.restores) != 1 || provider.restores[0].ResourceID != reserved.ID {
			return errors.New("restore retry replaced committed identity")
		}
		if _, err := service.Get(ctx, acct.ID, reserved.ID); !errors.Is(err, managedpostgres.ErrNotFound) {
			return errors.New("customer could access target during provider restore")
		}
		if items, err := service.List(ctx, acct.ID); err != nil || len(items) != 1 || items[0].ID != source.ID {
			return errors.New("private restore appeared in customer list")
		}
		if _, err := service.Delete(ctx, acct.ID, reserved.ID); !errors.Is(err, managedpostgres.ErrNotFound) {
			return errors.New("customer could delete private restore")
		}
		return nil
	}
	failedLease, ready, err := srv.prepareProjectEnvironmentCloneDatabases(ctx, reservationLease)
	if err == nil || ready || len(provider.restores) != 1 || !provider.deadlineObserved || provider.restores[0].Spec != source.Spec {
		t.Fatalf("uncheckpointed restore = ready %t, error %v, calls %+v", ready, err, provider.restores)
	}
	provider.onRestore = nil
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
	serviceClockOffset = 2 * time.Hour
	finished, ready, err := srv.prepareProjectEnvironmentCloneDatabases(ctx, replacement)
	if err != nil || !ready || len(provider.restores) != 1 || finished.Operation.Resources[0].TargetID == "" || finished.Operation.Resources[0].Status != "ready" || finished.Operation.Resources[0].CapturePoint != resources[0].CapturePoint {
		t.Fatalf("restart = %+v, ready %t, error %v, copies %d", finished.Operation.Resources, ready, err, len(provider.restores))
	}
	target, err := databases.Get(ctx, acct.ID, finished.Operation.Resources[0].TargetID)
	if err != nil || target.ID != reserved.ID || target.EnvironmentCloneOperationID != op.ID || target.Spec != source.Spec || target.ProviderResourceID == source.ProviderResourceID || target.RestoreSourceDatabaseID != source.ID {
		t.Fatalf("target = %+v, %v", target, err)
	}
	if _, err := service.Get(ctx, acct.ID, target.ID); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("completed restore became visible before full publication: %v", err)
	}
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set restore_window_seconds=0, provider_resource_id='source-unavailable' where id=$1", source.ID); err != nil {
		t.Fatal(err)
	}
	if adopted, created, err := store.ReserveProjectEnvironmentCloneDatabase(ctx, finished, source.ID, 1); err != nil || created || adopted.ID != target.ID {
		t.Fatalf("completed restore could not replay at an exhausted quota: %+v, %t, %v", adopted, created, err)
	}
	darkService, err := managedpostgres.NewService(registry, databases, managedpostgres.ServiceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	srv.managedPostgres = darkService
	if replayed, ready, err := srv.prepareProjectEnvironmentCloneDatabases(ctx, finished); err != nil || !ready || len(provider.restores) != 1 || replayed.Operation.Revision != finished.Operation.Revision {
		t.Fatalf("completed replay reread source or current provisioning gate: %+v, %t, %v", replayed, ready, err)
	}
	srv.managedPostgres = service
	if _, err := pool.Exec(ctx, "update managed_postgres_databases set restore_window_seconds=$2, provider_resource_id=$3 where id=$1", source.ID, source.Spec.RestoreWindowSeconds, source.ProviderResourceID); err != nil {
		t.Fatal(err)
	}
	live, err := service.Get(ctx, acct.ID, source.ID)
	if err != nil || live.Spec.StorageLimitBytes != 2<<30 {
		t.Fatalf("production intent was changed: %+v, %v", live, err)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "stage"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("copy step published a target: %v", err)
	}
	finished = capturedClonePostgresBindingWorkerContract(t, srv, store, pool, databases, provider, finished)
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
	serviceClockOffset = 0
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

func assertCloneDatabaseReservationExpiresDuringAccountWait(t *testing.T, parent context.Context, pool *pgxpool.Pool, store *state.PgStore, lease state.ProjectEnvironmentCloneLease, sourceID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 6*time.Second)
	defer cancel()
	locker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locker.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := locker.Exec(ctx, "select id from accounts where id=$1 for update", lease.Operation.AccountID); err != nil {
		t.Fatal(err)
	}
	var expiry time.Time
	if err := pool.QueryRow(ctx, "update project_environment_clone_operations set lease_until=clock_timestamp()+interval '2 seconds' where id=$1 returning lease_until", lease.Operation.ID).Scan(&expiry); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, _, err := store.ReserveProjectEnvironmentCloneDatabase(ctx, lease, sourceID, 3)
		result <- err
	}()
	for {
		var blocked bool
		if err := pool.QueryRow(ctx, "select exists(select 1 from pg_stat_activity where $1=any(pg_blocking_pids(pid)))", int32(locker.Conn().PgConn().PID())).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("reservation did not wait for quota serialization: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if delay := time.Until(expiry) + 10*time.Millisecond; delay > 0 {
		time.Sleep(delay)
	}
	if err := locker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, state.ErrConflict) {
		t.Fatalf("lease expired during account lock wait still reserved target: %v", err)
	}
	if _, err := pool.Exec(ctx, "update project_environment_clone_operations set lease_until=clock_timestamp()+interval '1 minute' where id=$1", lease.Operation.ID); err != nil {
		t.Fatal(err)
	}
}
