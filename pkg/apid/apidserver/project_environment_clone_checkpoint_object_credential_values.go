package apidserver

import (
	"context"
	"errors"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type checkpointObjectCredentialValueStore interface {
	state.ProjectEnvironmentCloneCheckpointObjectCredentialReservationStore
	state.ProjectEnvironmentCloneCheckpointObjectCredentialValueStore
}

// Local key generation has no external grant effects. The sealed matrix commits
// before publication; recovery reads it before invoking any key generation.
func (s *server) prepareCheckpointObjectCredentialValues(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointReleaseDriver) (state.ProjectEnvironmentCloneLease, state.ProjectEnvironmentCloneCheckpointObjectCredentialValues, error) {
	zero := state.ProjectEnvironmentCloneCheckpointObjectCredentialValues{}
	store, ok := s.store.(checkpointObjectCredentialValueStore)
	if !ok {
		return l, zero, state.ErrProjectEnvironmentCloneBindingCaptureUnavailable
	}
	var err error
	l, _, err = s.checkpointObjectTargetsForLease(ctx, l, d)
	if err != nil {
		return l, zero, err
	}
	retention, err := s.checkpointTargetRetention(ctx, l, d)
	if err != nil {
		return l, zero, err
	}
	r, err := store.ReserveProjectEnvironmentCloneCheckpointObjectCredentials(ctx, l, retention)
	if err != nil {
		return l, zero, err
	}
	out, err := store.ProjectEnvironmentCloneCheckpointObjectCredentialValues(ctx, l, retention)
	if err == nil || !errors.Is(err, state.ErrNotFound) {
		return l, out, err
	}
	out, err = s.newCheckpointObjectCredentialValues(ctx, r)
	if err != nil {
		return l, zero, err
	}
	retention, err = s.checkpointTargetRetention(ctx, l, d)
	if err != nil {
		return l, zero, err
	}
	out, err = store.PrepareProjectEnvironmentCloneCheckpointObjectCredentialValues(ctx, l, out, retention)
	return l, out, err
}

func (s *server) newCheckpointObjectCredentialValues(ctx context.Context, r state.ProjectEnvironmentCloneCheckpointObjectCredentialReservation) (state.ProjectEnvironmentCloneCheckpointObjectCredentialValues, error) {
	zero := state.ProjectEnvironmentCloneCheckpointObjectCredentialValues{}
	out := state.ProjectEnvironmentCloneCheckpointObjectCredentialValues{OperationID: r.Inputs.OperationID, ReservationSHA256: r.SHA256, Preparations: []state.ProjectEnvironmentCloneObjectCredentialPreparation{}}
	if len(r.Members) == 0 {
		for _, b := range r.Inputs.Buckets {
			if len(b.Credentials) != 0 {
				return zero, state.ErrConflict
			}
		}
		out.SHA256 = state.ProjectEnvironmentCloneCheckpointObjectCredentialValuesSHA256(out)
		return out, nil
	}
	recipient := hostSecretRecipient()
	if !s.object.ObjectStorageEnabled() || recipient == nil {
		return zero, objectstorage.ErrUnavailable
	}
	out.Endpoint, out.Region = s.objectStorage.PublicEndpoint, s.objectStorage.PublicRegion
	account, err := s.store.AccountByID(ctx, r.Inputs.AccountID)
	if err != nil {
		return zero, err
	}
	n := 0
	for _, b := range r.Inputs.Buckets {
		for _, slot := range b.Credentials {
			if n >= len(r.Members) {
				return zero, state.ErrConflict
			}
			member := r.Members[n]
			n++
			access, key, err := api.GenerateObjectS3Credential()
			if err != nil {
				return zero, err
			}
			sealed, err := secretbox.SealBytes(recipient, s3gateway.CredentialSecretNamespace, []byte(key), 64)
			if err != nil {
				return zero, err
			}
			c := state.ObjectS3Credential{ID: member.TargetCredentialID, AccountID: r.Inputs.AccountID, BucketID: b.Target.TargetBucketID, AccessKeyID: access, SecretSealed: sealed, KID: recipient.String(), Label: slot.Label, Permission: slot.Permission, Status: state.ObjectS3CredentialStatusActive, ManagedAppID: slot.ManagedAppID, ManagedScope: slot.TargetScope, ManagedPrefix: slot.ManagedPrefix}
			p := state.ProjectEnvironmentCloneObjectCredentialPreparation{OperationID: out.OperationID, AppID: b.AppID, SourceBucketID: b.Source.ID, SourceCredentialID: slot.SourceCredentialID, Credential: c, Secrets: []state.AppSecret{}}
			if slot.ManagedAppID != "" {
				bucketName := state.ProjectEnvironmentCloneObjectBucketName(state.ProjectEnvironmentCloneOperation{ID: r.Inputs.OperationID, ProjectID: r.Inputs.ProjectID, TargetEnvironment: r.Inputs.TargetEnvironment}, b.AppID, b.Source.ID)
				values := object.ObjectStorageBindingSecretValues(object.ObjectStorageBindingSecretKeys(slot.ManagedPrefix), out.Endpoint, out.Region, bucketName, access, key)
				secrets, problem := s.object.SealObjectStorageBindingValues(account, state.App{ID: b.AppID}, c.ID, slot.TargetScope, values, api.MustLimitsFor(account.Plan))
				if problem != nil {
					return zero, problem
				}
				for i := range secrets {
					secrets[i].SecretClass = state.SecretClassPersistent
					secrets[i].SecretVersion = 1
				}
				sort.Slice(secrets, func(i, j int) bool { return secrets[i].Key < secrets[j].Key })
				p.Secrets = secrets
			}
			out.Preparations = append(out.Preparations, p)
		}
	}
	if n != len(r.Members) {
		return zero, state.ErrConflict
	}
	out.SHA256 = state.ProjectEnvironmentCloneCheckpointObjectCredentialValuesSHA256(out)
	return out, nil
}
