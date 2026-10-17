package datapipeline

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func setupFixture(t *testing.T) (*Service, *PostgresStore, string, api.DataConnection, SetupRequest) {
	t.Helper()
	service, store, _, account := testService(t)
	c, err := service.CreateConnection(t.Context(), account, api.CreateDataConnectionRequest{Name: "setup-source", ConnectionURL: "postgres://r:private-sentinel@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	service.PlanMirror = func(context.Context, string, string) (MirrorPlan, error) {
		return MirrorPlan{Region: "test-region", StorageLimitBytes: 1 << 30, RestoreWindowSeconds: 3600}, nil
	}
	service.EnsureMirror = func(context.Context, Setup) error { return nil }
	service.MirrorState = func(context.Context, string, string) (string, error) { return "ready", nil }
	finishSetupCheck(t, store, account, c, "")
	return service, store, account, c, SetupRequest{Name: "setup-orders", SourceConnectionID: c.ID, Tables: []string{"orders"}, ReadSubjects: []string{"alice"}}
}
func finishSetupCheck(t *testing.T, store *PostgresStore, account string, c api.DataConnection, database string) {
	t.Helper()
	if _, err := store.RequestCheck(t.Context(), account, c.ID, c.Revision, database); err != nil {
		t.Fatal(err)
	}
	check, err := store.ClaimCheck(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	endpoint := &api.DataEndpointCapacity{State: "ready", UsableConnections: 100, RequiredConnections: 5, ObservedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(api.DataPipelineCapacityMaxAge).Format(time.RFC3339Nano)}
	capacity := &api.DataPipelineCapacity{State: "ready", Source: endpoint}
	if database != "" {
		capacity.Destination = endpoint
	}
	if err := store.FinishCheck(t.Context(), check, api.DataConnectionCheck{State: "passed", Tables: []api.DataSourceTable{{Name: "orders", Supported: true}}, Checks: []api.DataConnectionIssue{}, Capacity: capacity}); err != nil {
		t.Fatal(err)
	}
}
func setupTick(t *testing.T, service *Service, store *PostgresStore, id string) Setup {
	t.Helper()
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipeline_setups SET retry_at=clock_timestamp() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := service.SetupTick(t.Context()); err != nil {
		t.Fatal(err)
	}
	var account string
	if err := store.pool.QueryRow(t.Context(), `SELECT account_id::text FROM data_pipeline_setups WHERE id=$1`, id).Scan(&account); err != nil {
		t.Fatal(err)
	}
	job, err := store.Setup(t.Context(), account, id)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestSetupConcurrentSubmissionOnlySavesOneIntent(t *testing.T) {
	service, store, account, _, request := setupFixture(t)
	service.EnsureMirror = func(context.Context, Setup) error { t.Error("HTTP intent submission provisioned a mirror"); return nil }
	results := make(chan Setup, 12)
	failures := make(chan error, 12)
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() { job, err := service.StartSetup(t.Context(), account, request); results <- job; failures <- err })
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first Setup
	for job := range results {
		if first.ID == "" {
			first = job
		}
		if job.ID != first.ID || job.DatabaseID != first.DatabaseID {
			t.Fatal("duplicate submission changed resource IDs")
		}
	}
	rows, err := store.Setups(t.Context(), account)
	if err != nil || len(rows) != 1 {
		t.Fatalf("setup count: %d %v", len(rows), err)
	}
	changed := request
	changed.ReadSubjects = []string{"bob"}
	if _, err := service.StartSetup(t.Context(), account, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("different intent adopted old setup: %v", err)
	}
	if _, err := store.Setup(t.Context(), uuid.NewString(), first.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account setup: %v", err)
	}
}

func TestSetupRecoversLostMirrorReplyWithSameResource(t *testing.T) {
	service, store, account, c, request := setupFixture(t)
	job, err := service.StartSetup(t.Context(), account, request)
	if err != nil {
		t.Fatal(err)
	}
	if job = setupTick(t, service, store, job.ID); job.Phase != "provisioning" {
		t.Fatalf("source check: %+v", job)
	}
	made := map[string]bool{}
	attempts := 0
	service.EnsureMirror = func(_ context.Context, job Setup) error {
		attempts++
		made[job.DatabaseID] = true
		if attempts == 1 {
			return ErrUnavailable
		}
		return nil
	}
	job = setupTick(t, service, store, job.ID)
	if job.State != "blocked" || job.Phase != "provisioning" {
		t.Fatalf("lost reply: %+v", job)
	}
	if err := store.DeleteConnection(t.Context(), account, c.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("active setup source deleted: %v", err)
	}
	if _, err := store.ControlSetup(t.Context(), account, job.ID, job.Revision, "retry"); err != nil {
		t.Fatal(err)
	}
	// Restart the catalog client before resuming the same frozen plan and ID.
	service.Store = NewPostgresStore(store.pool)
	job = setupTick(t, service, store, job.ID)
	if job.Phase != "checking_mirror" || job.State != "working" || attempts != 2 || len(made) != 1 {
		t.Fatalf("recovery duplicated provisioning: %+v, %d, %d", job, attempts, len(made))
	}
}

func TestSetupLostPipelineReplyAndExpiredEvidenceRecoverToReady(t *testing.T) {
	service, store, account, c, request := setupFixture(t)
	job, err := service.StartSetup(t.Context(), account, request)
	if err != nil {
		t.Fatal(err)
	}
	job = setupTick(t, service, store, job.ID) // source -> provisioning
	job = setupTick(t, service, store, job.ID) // ready mirror -> check
	job = setupTick(t, service, store, job.ID) // queue mirror check
	finishSetupCheck(t, store, account, c, job.DatabaseID)
	job = setupTick(t, service, store, job.ID)
	if job.Phase != "starting" {
		t.Fatalf("mirror checks: %+v", job)
	}
	pipeline, err := service.Create(t.Context(), account, api.CreateDataPipelineRequest{Name: request.Name, SourceConnectionID: c.ID, DatabaseID: job.DatabaseID, Tables: request.Tables, ReadSubjects: request.ReadSubjects})
	if err != nil {
		t.Fatal(err)
	}
	// The pipeline was accepted, but its setup checkpoint reply was lost.
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_connection_checks SET observed_at=clock_timestamp()-interval '91 seconds' WHERE connection_id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	service.Store = NewPostgresStore(store.pool)
	job = setupTick(t, service, store, job.ID)
	if job.PipelineID != pipeline.ID || job.Phase != "syncing" {
		t.Fatalf("accepted pipeline was not recovered: %+v", job)
	}
	claimed, err := store.Claim(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(t.Context(), claimed, Observation{State: "live"}); err != nil {
		t.Fatal(err)
	}
	job = setupTick(t, service, store, job.ID)
	if job.State != "ready" {
		t.Fatalf("live mirror never completed setup: %+v", job)
	}
	pipelines, err := store.List(t.Context(), account)
	if err != nil || len(pipelines) != 1 {
		t.Fatal("pipeline retry duplicated intent")
	}
}

func TestSetupCancellationAndLeaseTakeoverFenceOldWorkers(t *testing.T) {
	service, store, account, _, request := setupFixture(t)
	job, err := service.StartSetup(t.Context(), account, request)
	if err != nil {
		t.Fatal(err)
	}
	old, err := store.ClaimSetup(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_pipeline_setups SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, job.ID); err != nil {
		t.Fatal(err)
	}
	newer, err := store.ClaimSetup(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FinishSetup(t.Context(), old, old); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired lease accepted: %v", err)
	}
	current, err := store.Setup(t.Context(), account, job.ID)
	if err != nil || current.LeaseToken != newer.LeaseToken {
		t.Fatal("old worker released the new lease")
	}
	if _, err := store.ControlSetup(t.Context(), account, job.ID, job.Revision, "cancel"); err != nil {
		t.Fatal(err)
	}
	next := newer
	next.State = "ready"
	if err := store.FinishSetup(t.Context(), newer, next); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancelled setup completed: %v", err)
	}
	current, err = store.Setup(t.Context(), account, job.ID)
	if err != nil || current.State != "cancelled" {
		t.Fatal("cancellation overwritten")
	}
	if _, err := store.ControlSetup(t.Context(), account, job.ID, job.Revision, "retry"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale control accepted: %v", err)
	}
}

func TestSetupRechecksExpiredSourceCapacityBeforeProvisioning(t *testing.T) {
	service, store, account, c, request := setupFixture(t)
	job, err := service.StartSetup(t.Context(), account, request)
	if err != nil {
		t.Fatal(err)
	}
	job = setupTick(t, service, store, job.ID)
	service.EnsureMirror = func(context.Context, Setup) error { t.Fatal("expired capacity provisioned a mirror"); return nil }
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_connection_checks SET created_at=clock_timestamp()-interval '92 seconds',observed_at=clock_timestamp()-interval '91 seconds' WHERE connection_id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	job = setupTick(t, service, store, job.ID)
	if job.Phase != "checking_source" || job.State != "working" {
		t.Fatalf("expired check was trusted: %+v", job)
	}
	check, err := store.Check(t.Context(), account, c.ID)
	if err != nil || check.State != "pending" {
		t.Fatal("fresh source inspection was not queued")
	}
}

func TestSetupSerializesDiscoveryAndReservesPipelineQuota(t *testing.T) {
	if api.DataPipelineSetupsPerConnection != 1 {
		t.Fatal("update the shared-check serialization contract before changing this limit")
	}
	service, _, account, c, request := setupFixture(t)
	if _, err := service.StartSetup(t.Context(), account, request); err != nil {
		t.Fatal(err)
	}
	other := request
	other.Name = "other-setup"
	if _, err := service.StartSetup(t.Context(), account, other); !errors.Is(err, ErrConflict) {
		t.Fatalf("competing source metadata workflows admitted: %v", err)
	}

	if _, err := service.Create(t.Context(), account, api.CreateDataPipelineRequest{Name: request.Name, SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: request.Tables, ReadSubjects: request.ReadSubjects}); !errors.Is(err, ErrConflict) {
		t.Fatalf("another pipeline stole the saved setup name: %v", err)
	}
	// A saved job keeps one slot from the account's pipeline quota before it
	// requests a billable mirror. Direct pipeline creation cannot steal that slot.
	for i := range api.DataPipelinesMax - 1 {
		_, err := service.Create(t.Context(), account, api.CreateDataPipelineRequest{Name: "direct-" + string(rune('a'+i)), SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: request.Tables, ReadSubjects: request.ReadSubjects})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Create(t.Context(), account, api.CreateDataPipelineRequest{Name: "direct-over", SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: request.Tables, ReadSubjects: request.ReadSubjects}); !errors.Is(err, ErrQuota) {
		t.Fatalf("setup reservation did not protect pipeline quota: %v", err)
	}
}

func TestSetupRejectsExistingPipelineNameBeforeMirrorProvision(t *testing.T) {
	service, store, account, c, request := setupFixture(t)
	if _, err := service.Create(t.Context(), account, api.CreateDataPipelineRequest{Name: request.Name, SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: request.Tables, ReadSubjects: request.ReadSubjects}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.StartSetup(t.Context(), account, request); !errors.Is(err, ErrConflict) {
		t.Fatalf("existing pipeline name accepted: %v", err)
	}
	jobs, err := store.Setups(t.Context(), account)
	if err != nil || len(jobs) != 0 {
		t.Fatal("conflicting pipeline name reserved a mirror")
	}
}

func TestSetupSavesExpiredSelectionsButRefreshesBeforeProvision(t *testing.T) {
	service, store, account, c, request := setupFixture(t)
	if _, err := store.pool.Exec(t.Context(), `UPDATE data_connection_checks SET created_at=clock_timestamp()-interval '92 seconds',observed_at=clock_timestamp()-interval '91 seconds' WHERE connection_id=$1`, c.ID); err != nil {
		t.Fatal(err)
	}
	service.EnsureMirror = func(context.Context, Setup) error {
		t.Fatal("saving stale selections provisioned a mirror")
		return nil
	}
	job, err := service.StartSetup(t.Context(), account, request)
	if err != nil {
		t.Fatalf("expired evidence discarded customer selections: %v", err)
	}
	job = setupTick(t, service, store, job.ID)
	if job.Phase != "checking_source" || job.State != "working" {
		t.Fatalf("stale evidence advanced setup: %+v", job)
	}
	check, err := store.Check(t.Context(), account, c.ID)
	if err != nil || check.State != "pending" {
		t.Fatal("fresh approval was not requested")
	}
}
