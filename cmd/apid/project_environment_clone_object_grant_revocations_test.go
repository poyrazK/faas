//go:build !no_pg

// adr: 590
package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/objectstorage/grantrevocation"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneGrantRevocationFailureStore struct {
	*state.PgStore
	fault string
}

func (s *cloneGrantRevocationFailureStore) ReserveProjectEnvironmentCloneObjectGrantRevocation(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string) (state.ProjectEnvironmentCloneObjectGrantRevocation, error) {
	r, err := s.PgStore.ReserveProjectEnvironmentCloneObjectGrantRevocation(ctx, l, id)
	if err == nil && s.fault == "reserve_reply" {
		s.fault = ""
		return state.ProjectEnvironmentCloneObjectGrantRevocation{}, objectstorage.ErrUnavailable
	}
	return r, err
}

func (s *cloneGrantRevocationFailureStore) DispatchProjectEnvironmentCloneObjectGrantRevocation(ctx context.Context, l state.ProjectEnvironmentCloneLease, p grantrevocation.Plan) (state.ProjectEnvironmentCloneObjectGrantRevocation, error) {
	if s.fault == "dispatch_before" {
		s.fault = ""
		return state.ProjectEnvironmentCloneObjectGrantRevocation{}, objectstorage.ErrUnavailable
	}
	r, err := s.PgStore.DispatchProjectEnvironmentCloneObjectGrantRevocation(ctx, l, p)
	if err == nil && s.fault == "dispatch_reply" {
		s.fault = ""
		return state.ProjectEnvironmentCloneObjectGrantRevocation{}, objectstorage.ErrUnavailable
	}
	return r, err
}

func (s *cloneGrantRevocationFailureStore) RecordProjectEnvironmentCloneObjectGrantRevocation(ctx context.Context, l state.ProjectEnvironmentCloneLease, p grantrevocation.Plan, o grantrevocation.Observation) (state.ProjectEnvironmentCloneObjectGrantRevocation, error) {
	if s.fault == "record_before" {
		s.fault = ""
		return state.ProjectEnvironmentCloneObjectGrantRevocation{}, objectstorage.ErrUnavailable
	}
	r, err := s.PgStore.RecordProjectEnvironmentCloneObjectGrantRevocation(ctx, l, p, o)
	if err == nil && s.fault == "record_reply" {
		s.fault = ""
		return state.ProjectEnvironmentCloneObjectGrantRevocation{}, objectstorage.ErrUnavailable
	}
	return r, err
}

type cloneGrantRevocationProvider struct {
	objectstorage.Provider
	journal                  grantrevocation.Plan
	revokes, reads           int
	fault                    string
	writers                  int64
	revoked                  bool
	beforeRevoke, beforeRead func(grantrevocation.Plan)
}

func (p *cloneGrantRevocationProvider) RevokeNativeWriteGrants(_ context.Context, plan grantrevocation.Plan) error {
	p.revokes++
	if p.journal.RequestID != "" && !reflect.DeepEqual(p.journal, plan) {
		return objectstorage.ErrConflict
	}
	p.journal = plan.Clone()
	if p.beforeRevoke != nil {
		p.beforeRevoke(plan)
	}
	if p.fault == "revoke_reply" {
		p.fault = ""
		return objectstorage.ErrUnavailable
	}
	// Provider-owned copies cannot alias the worker's retained roster.
	plan.GrantIDs[0] = uuid.NewString()
	return nil
}

func (p *cloneGrantRevocationProvider) ObserveNativeWriteGrantRevocation(_ context.Context, plan grantrevocation.Plan) (grantrevocation.Observation, error) {
	p.reads++
	if !reflect.DeepEqual(p.journal, plan) {
		return grantrevocation.Observation{}, objectstorage.ErrNotFound
	}
	if p.beforeRead != nil {
		p.beforeRead(plan)
	}
	hash, _ := p.journal.SHA256()
	o := grantrevocation.Observation{Scope: plan.Scope, RequestID: plan.RequestID, PlanSHA256: hash,
		RevocationID: "immutable-provider-retirement", AllNativeGrantsRevoked: p.revoked, InFlightWrites: p.writers}
	switch p.fault {
	case "missing_journal", "read_reply":
		return grantrevocation.Observation{}, objectstorage.ErrNotFound
	case "scope":
		o.Scope.PhysicalName += "-other"
	case "request":
		o.RequestID = uuid.NewString()
	case "hash":
		o.PlanSHA256 = "other"
	case "negative":
		o.InFlightWrites = -1
	case "identity":
		o.RevocationID = ""
	}
	return o, nil
}

func cloneGrantRevocationFixture(t *testing.T) (cloneCoordinatorFixture, *cloneGrantRevocationFailureStore, *cloneGrantRevocationProvider, capturedProjectEnvironmentObjectPlan, state.ObjectBucketMutation) {
	t.Helper()
	f, _, _, buckets := cloneCaptureBarrierFixture(t)
	ctx := t.Context()
	store := &cloneGrantRevocationFailureStore{PgStore: f.store.PgStore}
	f.srv.store = store
	provider := &cloneGrantRevocationProvider{Provider: f.objects, revoked: true}
	config := objectstorage.BackendConfig{ID: "storage", Driver: "fixture", Region: "us-east-1", Namespace: "coordinator", Endpoint: "https://storage.example.test", S3Region: "us-east-1"}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{DefaultRegion: config.Region, Defaults: map[string]string{config.Region: config.ID}, Backends: []objectstorage.BackendConfig{config}},
		func(string) string { return "" }, map[string]objectstorage.Factory{"fixture": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
			return provider, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	f.srv.WithObjectStorage(registry)
	plans, err := f.srv.capturedProjectEnvironmentObjectPlans(ctx, f.lease.Operation)
	if err != nil {
		t.Fatal(err)
	}
	var plan capturedProjectEnvironmentObjectPlan
	var source state.ObjectBucket
	for _, candidate := range plans {
		if _, err := registry.Resolve(candidate.source.BackendID, candidate.source.BackendFingerprint); err == nil {
			plan = candidate
		}
	}
	for _, b := range buckets {
		if b.ID == plan.source.ID {
			source = b
		}
	}
	if source.ID == "" {
		t.Fatal("no source in qualified fixture placement")
	}
	request, err := store.BeginObjectBucketMutation(ctx, source, state.ObjectBucketMutationRequest)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := store.BeginObjectBucketMutation(ctx, source, state.ObjectBucketMutationNativeGrant); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.AcquireProjectEnvironmentCloneObjectWriteFence(ctx, f.lease, source.ID); err != nil {
		t.Fatal(err)
	}
	provider.beforeRevoke = func(p grantrevocation.Plan) {
		r, err := store.PgStore.ProjectEnvironmentCloneObjectGrantRevocationForLease(ctx, f.lease, source.ID)
		if err != nil || r.RequestStartedAt.IsZero() || !reflect.DeepEqual(r.Plan, p) {
			t.Fatalf("provider dispatched without retained authority: %+v %v", r, err)
		}
	}
	return f, store, provider, plan, request
}

func TestPGCloneObjectGrantRevocationWorkerRecoversLostAcknowledgements(t *testing.T) {
	for _, fault := range []string{"reserve_reply", "dispatch_before", "dispatch_reply", "revoke_reply", "record_before", "record_reply"} {
		t.Run(fault, func(t *testing.T) {
			f, store, provider, plan, request := cloneGrantRevocationFixture(t)
			ctx := t.Context()
			store.fault = fault
			if fault == "revoke_reply" {
				provider.fault = fault
			}
			var observation grantrevocation.Observation
			var err error
			f.lease, observation, err = f.srv.revokeProjectEnvironmentCloneObjectNativeGrants(ctx, f.lease, plan)
			if err == nil || !reflect.DeepEqual(observation, grantrevocation.Observation{}) {
				t.Fatalf("uncertain outcome returned a usable observation: %+v %v", observation, err)
			}
			r, err := store.PgStore.ProjectEnvironmentCloneObjectGrantRevocationForLease(ctx, f.lease, plan.source.ID)
			if err != nil || len(r.Plan.GrantIDs) != 2 {
				t.Fatalf("lost original intent: %+v %v", r, err)
			}
			original := r.Plan.Clone()
			stale := f.lease
			if err := store.ReleaseProjectEnvironmentCloneLease(ctx, stale, 0); err != nil {
				t.Fatal(err)
			}
			f.lease, err = store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			provider.beforeRevoke = nil
			if _, _, err := f.srv.revokeProjectEnvironmentCloneObjectNativeGrants(ctx, stale, plan); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("stale worker resumed retirement: %v", err)
			}
			f.lease, observation, err = f.srv.revokeProjectEnvironmentCloneObjectNativeGrants(ctx, f.lease, plan)
			if err != nil || !observation.Drained() || observation.RequestID != original.RequestID || provider.reads == 0 || !reflect.DeepEqual(provider.journal, original) {
				t.Fatalf("retirement failed to recover original request: %+v %v", observation, err)
			}
			fences, err := store.ProjectEnvironmentCloneObjectWriteFencesForLease(ctx, f.lease)
			if err != nil || len(fences) != 1 || fences[0].NativeGrants != 0 || fences[0].Requests != 1 {
				t.Fatalf("retirement consumed synchronous request: %+v %v", fences, err)
			}
			if err := store.FinishObjectBucketMutation(ctx, request); err != nil {
				t.Fatal(err)
			}
			assertCloneCaptureHasNoPoint(t, f)
		})
	}
}

func TestPGCloneObjectGrantRevocationWorkerRequiresIndependentDrain(t *testing.T) {
	for _, fault := range []string{"missing_journal", "read_reply", "scope", "request", "hash", "negative", "identity", "unrevoked", "busy"} {
		t.Run(fault, func(t *testing.T) {
			f, store, provider, plan, _ := cloneGrantRevocationFixture(t)
			provider.fault = fault
			if fault == "busy" {
				provider.writers = 1
			}
			if fault == "unrevoked" {
				provider.revoked = false
			}
			var observation grantrevocation.Observation
			var err error
			f.lease, observation, err = f.srv.revokeProjectEnvironmentCloneObjectNativeGrants(t.Context(), f.lease, plan)
			if fault == "busy" || fault == "unrevoked" {
				if err != nil || observation.Drained() {
					t.Fatalf("false drainage: %+v %v", observation, err)
				}
			} else if err == nil || !reflect.DeepEqual(observation, grantrevocation.Observation{}) {
				t.Fatalf("unowned provider observation: %+v %v", observation, err)
			}
			fences, err := store.ProjectEnvironmentCloneObjectWriteFencesForLease(t.Context(), f.lease)
			if err != nil || fences[0].NativeGrants != 2 || provider.revokes != 1 || provider.reads != 1 {
				t.Fatalf("reply or unknown observation consumed native grants: %+v %v", fences, err)
			}
			provider.fault, provider.writers, provider.revoked = "", 0, true
			f.lease, observation, err = f.srv.revokeProjectEnvironmentCloneObjectNativeGrants(t.Context(), f.lease, plan)
			if err != nil || !observation.Drained() {
				t.Fatalf("original retirement did not resume: %+v %v", observation, err)
			}
		})
	}
}

func TestPGCloneObjectGrantRevocationWorkerRechecksAuthorityAndConfiguration(t *testing.T) {
	for _, fault := range []string{"revocation_admission", "observation_admission", "revocation_lease", "observation_lease", "configuration", "placement", "cancellation"} {
		t.Run(fault, func(t *testing.T) {
			f, store, provider, plan, _ := cloneGrantRevocationFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			closed := false
			f.srv.cloneWorkerAdmission = func(context.Context) error {
				if closed {
					return objectstorage.ErrUnavailable
				}
				return nil
			}
			mutate := func(grantrevocation.Plan) {
				switch fault {
				case "revocation_admission", "observation_admission":
					closed = true
				case "revocation_lease", "observation_lease":
					if err := store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
						t.Fatal(err)
					}
				case "configuration":
					if err := store.UpsertAppEnvInScope(t.Context(), f.lease.Operation.AccountID, plan.appID, "production", "CAPTURED", "changed"); err != nil {
						t.Fatal(err)
					}
				case "placement":
					if _, err := f.pool.Exec(t.Context(), "update object_buckets set physical_name=physical_name||'-other' where id=$1", plan.source.ID); err != nil {
						t.Fatal(err)
					}
				case "cancellation":
					cancel()
				}
			}
			if fault == "revocation_admission" || fault == "revocation_lease" || fault == "placement" {
				provider.beforeRevoke = mutate
			} else {
				provider.beforeRead = mutate
			}
			_, observation, err := f.srv.revokeProjectEnvironmentCloneObjectNativeGrants(ctx, f.lease, plan)
			if err == nil || !reflect.DeepEqual(observation, grantrevocation.Observation{}) {
				t.Fatalf("authority/configuration drift returned observation: %+v %v", observation, err)
			}
			var count int
			if err := f.pool.QueryRow(t.Context(), "select count(*) from object_bucket_mutations where bucket_id=$1 and kind='native_grant'", plan.source.ID).Scan(&count); err != nil || count != 2 {
				t.Fatalf("drift consumed retained grants: %d %v", count, err)
			}
		})
	}
}

func TestPGCloneObjectGrantRevocationWorkerUnsupportedProvider(t *testing.T) {
	f, store, _, plan, _ := cloneGrantRevocationFixture(t)
	backend := f.srv.objectStorage.Backends()[0]
	config := objectstorage.BackendConfig{ID: backend.ID, Driver: "fixture", Region: backend.Region, Namespace: "coordinator", Endpoint: "https://storage.example.test", S3Region: "us-east-1"}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{DefaultRegion: config.Region, Defaults: map[string]string{config.Region: config.ID}, Backends: []objectstorage.BackendConfig{config}},
		func(string) string { return "" }, map[string]objectstorage.Factory{"fixture": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) {
			return f.objects, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	f.srv.WithObjectStorage(registry)
	_, observation, err := f.srv.revokeProjectEnvironmentCloneObjectNativeGrants(t.Context(), f.lease, plan)
	if !errors.Is(err, objectstorage.ErrUnsupported) || !reflect.DeepEqual(observation, grantrevocation.Observation{}) {
		t.Fatalf("unqualified provider accepted: %+v %v", observation, err)
	}
	if _, err := store.ProjectEnvironmentCloneObjectGrantRevocationForLease(t.Context(), f.lease, plan.source.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unsupported provider persisted dispatch: %v", err)
	}
}

func TestPGCloneObjectGrantRevocationWorkerCompensationRecoversOriginalIntent(t *testing.T) {
	f, store, provider, plan, _ := cloneGrantRevocationFixture(t)
	ctx := t.Context()
	provider.fault = "revoke_reply"
	var err error
	f.lease, _, err = f.srv.revokeProjectEnvironmentCloneObjectNativeGrants(ctx, f.lease, plan)
	if err == nil {
		t.Fatal("lost native dispatch reply was acknowledged")
	}
	original := provider.journal.Clone()
	op := f.lease.Operation
	f.lease.Operation, err = store.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AbandonProjectEnvironmentCloneObjectWriteFences(ctx, f.lease); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("unknown dispatched retirement reopened source: %v", err)
	}
	if err := store.UpsertAppEnvInScope(ctx, op.AccountID, plan.appID, "production", "CAPTURED", "changed-after-abandonment"); err != nil {
		t.Fatal(err)
	}
	provider.beforeRevoke = nil
	provider.writers = 1
	var observation grantrevocation.Observation
	f.lease, observation, err = f.srv.revokeProjectEnvironmentCloneObjectNativeGrants(ctx, f.lease, plan)
	if err != nil || observation.Drained() || !reflect.DeepEqual(original, provider.journal) {
		t.Fatalf("compensation replaced or presumed original retirement: %+v %v", observation, err)
	}
	provider.writers = 0
	f.lease, observation, err = f.srv.revokeProjectEnvironmentCloneObjectNativeGrants(ctx, f.lease, plan)
	if err != nil || !observation.Drained() {
		t.Fatalf("compensation failed to drain original retirement: %+v %v", observation, err)
	}
	if err := store.AbandonProjectEnvironmentCloneObjectWriteFences(ctx, f.lease); err != nil {
		t.Fatal(err)
	}
}
