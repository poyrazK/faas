package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type clonePostgresBindingWorkerStore interface {
	state.ProjectEnvironmentCloneWorkerLeaseStore
	state.ProjectEnvironmentCloneBindingCaptureStore
	state.ProjectEnvironmentClonePostgresBindingStore
}

func (s *server) prepareProjectEnvironmentClonePostgresBindings(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, []string, int, error) {
	leases, ok := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	if !ok {
		return lease, nil, 0, state.ErrProjectEnvironmentCloneBindingCaptureUnavailable
	}
	lease, err := leases.RenewProjectEnvironmentCloneLease(ctx, lease, projectEnvironmentCloneWorkerLeaseDuration)
	if err != nil {
		return lease, nil, 0, err
	}
	if lease.Operation.Status != state.CloneOperationCopying {
		return lease, nil, 0, state.ErrConflict
	}
	capture, ok := s.store.(state.ProjectEnvironmentCloneBindingCaptureStore)
	if !ok {
		return lease, nil, 0, state.ErrProjectEnvironmentCloneBindingCaptureUnavailable
	}
	views, err := capture.ProjectEnvironmentCloneBindings(ctx, lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID)
	if err != nil {
		return lease, nil, 0, err
	}
	ids := []string{}
	for _, view := range views {
		for _, source := range view.Postgres {
			store, ok := s.store.(clonePostgresBindingWorkerStore)
			if !ok {
				return lease, nil, 0, state.ErrProjectEnvironmentCloneBindingCaptureUnavailable
			}
			lease, err = leases.RenewProjectEnvironmentCloneLease(ctx, lease, projectEnvironmentCloneWorkerLeaseDuration)
			if err != nil {
				return lease, nil, 0, err
			}
			callCtx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
			prepared, err := s.prepareCapturedClonePostgresBinding(callCtx, store, lease, source.ID)
			cancel()
			if err != nil {
				return lease, nil, 0, fmt.Errorf("prepare captured PostgreSQL binding: %w", err)
			}
			ids = append(ids, prepared.Binding.ID)
		}
	}
	return lease, ids, len(ids), nil
}

func (s *server) prepareCapturedClonePostgresBinding(ctx context.Context, store clonePostgresBindingWorkerStore, lease state.ProjectEnvironmentCloneLease, sourceID string) (state.ProjectEnvironmentClonePostgresBindingPreparation, error) {
	target, prepared, err := store.ProjectEnvironmentClonePostgresBindingForLease(ctx, lease, sourceID)
	if err == nil && prepared != nil {
		return *prepared, nil
	}
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return state.ProjectEnvironmentClonePostgresBindingPreparation{}, err
	}
	if s.managedPostgresBindings == nil || setSecretRecipient == nil || hostHMACKey == nil || setSecretRecipient() == nil || len(hostHMACKey()) == 0 {
		return state.ProjectEnvironmentClonePostgresBindingPreparation{}, managedpostgres.ErrUnavailable
	}
	account, err := s.store.AccountByID(ctx, lease.Operation.AccountID)
	if err != nil {
		return state.ProjectEnvironmentClonePostgresBindingPreparation{}, err
	}
	if target.ID == "" {
		target, _, err = store.ReserveProjectEnvironmentClonePostgresBinding(ctx, lease, sourceID)
		if err != nil {
			return state.ProjectEnvironmentClonePostgresBindingPreparation{}, err
		}
	}
	sink := &clonePostgresPreparationSink{store: store, lease: lease, sourceID: sourceID, maxSecrets: api.MustLimitsFor(account.Plan).SecretCountMax,
		sealer: &appSecretCredentialSink{recipient: setSecretRecipient, hmacKey: hostHMACKey}}
	binding := managedpostgres.Binding{ID: target.ID, AccountID: target.AccountID, DatabaseID: target.DatabaseID, AppID: target.AppID, Scope: target.Scope,
		EnvironmentKey: target.EnvironmentKey, Access: managedpostgres.CredentialAccess(target.Access), CredentialGeneration: target.CredentialGeneration, State: managedpostgres.BindingStateProvisioning}
	if _, _, err := s.managedPostgresBindings.PrepareReservedCredential(ctx, binding, lease.Operation.ID, sink); err != nil {
		return state.ProjectEnvironmentClonePostgresBindingPreparation{}, err
	}
	_, prepared, err = store.ProjectEnvironmentClonePostgresBindingForLease(ctx, lease, sourceID)
	if err != nil {
		return state.ProjectEnvironmentClonePostgresBindingPreparation{}, err
	}
	if prepared == nil {
		return state.ProjectEnvironmentClonePostgresBindingPreparation{}, state.ErrConflict
	}
	return *prepared, nil
}

type clonePostgresPreparationSink struct {
	store      state.ProjectEnvironmentClonePostgresBindingStore
	lease      state.ProjectEnvironmentCloneLease
	sourceID   string
	maxSecrets int
	sealer     *appSecretCredentialSink
}

func (s *clonePostgresPreparationSink) Put(ctx context.Context, binding managedpostgres.Binding, material managedpostgres.CredentialMaterial) (string, error) {
	secret, ref, err := s.sealer.seal(binding, material)
	if err != nil {
		return "", err
	}
	_, err = s.store.PrepareProjectEnvironmentClonePostgresBinding(ctx, s.lease, state.ProjectEnvironmentClonePostgresBindingRequest{
		SourceBindingID: s.sourceID, TargetBindingID: binding.ID, ProviderIdentityID: material.ProviderIdentityID, CredentialRef: ref, Secret: secret, MaxSecretsPerApp: s.maxSecrets})
	return ref, err
}

func (*clonePostgresPreparationSink) Delete(context.Context, managedpostgres.Binding) error {
	return managedpostgres.ErrUnsupported
}
