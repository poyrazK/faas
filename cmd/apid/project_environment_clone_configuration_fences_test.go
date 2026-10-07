//go:build !no_pg

// adr: 590
package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneConfigurationFenceFailureStore struct {
	*state.PgStore
	fault string
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
	f, store, provider, buckets := cloneCaptureBarrierFixture(t)
	provider.drained = true
	provider.onDiscover = func(ctx context.Context) error {
		fence, err := store.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(ctx, f.lease)
		if err != nil || !cloneConfigurationFenceMatches(f.lease.Operation, fence) {
			t.Fatalf("provider dispatched without original configuration hold: %+v %v", fence, err)
		}
		if err := store.UpsertAppEnvInScope(ctx, f.lease.Operation.AccountID, buckets[0].AppID, "production", "CAPTURED", "changed-during-capture"); !errors.Is(err, state.ErrProjectEnvironmentCloneConfigurationFenced) {
			t.Fatalf("source edit during remote IO was admitted: %v", err)
		}
		return nil
	}
	var result cloneCaptureBarrierObservation
	var err error
	f.lease, result, err = f.srv.prepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), f.lease)
	if err != nil || !result.instrumentedWritersDrained || !cloneConfigurationFenceMatches(f.lease.Operation, result.configurationFence) {
		t.Fatalf("stable barrier preparation failed: %+v %v", result, err)
	}
	first := result.configurationFence
	if err := store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.lease, result, err = f.srv.prepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), f.lease)
	if err != nil || !reflect.DeepEqual(first, result.configurationFence) {
		t.Fatalf("replacement worker replaced configuration hold: %+v %v", result, err)
	}
	assertCloneCaptureHasNoPoint(t, f)
}

func TestPGCloneStableCaptureBarriersRecoverLostConfigurationHoldReply(t *testing.T) {
	f, base, provider, buckets := cloneCaptureBarrierFixture(t)
	store := &cloneConfigurationFenceFailureStore{PgStore: base.PgStore, fault: "acquire_reply"}
	f.srv.store, provider.drained = store, true
	var result cloneCaptureBarrierObservation
	var err error
	f.lease, result, err = f.srv.prepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), f.lease)
	if !errors.Is(err, objectstorage.ErrUnavailable) || !reflect.DeepEqual(result, cloneCaptureBarrierObservation{}) || provider.roles != 0 || provider.closes != 0 {
		t.Fatalf("lost hold reply dispatched providers or supplied evidence: %+v %v", result, err)
	}
	first, err := store.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), f.lease.Operation.AccountID, buckets[0].AppID, "production", "CAPTURED", "changed"); !errors.Is(err, state.ErrProjectEnvironmentCloneConfigurationFenced) {
		t.Fatalf("lost reply released source configuration: %v", err)
	}
	stale := f.lease
	if err := store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, result, err := f.srv.prepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), stale); !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(result, cloneCaptureBarrierObservation{}) {
		t.Fatalf("stale worker resumed stable capture: %+v %v", result, err)
	}
	f.lease, result, err = f.srv.prepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), f.lease)
	if err != nil || !reflect.DeepEqual(result.configurationFence, first) || !result.instrumentedWritersDrained {
		t.Fatalf("lost hold recovery changed original configuration: %+v %v", result, err)
	}
	assertCloneCaptureHasNoPoint(t, f)
}

func TestPGCloneStableCaptureBarriersRejectMissingOrChangedConfigurationHold(t *testing.T) {
	for _, fault := range []string{"missing_hold", "changed_identity"} {
		t.Run(fault, func(t *testing.T) {
			f, base, provider, _ := cloneCaptureBarrierFixture(t)
			store := &cloneConfigurationFenceFailureStore{PgStore: base.PgStore, fault: fault}
			f.srv.store, provider.drained = store, true
			var result cloneCaptureBarrierObservation
			var err error
			f.lease, result, err = f.srv.prepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), f.lease)
			if err == nil || !reflect.DeepEqual(result, cloneCaptureBarrierObservation{}) {
				t.Fatalf("missing configuration authority supplied evidence: %+v %v", result, err)
			}
			if held, err := store.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(t.Context(), f.lease); err != nil || !cloneConfigurationFenceMatches(f.lease.Operation, held) {
				t.Fatalf("failed observation discarded hold: %+v %v", held, err)
			}
			fences, err := store.ProjectEnvironmentCloneObjectWriteFencesForLease(t.Context(), f.lease)
			if err != nil || len(fences) != 2 {
				t.Fatalf("failed configuration observation discarded data holds: %+v %v", fences, err)
			}
			assertCloneCaptureHasNoPoint(t, f)
		})
	}
}

func TestPGCloneStableCaptureBarriersRejectConfigurationDriftBeforeProviderIO(t *testing.T) {
	f, store, provider, buckets := cloneCaptureBarrierFixture(t)
	if err := store.UpsertAppEnvInScope(t.Context(), f.lease.Operation.AccountID, buckets[0].AppID, "production", "CAPTURED", "changed-before-hold"); err != nil {
		t.Fatal(err)
	}
	lease, result, err := f.srv.prepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), f.lease)
	if !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual(result, cloneCaptureBarrierObservation{}) || provider.roles != 0 || provider.closes != 0 {
		t.Fatalf("changed configuration dispatched source work: %+v %v", result, err)
	}
	if _, err := store.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(t.Context(), lease); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed configuration capture retained a new hold: %v", err)
	}
	if holds, err := store.ProjectEnvironmentCloneObjectWriteFencesForLease(t.Context(), lease); err != nil || len(holds) != 0 {
		t.Fatalf("drift acquired data holds: %+v %v", holds, err)
	}
	assertCloneCaptureHasNoPoint(t, f)
}

func TestPGCloneStableCaptureBarriersAbandonmentRecoversLostConfigurationRelease(t *testing.T) {
	f, base, provider, buckets := cloneCaptureBarrierFixture(t)
	store := &cloneConfigurationFenceFailureStore{PgStore: base.PgStore}
	f.srv.store, provider.drained = store, true
	var err error
	f.lease, _, err = f.srv.prepareProjectEnvironmentCloneStableCaptureBarriers(t.Context(), f.lease)
	if err != nil {
		t.Fatal(err)
	}
	op := f.lease.Operation
	f.lease.Operation, err = store.AdvanceProjectEnvironmentCloneOperation(t.Context(), op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	store.fault = "abandon_reply"
	f.lease, err = f.srv.processProjectEnvironmentCloneLease(t.Context(), store, f.lease)
	if !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatalf("lost configuration release reply was ignored: %v", err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), op.AccountID, buckets[0].AppID, "production", "CAPTURED", "edited-after-abandonment"); err != nil {
		t.Fatalf("abandoned configuration remained fenced: %v", err)
	}
	if holds, err := store.ProjectEnvironmentCloneObjectWriteFencesForLease(t.Context(), f.lease); err != nil || len(holds) != 2 {
		t.Fatalf("configuration release also released data: %+v %v", holds, err)
	}
	if err := store.ReleaseProjectEnvironmentCloneLease(t.Context(), f.lease, 0); err != nil {
		t.Fatal(err)
	}
	f.lease, err = store.ClaimNextProjectEnvironmentClone(t.Context(), uuid.NewString(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.lease, err = f.srv.abandonProjectEnvironmentCloneConfigurationFence(t.Context(), f.lease)
	if err != nil {
		t.Fatalf("replacement worker failed committed release recovery: %v", err)
	}
	if _, err := store.PgStore.ProjectEnvironmentCloneConfigurationFenceForLease(t.Context(), f.lease); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("recovery reclaimed the abandoned hold: %v", err)
	}
	if f.lease.Operation.Status != state.CloneOperationCompensating {
		t.Fatal("configuration release completed other resource cleanup")
	}
}
