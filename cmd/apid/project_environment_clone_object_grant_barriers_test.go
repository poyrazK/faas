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

type cloneGrantBarrierStore struct {
	*cloneGrantRevocationFailureStore
	loseAbandon bool
}

func (s *cloneGrantBarrierStore) AbandonProjectEnvironmentCloneObjectWriteFences(ctx context.Context, l state.ProjectEnvironmentCloneLease) error {
	err := s.PgStore.AbandonProjectEnvironmentCloneObjectWriteFences(ctx, l)
	if err == nil && s.loseAbandon {
		s.loseAbandon = false
		return objectstorage.ErrUnavailable
	}
	return err
}

type cloneGrantBarrierFixture struct {
	cloneCoordinatorFixture
	store   *cloneGrantBarrierStore
	native  *cloneGrantRevocationProvider
	pg      *cloneCheckpointClosureProvider
	buckets []state.ObjectBucket
	request state.ObjectBucketMutation
}

func newCloneGrantBarrierFixture(t *testing.T) cloneGrantBarrierFixture {
	t.Helper()
	f, store, pg, buckets := cloneCaptureBarrierFixture(t)
	out := cloneGrantBarrierFixture{cloneCoordinatorFixture: f, pg: pg, buckets: buckets,
		store:  &cloneGrantBarrierStore{cloneGrantRevocationFailureStore: &cloneGrantRevocationFailureStore{PgStore: store.PgStore}},
		native: &cloneGrantRevocationProvider{Provider: f.objects, revoked: true}}
	out.srv.store = out.store
	configureCloneGrantProvider(t, out.srv, out.native)
	pg.drained = true
	var err error
	out.request, err = out.store.BeginObjectBucketMutation(t.Context(), buckets[0], state.ObjectBucketMutationRequest)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := out.store.BeginObjectBucketMutation(t.Context(), buckets[0], state.ObjectBucketMutationNativeGrant); err != nil {
			t.Fatal(err)
		}
	}
	out.native.beforeRevoke = func(plan grantrevocation.Plan) {
		objects, err := out.store.ProjectEnvironmentCloneObjectWriteFencesForLease(t.Context(), out.lease)
		if err != nil || len(objects) != 2 {
			t.Fatalf("retirement dispatched before all object holds: %v", err)
		}
		postgres, err := out.store.ProjectEnvironmentClonePostgresWriteFencesForLease(t.Context(), out.lease)
		if err != nil || len(postgres) != 1 {
			t.Fatalf("retirement dispatched before PostgreSQL hold: %v", err)
		}
		if plan.Scope.OperationID != out.lease.Operation.ID || plan.Scope.BucketID != buckets[0].ID {
			t.Fatal("provider received a substituted source")
		}
	}
	return out
}

func (f *cloneGrantBarrierFixture) handoff(t *testing.T) state.ProjectEnvironmentCloneLease {
	t.Helper()
	old := f.lease
	if err := f.store.ReleaseProjectEnvironmentCloneLease(t.Context(), old, 0); err != nil {
		t.Fatal(err)
	}
	var err error
	f.lease, err = f.store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil || f.lease.Operation.ID != old.Operation.ID {
		t.Fatalf("replacement worker failed to claim original operation: %v", err)
	}
	f.native.beforeRevoke = nil
	return old
}

func (f *cloneGrantBarrierFixture) compensate(t *testing.T) {
	t.Helper()
	op := f.lease.Operation
	var err error
	f.lease.Operation, err = f.store.AdvanceProjectEnvironmentCloneOperation(t.Context(), op.AccountID, op.ProjectID, op.ID,
		op.Status, state.CloneOperationCompensating, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
}

func TestPGCloneGrantBarriersRecoverRetirementAcknowledgements(t *testing.T) {
	for _, fault := range []string{"revoke_reply", "record_reply"} {
		t.Run(fault, func(t *testing.T) {
			f := newCloneGrantBarrierFixture(t)
			if fault == "revoke_reply" {
				f.native.fault = fault
			} else {
				f.store.fault = fault
			}
			var result cloneCaptureBarrierObservation
			var err error
			f.lease, result, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(t.Context(), f.lease)
			if err == nil || !reflect.DeepEqual(result, cloneCaptureBarrierObservation{}) {
				t.Fatalf("lost acknowledgement supplied capture evidence: %+v %v", result, err)
			}
			original := f.native.journal.Clone()
			if len(original.GrantIDs) != 2 {
				t.Fatal("driver did not retain the original native selection")
			}
			stale := f.handoff(t)
			if _, result, err := f.srv.prepareProjectEnvironmentCloneCaptureBarriers(t.Context(), stale); !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(result, cloneCaptureBarrierObservation{}) {
				t.Fatalf("stale worker resumed barriers: %+v %v", result, err)
			}
			f.lease, result, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(t.Context(), f.lease)
			if err != nil || result.instrumentedWritersDrained || len(result.objectRetirements) != 1 || !result.objectRetirements[0].Drained() ||
				!reflect.DeepEqual(f.native.journal, original) || result.objects[0].NativeGrants != 0 || result.objects[0].Requests != 1 {
				t.Fatalf("barriers failed original retirement recovery: %+v %v", result, err)
			}
			if err := f.store.FinishObjectBucketMutation(t.Context(), f.request); err != nil {
				t.Fatal(err)
			}
			calls := f.native.revokes
			f.lease, result, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(t.Context(), f.lease)
			if err != nil || !result.instrumentedWritersDrained || len(result.objectRetirements) != 1 || f.native.revokes != calls {
				t.Fatalf("drained resume redispatched or omitted evidence: %+v %v", result, err)
			}
			assertCloneCaptureHasNoPoint(t, f.cloneCoordinatorFixture)
		})
	}
}

func TestPGCloneGrantBarriersKeepBusyNativeAndRequestWritersSeparate(t *testing.T) {
	f := newCloneGrantBarrierFixture(t)
	f.native.writers = 1
	var result cloneCaptureBarrierObservation
	var err error
	f.lease, result, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(t.Context(), f.lease)
	if err != nil || result.instrumentedWritersDrained || len(result.objectRetirements) != 1 || result.objectRetirements[0].Drained() ||
		result.objects[0].NativeGrants != 2 || result.objects[0].Requests != 1 {
		t.Fatalf("busy native writes were settled: %+v %v", result, err)
	}
	f.native.writers = 0
	f.lease, result, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(t.Context(), f.lease)
	if err != nil || result.instrumentedWritersDrained || !result.objectRetirements[0].Drained() || result.objects[0].NativeGrants != 0 || result.objects[0].Requests != 1 {
		t.Fatalf("native retirement settled a synchronous request: %+v %v", result, err)
	}
	assertCloneCaptureHasNoPoint(t, f.cloneCoordinatorFixture)
}

func TestPGCloneGrantBarriersReobserveLocallyDrainedRetirements(t *testing.T) {
	for _, fault := range []string{"missing_journal", "hash", "identity", "busy", "unrevoked"} {
		t.Run(fault, func(t *testing.T) {
			f := newCloneGrantBarrierFixture(t)
			var result cloneCaptureBarrierObservation
			var err error
			f.lease, result, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(t.Context(), f.lease)
			if err != nil || len(result.objectRetirements) != 1 || !result.objectRetirements[0].Drained() {
				t.Fatalf("initial retirement failed: %v", err)
			}
			if err := f.store.FinishObjectBucketMutation(t.Context(), f.request); err != nil {
				t.Fatal(err)
			}
			f.handoff(t)
			calls, reads := f.native.revokes, f.native.reads
			f.native.fault = fault
			if fault == "busy" {
				f.native.writers = 1
			}
			if fault == "unrevoked" {
				f.native.revoked = false
			}
			f.lease, result, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(t.Context(), f.lease)
			if err == nil || !reflect.DeepEqual(result, cloneCaptureBarrierObservation{}) || f.native.revokes != calls || f.native.reads != reads+1 {
				t.Fatalf("local zero counts hid invalid provider evidence: %+v %v", result, err)
			}
			fences, err := f.store.ProjectEnvironmentCloneObjectWriteFencesForLease(t.Context(), f.lease)
			if err != nil || len(fences) != 2 || fences[0].NativeGrants != 0 {
				t.Fatalf("failed observation discarded holds: %+v %v", fences, err)
			}
			assertCloneCaptureHasNoPoint(t, f.cloneCoordinatorFixture)
		})
	}
}

func TestPGCloneGrantBarriersPreflightUnsupportedNativeCapabilities(t *testing.T) {
	for _, fault := range []string{"unsupported", "registry_missing"} {
		t.Run(fault, func(t *testing.T) {
			f := newCloneGrantBarrierFixture(t)
			configureCloneGrantProvider(t, f.srv, f.objects)
			if fault == "registry_missing" {
				f.srv.objectStorage = nil
			}
			var err error
			f.lease, _, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(t.Context(), f.lease)
			if !errors.Is(err, objectstorage.ErrUnsupported) || f.pg.roles != 0 || f.pg.closes != 0 || f.native.revokes != 0 {
				t.Fatalf("unsupported native capability dispatched provider work: %v", err)
			}
			fences, err := f.store.ProjectEnvironmentCloneObjectWriteFencesForLease(t.Context(), f.lease)
			if err != nil || len(fences) != 2 || fences[0].NativeGrants != 2 || fences[0].Requests != 1 {
				t.Fatalf("preflight failure discarded source holds: %+v %v", fences, err)
			}
			if _, err := f.store.ProjectEnvironmentCloneObjectGrantRevocationForLease(t.Context(), f.lease, f.buckets[0].ID); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("unsupported capability persisted retirement intent: %v", err)
			}
		})
	}
}

type cloneMultipleGrantProvider struct {
	objectstorage.Provider
	byBucket map[string]*cloneGrantRevocationProvider
}

func (p *cloneMultipleGrantProvider) RevokeNativeWriteGrants(ctx context.Context, plan grantrevocation.Plan) error {
	return p.byBucket[plan.Scope.BucketID].RevokeNativeWriteGrants(ctx, plan)
}

func (p *cloneMultipleGrantProvider) ObserveNativeWriteGrantRevocation(ctx context.Context, plan grantrevocation.Plan) (grantrevocation.Observation, error) {
	return p.byBucket[plan.Scope.BucketID].ObserveNativeWriteGrantRevocation(ctx, plan)
}

func TestPGCloneGrantBarriersRecoverPartiallyRetiredBucketRoster(t *testing.T) {
	f := newCloneGrantBarrierFixture(t)
	second := &cloneGrantRevocationProvider{Provider: f.objects, revoked: true, fault: "revoke_reply"}
	provider := &cloneMultipleGrantProvider{Provider: f.objects, byBucket: map[string]*cloneGrantRevocationProvider{f.buckets[0].ID: f.native, f.buckets[1].ID: second}}
	configureCloneGrantProvider(t, f.srv, provider)
	if _, err := f.store.BeginObjectBucketMutation(t.Context(), f.buckets[1], state.ObjectBucketMutationNativeGrant); err != nil {
		t.Fatal(err)
	}
	var result cloneCaptureBarrierObservation
	var err error
	f.lease, result, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(t.Context(), f.lease)
	if err == nil || !reflect.DeepEqual(result, cloneCaptureBarrierObservation{}) || f.native.revokes != 1 || second.revokes != 1 {
		t.Fatalf("partial retirement supplied complete evidence: %+v %v", result, err)
	}
	firstPlan, secondPlan := f.native.journal.Clone(), second.journal.Clone()
	f.handoff(t)
	f.lease, result, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(t.Context(), f.lease)
	if err != nil || len(result.objectRetirements) != 2 || result.instrumentedWritersDrained || f.native.revokes != 1 || second.revokes != 2 ||
		!reflect.DeepEqual(firstPlan, f.native.journal) || !reflect.DeepEqual(secondPlan, second.journal) || result.objects[0].NativeGrants != 0 || result.objects[1].NativeGrants != 0 {
		t.Fatalf("partial roster recovery changed original retirements: %+v %v", result, err)
	}
	assertCloneCaptureHasNoPoint(t, f.cloneCoordinatorFixture)
}

func TestPGCloneGrantBarrierAbandonmentResumesOnlyDispatchedRetirement(t *testing.T) {
	for _, fault := range []string{"revoke_reply", "record_reply"} {
		t.Run(fault, func(t *testing.T) {
			f := newCloneGrantBarrierFixture(t)
			f.native.writers = 1
			if fault == "revoke_reply" {
				f.native.fault = fault
			} else {
				f.store.fault = fault
			}
			var err error
			f.lease, _, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(t.Context(), f.lease)
			if err == nil {
				t.Fatal("lost capture acknowledgement was ignored")
			}
			original := f.native.journal.Clone()
			f.compensate(t)
			if err := f.store.UpsertAppEnvInScope(t.Context(), f.lease.Operation.AccountID, f.buckets[0].AppID, "production", "CAPTURED", "edited-during-abandonment"); err != nil {
				t.Fatal(err)
			}
			f.handoff(t)
			f.lease, err = f.srv.abandonProjectEnvironmentCloneObjectWriteFences(t.Context(), f.lease)
			if !errors.Is(err, errCloneCompensationUnavailable) || !reflect.DeepEqual(f.native.journal, original) {
				t.Fatalf("busy compensation replaced or settled original intent: %v", err)
			}
			fences, err := f.store.ProjectEnvironmentCloneObjectWriteFencesForLease(t.Context(), f.lease)
			if err != nil || len(fences) != 2 || fences[0].NativeGrants != 2 {
				t.Fatalf("busy compensation released part of the source roster: %+v %v", fences, err)
			}
			f.native.writers, f.store.loseAbandon = 0, true
			f.lease, err = f.srv.abandonProjectEnvironmentCloneObjectWriteFences(t.Context(), f.lease)
			if !errors.Is(err, objectstorage.ErrUnavailable) {
				t.Fatalf("lost fence-release acknowledgement was ignored: %v", err)
			}
			f.handoff(t)
			calls, reads := f.native.revokes, f.native.reads
			f.lease, err = f.srv.abandonProjectEnvironmentCloneObjectWriteFences(t.Context(), f.lease)
			if err != nil || f.native.revokes != calls || f.native.reads != reads {
				t.Fatalf("released source replay initiated more native work: %v", err)
			}
			barrier, err := f.store.AcquireObjectBucketWriteFence(t.Context(), f.buckets[0], uuid.NewString())
			if err != nil || barrier.NativeGrants != 0 || barrier.Requests != 1 {
				t.Fatalf("compensation erased synchronous request: %+v %v", barrier, err)
			}
			if err := f.store.ReleaseObjectBucketWriteFence(t.Context(), f.buckets[0], barrier.Token); err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.BeginObjectBucketMutation(t.Context(), f.buckets[0], state.ObjectBucketMutationRequest); err != nil {
				t.Fatalf("drained abandonment did not reopen source admission: %v", err)
			}
		})
	}
}

func TestPGCloneGrantBarrierAbandonmentDoesNotStartReservedRetirement(t *testing.T) {
	for _, reserved := range []bool{false, true} {
		t.Run(map[bool]string{false: "no_intent", true: "reserved"}[reserved], func(t *testing.T) {
			f := newCloneGrantBarrierFixture(t)
			if reserved {
				f.store.fault = "dispatch_before"
				var err error
				f.lease, _, err = f.srv.prepareProjectEnvironmentCloneCaptureBarriers(t.Context(), f.lease)
				if !errors.Is(err, objectstorage.ErrUnavailable) {
					t.Fatalf("retirement was not interrupted before dispatch: %v", err)
				}
				intent, err := f.store.ProjectEnvironmentCloneObjectGrantRevocationForLease(t.Context(), f.lease, f.buckets[0].ID)
				if err != nil || intent.State != "reserved" || !intent.RequestStartedAt.IsZero() {
					t.Fatalf("retirement was already dispatched: %+v %v", intent, err)
				}
			} else {
				for _, b := range f.buckets {
					if _, err := f.store.AcquireProjectEnvironmentCloneObjectWriteFence(t.Context(), f.lease, b.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			f.compensate(t)
			f.srv.objectStorage = nil // no provider is needed for undispatched intent
			var err error
			f.lease, err = f.srv.abandonProjectEnvironmentCloneObjectWriteFences(t.Context(), f.lease)
			if err != nil || f.native.revokes != 0 || f.native.reads != 0 {
				t.Fatalf("abandonment initiated native retirement: %v", err)
			}
			barrier, err := f.store.AcquireObjectBucketWriteFence(t.Context(), f.buckets[0], uuid.NewString())
			if err != nil || barrier.NativeGrants != 2 || barrier.Requests != 1 {
				t.Fatalf("undispatched abandonment consumed original writers: %+v %v", barrier, err)
			}
			if err := f.store.ReleaseObjectBucketWriteFence(t.Context(), f.buckets[0], barrier.Token); err != nil {
				t.Fatal(err)
			}
		})
	}
}
