package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type cloneObjectCredentialWorkerStore interface {
	cloneObjectWorkerStore
	state.ProjectEnvironmentCloneObjectCredentialStore
}

// Credential identities and sealed runtime values live in an atomic private
// receipt. A retry reads that receipt before generating any new key material.
// Only managed credentials contribute IDs to environment materialization;
// standalone customer credentials are recreated with their captured rights.
func (s *server) prepareProjectEnvironmentCloneObjectCredentials(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, []string, int, error) {
	store, ok := s.store.(cloneObjectCredentialWorkerStore)
	if !ok {
		return lease, nil, 0, state.ErrProjectEnvironmentCloneBindingCaptureUnavailable
	}
	lease, err := renewCloneObjectWorkerLease(ctx, store, lease, state.CloneOperationCopying)
	if err != nil {
		return lease, nil, 0, err
	}
	plans, err := s.capturedProjectEnvironmentObjectPlans(ctx, lease.Operation)
	if err != nil {
		return lease, nil, 0, err
	}
	indices, _, err := validateCapturedProjectEnvironmentObjectResources(plans, lease.Operation.Resources, false)
	if err != nil {
		return lease, nil, 0, err
	}
	for _, plan := range plans {
		if lease.Operation.Resources[indices[plan.source.ID]].Status != "ready" {
			return lease, nil, 0, state.ErrConflict
		}
	}
	ids, secretCount := []string{}, 0
	for _, plan := range plans {
		for _, source := range plan.source.Credentials {
			lease, err = renewCloneObjectWorkerLease(ctx, store, lease, state.CloneOperationCopying)
			if err != nil {
				return lease, nil, 0, err
			}
			callCtx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
			prepared, err := s.prepareCapturedCloneObjectCredential(callCtx, store, lease, plan, source, lease.Operation.Resources[indices[plan.source.ID]].TargetID)
			cancel()
			if err != nil {
				return lease, nil, 0, fmt.Errorf("prepare captured object credential: %w", err)
			}
			if prepared.Credential.ManagedAppID != "" {
				ids = append(ids, prepared.Credential.ID)
				secretCount += len(prepared.Secrets)
			}
		}
	}
	return lease, ids, secretCount, nil
}

func (s *server) prepareCapturedCloneObjectCredential(ctx context.Context, store cloneObjectCredentialWorkerStore, lease state.ProjectEnvironmentCloneLease, plan capturedProjectEnvironmentObjectPlan, source state.ProjectEnvironmentCloneObjectCredential, targetID string) (state.ProjectEnvironmentCloneObjectCredentialPreparation, error) {
	prepared, err := store.ProjectEnvironmentCloneObjectCredentialForLease(ctx, lease, source.ID)
	if err == nil || !errors.Is(err, state.ErrNotFound) {
		return prepared, err
	}
	bucket, err := store.ProjectEnvironmentCloneObjectBucketForLease(ctx, lease, plan.appID, plan.source.ID, targetID)
	if err != nil {
		return prepared, err
	}
	request, err := s.newCapturedCloneObjectCredential(ctx, lease.Operation, plan, source, bucket)
	if err != nil {
		return prepared, err
	}
	return store.PrepareProjectEnvironmentCloneObjectCredential(ctx, lease, request)
}

func (s *server) newCapturedCloneObjectCredential(ctx context.Context, op state.ProjectEnvironmentCloneOperation, plan capturedProjectEnvironmentObjectPlan, source state.ProjectEnvironmentCloneObjectCredential, bucket state.ObjectBucket) (state.ProjectEnvironmentCloneObjectCredentialRequest, error) {
	request := state.ProjectEnvironmentCloneObjectCredentialRequest{AppID: plan.appID, SourceBucketID: plan.source.ID, SourceCredentialID: source.ID}
	if !s.objectStorageEnabled() || setSecretRecipient() == nil {
		return request, objectstorage.ErrUnavailable
	}
	recipient := setSecretRecipient()
	accessKey, secretKey, err := api.GenerateObjectS3Credential()
	if err != nil {
		return request, fmt.Errorf("generate stage object credential: %w", err)
	}
	sealed, err := secretbox.SealBytes(recipient, s3gateway.CredentialSecretNamespace, []byte(secretKey), 64)
	if err != nil {
		return request, fmt.Errorf("seal stage object credential: %w", err)
	}
	credential := state.ObjectS3Credential{ID: uuid.NewString(), AccountID: op.AccountID, BucketID: bucket.ID, AccessKeyID: accessKey,
		SecretSealed: sealed, KID: recipient.String(), Label: source.Label, Permission: source.Permission, Status: state.ObjectS3CredentialStatusActive,
		ManagedAppID: source.ManagedAppID, ManagedPrefix: source.ManagedPrefix}
	request.Target = state.ObjectS3ComputeBindingCreateRequest{Credential: credential, MaxCredentialsPerBucket: api.MaxObjectS3CredentialsPerBucket}
	if source.ManagedAppID == "" {
		return request, nil
	}
	account, err := s.store.AccountByID(ctx, op.AccountID)
	if err != nil {
		return request, err
	}
	limits := api.MustLimitsFor(account.Plan)
	values := objectStorageBindingSecretValues(objectStorageBindingSecretKeys(source.ManagedPrefix), s.objectStorage.PublicEndpoint, s.objectStorage.PublicRegion, bucket.Name, accessKey, secretKey)
	secrets, problem := s.sealObjectStorageBindingValues(account, state.App{ID: plan.appID}, credential.ID, op.TargetEnvironment, values, limits)
	if problem != nil {
		return request, problem
	}
	request.Target.Credential.ManagedScope = op.TargetEnvironment
	request.Target.Secrets, request.Target.MaxSecretsPerApp = secrets, limits.SecretCountMax
	return request, nil
}
