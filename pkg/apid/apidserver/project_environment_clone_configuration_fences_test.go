//go:build !no_pg

// adr: 590
package apidserver_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type cloneConfigurationFenceFailureStore struct {
	PgStore *state.PgStore
	fault   string
}

func (s *cloneConfigurationFenceFailureStore) AcquireProjectEnvironmentCloneConfigurationFence(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneConfigurationFence, error) {
	fence, err := s.PgStore.AcquireProjectEnvironmentCloneConfigurationFence(ctx, lease)
	if err == nil && s.fault == "acquire_reply" {
		s.fault = ""
		return state.ProjectEnvironmentCloneConfigurationFence{}, objectstorage.ErrUnavailable
	}
	return fence, err
}

func (s *cloneConfigurationFenceFailureStore) ProjectEnvironmentCloneConfigurationFenceForLease(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneConfigurationFence, error) {
	fence, err := s.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(ctx, lease)
	if err == nil {
		switch s.fault {
		case "missing_hold":
			return state.ProjectEnvironmentCloneConfigurationFence{}, state.ErrNotFound
		case "changed_identity":
			fence.SourceRevisionHash = strings.Repeat("f", 64)
		}
	}
	return fence, err
}

func (s *cloneConfigurationFenceFailureStore) AbandonProjectEnvironmentCloneConfigurationFence(ctx context.Context, lease state.ProjectEnvironmentCloneLease) error {
	err := s.PgStore.AbandonProjectEnvironmentCloneConfigurationFence(ctx, lease)
	if err == nil && s.fault == "abandon_reply" {
		s.fault = ""
		return objectstorage.ErrUnavailable
	}
	return err
}

func TestPGCloneStableCaptureBarriersKeepConfigurationHeldDuringProviderIO(t *testing.T) {
	f, store, provider, buckets := apidserver.CloneCaptureBarrierFixture(t)
	(*provider.Drained()) = true
	(*provider.OnDiscover()) = func(ctx context.Context) error {
		fence, err := store.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(ctx, (*f.Lease()))
		if err != nil || !apidserver.CloneConfigurationFenceMatches((*f.Lease()).Operation, fence) {
			t.Fatalf("provider dispatched without original configuration hold: %+v %v", fence, err)
		}
		if err := store.PgStore.UpsertAppEnvInScope(ctx, (*f.Lease()).Operation.AccountID, buckets[0].AppID, "production", "CAPTURED", "changed-during-capture"); !errors.Is(err, state.ErrProjectEnvironmentCloneConfigurationFenced) {
			t.Fatalf("source edit during remote IO was admitted: %v", err)
		}
		return nil
	}
	var result apidserver.CloneCaptureBarrierObservation
	var err error
	(*f.Lease()), result, err = (*f.Srv()).PrepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), (*f.Lease()))
	if err != nil || !(*result.InstrumentedWritersDrained()) || !apidserver.CloneConfigurationFenceMatches((*f.Lease()).Operation, (*result.ConfigurationFence())) {
		t.Fatalf("stable barrier preparation failed: %+v %v", result, err)
	}
	first := (*result.ConfigurationFence())
	if err := store.PgStore.ReleaseProjectEnvironmentCloneLease(t.Context(), (*f.Lease()), 0); err != nil {
		t.Fatal(err)
	}
	(*f.Lease()), err = store.PgStore.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	(*f.Lease()), result, err = (*f.Srv()).PrepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), (*f.Lease()))
	if err != nil || !reflect.DeepEqual(first, (*result.ConfigurationFence())) {
		t.Fatalf("replacement worker replaced configuration hold: %+v %v", result, err)
	}
	apidserver.AssertCloneCaptureHasNoPoint(t, f)
}

func TestPGCloneStableCaptureBarriersRecoverLostConfigurationHoldReply(t *testing.T) {
	f, base, provider, buckets := apidserver.CloneCaptureBarrierFixture(t)
	store := &cloneConfigurationFenceFailureStore{PgStore: base.PgStore, fault: "acquire_reply"}
	(*(*f.Srv()).Store()), (*provider.Drained()) = statetest.NewPg(store), true
	var result apidserver.CloneCaptureBarrierObservation
	var err error
	(*f.Lease()), result, err = (*f.Srv()).PrepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), (*f.Lease()))
	if !errors.Is(err, objectstorage.ErrUnavailable) || !reflect.DeepEqual(result, apidserver.CloneCaptureBarrierObservation{}) || (*provider.Roles()) != 0 || (*provider.Closes()) != 0 {
		t.Fatalf("lost hold reply dispatched providers or supplied evidence: %+v %v", result, err)
	}
	first, err := store.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(t.Context(), (*f.Lease()))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PgStore.UpsertAppEnvInScope(t.Context(), (*f.Lease()).Operation.AccountID, buckets[0].AppID, "production", "CAPTURED", "changed"); !errors.Is(err, state.ErrProjectEnvironmentCloneConfigurationFenced) {
		t.Fatalf("lost reply released source configuration: %v", err)
	}
	stale := (*f.Lease())
	if err := store.PgStore.ReleaseProjectEnvironmentCloneLease(t.Context(), (*f.Lease()), 0); err != nil {
		t.Fatal(err)
	}
	(*f.Lease()), err = store.PgStore.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, result, err := (*f.Srv()).PrepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), stale); !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(result, apidserver.CloneCaptureBarrierObservation{}) {
		t.Fatalf("stale worker resumed stable capture: %+v %v", result, err)
	}
	(*f.Lease()), result, err = (*f.Srv()).PrepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), (*f.Lease()))
	if err != nil || !reflect.DeepEqual((*result.ConfigurationFence()), first) || !(*result.InstrumentedWritersDrained()) {
		t.Fatalf("lost hold recovery changed original configuration: %+v %v", result, err)
	}
	apidserver.AssertCloneCaptureHasNoPoint(t, f)
}

func TestPGCloneStableCaptureBarriersRejectMissingOrChangedConfigurationHold(t *testing.T) {
	for _, fault := range []string{"missing_hold", "changed_identity"} {
		t.Run(fault, func(t *testing.T) {
			f, base, provider, _ := apidserver.CloneCaptureBarrierFixture(t)
			store := &cloneConfigurationFenceFailureStore{PgStore: base.PgStore, fault: fault}
			(*(*f.Srv()).Store()), (*provider.Drained()) = statetest.NewPg(store), true
			var result apidserver.CloneCaptureBarrierObservation
			var err error
			(*f.Lease()), result, err = (*f.Srv()).PrepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), (*f.Lease()))
			if err == nil || !reflect.DeepEqual(result, apidserver.CloneCaptureBarrierObservation{}) {
				t.Fatalf("missing configuration authority supplied evidence: %+v %v", result, err)
			}
			if held, err := store.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(t.Context(), (*f.Lease())); err != nil || !apidserver.CloneConfigurationFenceMatches((*f.Lease()).Operation, held) {
				t.Fatalf("failed observation discarded hold: %+v %v", held, err)
			}
			fences, err := store.PgStore.ProjectEnvironmentCloneObjectWriteFencesForLease(t.Context(), (*f.Lease()))
			if err != nil || len(fences) != 2 {
				t.Fatalf("failed configuration observation discarded data holds: %+v %v", fences, err)
			}
			apidserver.AssertCloneCaptureHasNoPoint(t, f)
		})
	}
}

func TestPGCloneStableCaptureBarriersRejectConfigurationDriftBeforeProviderIO(t *testing.T) {
	f, store, provider, buckets := apidserver.CloneCaptureBarrierFixture(t)
	if err := store.PgStore.UpsertAppEnvInScope(t.Context(), (*f.Lease()).Operation.AccountID, buckets[0].AppID, "production", "CAPTURED", "changed-before-hold"); err != nil {
		t.Fatal(err)
	}
	lease, result, err := (*f.Srv()).PrepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), (*f.Lease()))
	if !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(result, apidserver.CloneCaptureBarrierObservation{}) || (*provider.Roles()) != 0 || (*provider.Closes()) != 0 {
		t.Fatalf("changed configuration dispatched source work: %+v %v", result, err)
	}
	if _, err := store.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(t.Context(), lease); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed configuration capture retained a new hold: %v", err)
	}
	if holds, err := store.ProjectEnvironmentCloneObjectWriteFencesForLease(t.Context(), lease); err != nil || len(holds) != 0 {
		t.Fatalf("drift acquired data holds: %+v %v", holds, err)
	}
	apidserver.AssertCloneCaptureHasNoPoint(t, f)
}

func TestPGCloneStableCaptureBarriersAbandonmentRecoversLostConfigurationRelease(t *testing.T) {
	f, base, provider, buckets := apidserver.CloneCaptureBarrierFixture(t)
	store := &cloneConfigurationFenceFailureStore{PgStore: base.PgStore}
	(*(*f.Srv()).Store()), (*provider.Drained()) = statetest.NewPg(store), true
	var err error
	(*f.Lease()), _, err = (*f.Srv()).PrepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), (*f.Lease()))
	if err != nil {
		t.Fatal(err)
	}
	op := (*f.Lease()).Operation
	(*f.Lease()).Operation, err = store.PgStore.AdvanceProjectEnvironmentCloneOperation(t.Context(), op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	store.fault = "abandon_reply"
	(*f.Lease()), err = (*f.Srv()).ProcessProjectEnvironmentCloneLease(t.Context(), statetest.NewPg(store), (*f.Lease()))
	if !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatalf("lost configuration release reply was ignored: %v", err)
	}
	if err := store.PgStore.UpsertAppEnvInScope(t.Context(), op.AccountID, buckets[0].AppID, "production", "CAPTURED", "edited-after-abandonment"); err != nil {
		t.Fatalf("abandoned configuration remained fenced: %v", err)
	}
	if holds, err := store.PgStore.ProjectEnvironmentCloneObjectWriteFencesForLease(t.Context(), (*f.Lease())); err != nil || len(holds) != 2 {
		t.Fatalf("configuration release also released data: %+v %v", holds, err)
	}
	if err := store.PgStore.ReleaseProjectEnvironmentCloneLease(t.Context(), (*f.Lease()), 0); err != nil {
		t.Fatal(err)
	}
	(*f.Lease()), err = store.PgStore.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	(*f.Lease()), err = (*f.Srv()).AbandonProjectEnvironmentCloneConfigurationFence(t.Context(), (*f.Lease()))
	if err != nil {
		t.Fatalf("replacement worker failed committed release recovery: %v", err)
	}
	if _, err := store.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(t.Context(), (*f.Lease())); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("recovery reclaimed the abandoned hold: %v", err)
	}
	if (*f.Lease()).Operation.Status != state.CloneOperationCompensating {
		t.Fatal("configuration release completed other resource cleanup")
	}
}
