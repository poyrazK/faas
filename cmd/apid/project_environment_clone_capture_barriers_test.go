//go:build !no_pg

// adr: 590
package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneCaptureBarrierFailureStore struct {
	*state.PgStore
	loseAcquire                bool
	objectFault, postgresFault string
}

func (s *cloneCaptureBarrierFailureStore) AcquireProjectEnvironmentCloneObjectWriteFence(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ObjectBucketWriteFence, error) {
	f, err := s.PgStore.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, l, id)
	if err == nil && s.loseAcquire {
		s.loseAcquire = false
		return state.ObjectBucketWriteFence{}, managedpostgres.ErrUnavailable
	}
	return f, err
}

func (s *cloneCaptureBarrierFailureStore) ProjectEnvironmentCloneObjectWriteFencesForLease(ctx context.Context, l state.ProjectEnvironmentCloneLease) ([]state.ObjectBucketWriteFence, error) {
	fences, err := s.PgStore.ProjectEnvironmentCloneObjectWriteFencesForLease(ctx, l)
	if err == nil && len(fences) > 0 {
		switch s.objectFault {
		case "omit":
			fences = fences[1:]
		case "duplicate":
			fences[1] = fences[0]
		case "placement":
			fences[0].Bucket.PhysicalName += "-other"
		case "owner":
			fences[0].CloneOperationID = uuid.NewString()
		case "negative":
			fences[0].NativeGrants = -1
		}
	}
	return fences, err
}

func (s *cloneCaptureBarrierFailureStore) ProjectEnvironmentClonePostgresWriteFencesForLease(ctx context.Context, l state.ProjectEnvironmentCloneLease) ([]state.ProjectEnvironmentClonePostgresWriteFence, error) {
	fences, err := s.PgStore.ProjectEnvironmentClonePostgresWriteFencesForLease(ctx, l)
	if err == nil && len(fences) > 0 {
		switch s.postgresFault {
		case "omit":
			fences = nil
		case "placement":
			fences[0].SourceDataResourceID += "-other"
		}
	}
	return fences, err
}

func cloneCaptureBarrierFixture(t *testing.T) (cloneCoordinatorFixture, *cloneCaptureBarrierFailureStore, *cloneCheckpointClosureProvider, []state.ObjectBucket) {
	t.Helper()
	f := newCloneCoordinatorFixture(t, true)
	ctx := t.Context()
	op := f.lease.Operation
	if err := f.store.ReleaseProjectEnvironmentCloneLease(ctx, f.lease, time.Hour); err != nil {
		t.Fatal(err)
	}
	// A second owned bucket makes omissions and duplicate roster replies real.
	id := uuid.NewString()
	b, err := f.store.ReserveObjectBucket(ctx, state.ObjectBucket{ID: id, AccountID: op.AccountID, AppID: f.apps[1].ID, Name: "second", Scope: "production",
		Region: "us-east-1", BackendID: "storage", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "gregale-" + strings.ReplaceAll(id, "-", "")}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.ClaimObjectBucket(ctx, op.AccountID, f.apps[1].ID, b.ID, "source", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.FinishObjectBucket(ctx, b.ID, "source", "ready"); err != nil {
		t.Fatal(err)
	}
	op, err = f.store.CreateCapturedProjectEnvironmentCloneOperation(ctx, state.ProjectEnvironmentCloneCaptureRequest{
		AccountID: op.AccountID, ProjectID: op.ProjectID, SourceEnvironment: "production", TargetEnvironment: "barriers", IdempotencyKey: "barriers"})
	if err != nil {
		t.Fatal(err)
	}
	f.lease, err = f.store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil || f.lease.Operation.ID != op.ID {
		t.Fatalf("claim barrier operation: %v", err)
	}
	f.lease.Operation, err = f.store.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCapturing, f.lease.Operation.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	store := &cloneCaptureBarrierFailureStore{PgStore: f.store.PgStore}
	provider := &cloneCheckpointClosureProvider{cloneMaintenanceProvider: &cloneMaintenanceProvider{cloneSnapshotProvider: &cloneSnapshotProvider{}}}
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "eu", Defaults: map[string]string{"eu": "test"}, MaxDatabasesPerAccount: 3,
		Backends: []managedpostgres.BackendConfig{{ID: "test", Driver: "test", Region: "eu", Namespace: "coordinator"}}}, func(string) string { return "" },
		map[string]managedpostgres.Factory{"test": func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return provider, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	databases, err := managedpostgres.NewPostgresStore(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	f.srv.managedPostgres, err = managedpostgres.NewService(registry, databases, managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return false }})
	if err != nil {
		t.Fatal(err)
	}
	f.srv.store = store
	f.srv.cloneWorkerAdmission = func(context.Context) error { return nil }
	plans, err := f.srv.capturedProjectEnvironmentObjectPlans(ctx, f.lease.Operation)
	if err != nil || len(plans) != 2 {
		t.Fatalf("owned object roster: %v", err)
	}
	buckets := make([]state.ObjectBucket, 0, len(plans))
	for _, plan := range plans {
		bucket, err := store.GetObjectBucket(ctx, op.AccountID, plan.appID, plan.source.ID)
		if err != nil {
			t.Fatal(err)
		}
		buckets = append(buckets, bucket)
	}
	return f, store, provider, buckets
}

func assertCloneCaptureHasNoPoint(t *testing.T, f cloneCoordinatorFixture) {
	t.Helper()
	l := f.lease
	op, err := f.store.ProjectEnvironmentCloneOperationByID(t.Context(), l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
	if err != nil || op.Status != state.CloneOperationCapturing || op.Revision != l.Operation.Revision || len(op.Resources) != 0 || op.SourceRevisionHash != l.Operation.SourceRevisionHash || op.TargetReleaseSetID != "" {
		t.Fatalf("barriers supplied capture or publication: %+v %v", op, err)
	}
	if _, err := f.store.ProjectEnvironmentBySlug(t.Context(), op.AccountID, op.ProjectID, "barriers"); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("barriers created a stage")
	}
	var targets int
	if err := f.pool.QueryRow(t.Context(), "select count(*) from project_environment_clone_workloads where operation_id=$1 and target_deployment_id is not null", op.ID).Scan(&targets); err != nil || targets != 0 {
		t.Fatalf("barriers created target artifacts: %d %v", targets, err)
	}
}

func TestPGCloneCaptureBarriersRecoverAllSourcesAndOutstandingWriters(t *testing.T) {
	f, store, provider, buckets := cloneCaptureBarrierFixture(t)
	ctx := t.Context()
	request, err := store.BeginObjectBucketMutation(ctx, buckets[0], state.ObjectBucketMutationRequest)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := store.BeginObjectBucketMutation(ctx, buckets[1], state.ObjectBucketMutationNativeGrant)
	if err != nil {
		t.Fatal(err)
	}
	store.loseAcquire = true
	var observation cloneCaptureBarrierObservation
	f.lease, observation, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(ctx, f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(observation, cloneCaptureBarrierObservation{}) || provider.roles != 0 || provider.closes != 0 {
		t.Fatalf("uncertain partial roster reached provider: %v", err)
	}
	partial, err := store.PgStore.ProjectEnvironmentCloneObjectWriteFencesForLease(ctx, f.lease)
	if err != nil || len(partial) != 1 {
		t.Fatalf("lost acquisition discarded durable hold: %v", err)
	}
	stale := f.lease
	if err := store.ReleaseProjectEnvironmentCloneLease(ctx, f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, result, err := f.srv.prepareProjectEnvironmentCloneCaptureBarriers(ctx, stale); !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(result, cloneCaptureBarrierObservation{}) {
		t.Fatalf("stale worker resumed capture: %v", err)
	}
	provider.onDiscover = func(ctx context.Context) error {
		fences, err := store.PgStore.ProjectEnvironmentCloneObjectWriteFencesForLease(ctx, f.lease)
		if err != nil || len(fences) != 2 {
			t.Fatalf("provider ran before complete object roster: %v", err)
		}
		return nil
	}
	provider.loseClose = true
	f.lease, observation, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(ctx, f.lease)
	if !errors.Is(err, managedpostgres.ErrUnavailable) || !reflect.DeepEqual(observation, cloneCaptureBarrierObservation{}) || provider.closes != 1 {
		t.Fatalf("lost close supplied evidence: %v", err)
	}
	plans, err := f.srv.capturedProjectEnvironmentDatabasePlans(ctx, f.lease.Operation)
	if err != nil || len(plans) != 1 {
		t.Fatalf("shared bindings duplicated source: %v", err)
	}
	original, err := store.ProjectEnvironmentClonePostgresCheckpointSelectionForLease(ctx, f.lease, plans[0].source.ID)
	if err != nil {
		t.Fatal(err)
	}
	provider.inventory = []string{"changed_catalogue"}
	f.lease, observation, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(ctx, f.lease)
	if err != nil || observation.instrumentedWritersDrained || len(observation.postgres) != 1 || len(observation.objects) != 2 || observation.configuration.Hash != f.lease.Operation.SourceRevisionHash || provider.discoveries != 1 || provider.deadlineMissing {
		t.Fatalf("busy recovery omitted a source: %+v %v", observation, err)
	}
	if observation.objects[0].Requests != 1 || observation.objects[1].NativeGrants != 1 {
		t.Fatal("unknown writers were treated as drained")
	}
	for _, bucket := range buckets {
		if _, err := store.BeginObjectBucketMutation(ctx, bucket, state.ObjectBucketMutationRequest); !errors.Is(err, state.ErrObjectBucketWriteFenced) {
			t.Fatalf("source admitted another writer: %v", err)
		}
	}
	provider.drained = true
	if err := store.FinishObjectBucketMutation(ctx, request); err != nil {
		t.Fatal(err)
	}
	f.lease, observation, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(ctx, f.lease)
	if err != nil || observation.instrumentedWritersDrained || observation.objects[1].NativeGrants != 1 {
		t.Fatalf("completed request hid unresolved native grant: %v", err)
	}
	// Request completion cannot acknowledge a native grant's remote outcome.
	if err := store.FinishObjectBucketMutation(ctx, grant); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("request completion cleared a native grant: %v", err)
	}
	f.lease, observation, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(ctx, f.lease)
	if err != nil || observation.instrumentedWritersDrained || observation.objects[1].NativeGrants != 1 {
		t.Fatalf("invalid acknowledgement lost the native grant: %v", err)
	}
	recovered, err := store.ProjectEnvironmentClonePostgresCheckpointSelectionForLease(ctx, f.lease, plans[0].source.ID)
	if err != nil || !bytes.Equal(original.Sealed.Ciphertext, recovered.Sealed.Ciphertext) || original.Sealed.Fingerprint != recovered.Sealed.Fingerprint || provider.discoveries != 1 {
		t.Fatalf("retry replaced original source selection: %v", err)
	}
	assertCloneCaptureHasNoPoint(t, f)
}

func TestPGCloneCaptureBarriersZeroTrackedWritersDoNotSelectCheckpoint(t *testing.T) {
	f, _, provider, _ := cloneCaptureBarrierFixture(t)
	provider.drained = true
	var result cloneCaptureBarrierObservation
	var err error
	f.lease, result, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(t.Context(), f.lease)
	if err != nil || !result.instrumentedWritersDrained || len(result.postgres) != 1 || len(result.objects) != 2 {
		t.Fatalf("empty instrumented writer roster: %+v %v", result, err)
	}
	assertCloneCaptureHasNoPoint(t, f)
}

func TestPGCloneCaptureBarriersRejectIncompleteOwnedRostersBeforeProviderIO(t *testing.T) {
	for _, fault := range []string{"object_omit", "object_duplicate", "object_placement", "object_owner", "object_negative", "postgres_omit", "postgres_placement"} {
		t.Run(fault, func(t *testing.T) {
			f, store, provider, _ := cloneCaptureBarrierFixture(t)
			if strings.HasPrefix(fault, "object_") {
				store.objectFault = strings.TrimPrefix(fault, "object_")
			} else {
				store.postgresFault = strings.TrimPrefix(fault, "postgres_")
			}
			var result cloneCaptureBarrierObservation
			var err error
			f.lease, result, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(t.Context(), f.lease)
			if !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(result, cloneCaptureBarrierObservation{}) || provider.roles != 0 || provider.closes != 0 {
				t.Fatalf("incomplete roster dispatched: %v", err)
			}
			actual, err := store.PgStore.ProjectEnvironmentCloneObjectWriteFencesForLease(t.Context(), f.lease)
			if err != nil || len(actual) != 2 {
				t.Fatalf("roster rejection released sources: %v", err)
			}
			assertCloneCaptureHasNoPoint(t, f)
		})
	}
}

func TestPGCloneCaptureBarriersRejectConfigurationDriftAndLostAuthority(t *testing.T) {
	for _, fault := range []string{"config_before", "admission", "provider_missing", "config_during", "object_omit_during", "postgres_omit_during", "handoff", "admission_handoff", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			f, store, provider, _ := cloneCaptureBarrierFixture(t)
			provider.drained = true
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			edit := func(ctx context.Context) error {
				return store.UpsertAppEnvInScope(ctx, f.lease.Operation.AccountID, f.apps[0].ID, "production", "CAPTURED", "changed")
			}
			if fault == "config_before" {
				if err := edit(ctx); err != nil {
					t.Fatal(err)
				}
			} else if fault == "admission" {
				f.srv.cloneWorkerAdmission = func(context.Context) error { return managedpostgres.ErrUnavailable }
			} else if fault == "provider_missing" {
				f.srv.managedPostgres = nil
			} else {
				provider.onObserve = func(ctx context.Context) error {
					handoff := func(ctx context.Context) error {
						if err := store.ReleaseProjectEnvironmentCloneLease(ctx, f.lease, 0); err != nil {
							return err
						}
						var err error
						f.lease, err = store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
						return err
					}
					switch fault {
					case "config_during":
						return edit(ctx)
					case "object_omit_during":
						store.objectFault = "omit"
					case "postgres_omit_during":
						store.postgresFault = "omit"
					case "handoff":
						return handoff(ctx)
					case "admission_handoff":
						f.srv.cloneWorkerAdmission = handoff
					case "cancel":
						cancel()
					}
					return nil
				}
			}
			lease := f.lease
			_, result, err := f.srv.prepareProjectEnvironmentCloneCaptureBarriers(ctx, lease)
			if err == nil || !reflect.DeepEqual(result, cloneCaptureBarrierObservation{}) {
				t.Fatalf("%s returned usable observation: %v", fault, err)
			}
			if fault == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			actual, readErr := store.PgStore.ProjectEnvironmentCloneObjectWriteFencesForLease(t.Context(), f.lease)
			expected := 2
			if fault == "config_before" || fault == "admission" || fault == "provider_missing" {
				expected = 0
			}
			if readErr != nil || len(actual) != expected || expected == 0 && provider.roles != 0 {
				t.Fatalf("%s changed source authority: %d %v", fault, len(actual), readErr)
			}
			assertCloneCaptureHasNoPoint(t, f)
		})
	}
}
