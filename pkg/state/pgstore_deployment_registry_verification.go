package state

// adr: 387

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ DeploymentRegistryVerificationStore = (*PgStore)(nil)

func registryVerificationError(err error) error {
	var p *pgconn.PgError
	if errors.As(err, &p) {
		switch p.ConstraintName {
		case "deployment_registry_verification_missing":
			return ErrNotFound
		case "deployment_registry_verification_stale":
			return ErrApplicationStandardRuntimeStale
		case "deployment_registry_verification_busy":
			return ErrApplicationStandardRuntimeBusy
		case "deployment_registry_verification_immutable":
			return ErrInvalidArgument
		}
	}
	return mapErr(err)
}

func registryVerificationRow(row sqlc.DeploymentRegistryVerification) (DeploymentRegistryVerification, error) {
	var in DeploymentRegistryVerificationInput
	if err := json.Unmarshal(row.InputSnapshot, &in); err != nil {
		return DeploymentRegistryVerification{}, err
	}
	in.ID = pgUUIDString(row.ID)
	in.Proof.Evidence = &imagepublisher.ImageSignatureEvidence{Payload: append([]byte(nil), row.Payload...), Signature: append([]byte(nil), row.Signature...)}
	in, hash, err := prepareRegistryVerification(in)
	if err != nil {
		return DeploymentRegistryVerification{}, err
	}
	if hash != row.InputHash || in.AccountID != pgUUIDString(row.AccountID) || in.AppID != pgUUIDString(row.AppID) ||
		in.DeploymentID != pgUUIDString(row.DeploymentID) || in.WorkloadName != row.WorkloadName ||
		"sha256:"+standardReviewBytesDigest(row.Payload) != in.Proof.PayloadDigest || "sha256:"+standardReviewBytesDigest(row.Signature) != in.Proof.SignatureDigest ||
		!row.VerifiedAt.Valid || !row.ExpiresAt.Valid || !row.ExpiresAt.Time.After(row.VerifiedAt.Time) || row.ExpiresAt.Time.Sub(row.VerifiedAt.Time) > api.ImageSignatureVerificationTTL {
		return DeploymentRegistryVerification{}, fmt.Errorf("registry verification stored binding mismatch")
	}
	return DeploymentRegistryVerification{ID: in.ID, Input: in, InputHash: hash, VerifiedAt: row.VerifiedAt.Time, ExpiresAt: row.ExpiresAt.Time}, nil
}

func (s *PgStore) RecordDeploymentRegistryVerification(ctx context.Context, input DeploymentRegistryVerificationInput) (DeploymentRegistryVerification, error) {
	in, hash, err := prepareRegistryVerification(input)
	if err != nil {
		return DeploymentRegistryVerification{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DeploymentRegistryVerification{}, err
	}
	defer tx.Rollback(ctx)
	q := sqlc.New()
	raw, err := q.LockDeploymentRegistryVerification(ctx, tx, sqlc.LockDeploymentRegistryVerificationParams{
		AppID: mustPgUUID(in.AppID), DeploymentID: mustPgUUID(in.DeploymentID), AccountID: mustPgUUID(in.AccountID), WorkloadName: in.WorkloadName, Publisher: in.Proof.PublisherName,
	})
	if err != nil {
		return DeploymentRegistryVerification{}, registryVerificationError(err)
	}
	var current struct {
		AppID          string `json:"app_id"`
		AccountID      string `json:"account_id"`
		OrgID          string `json:"org_id"`
		ImageReference string `json:"image_reference"`
		KeyDER         []byte `json:"key_der"`
	}
	if err := json.Unmarshal(raw, &current); err != nil {
		return DeploymentRegistryVerification{}, err
	}
	if current.AppID != in.AppID || current.AccountID != in.AccountID || current.OrgID != in.OrgID || current.ImageReference != in.ImageReference {
		return DeploymentRegistryVerification{}, ErrApplicationStandardRuntimeStale
	}
	if err := verifyRegistryCurrentKey(in, current.KeyDER); err != nil {
		return DeploymentRegistryVerification{}, err
	}
	existing, err := q.GetDeploymentRegistryVerificationByID(ctx, tx, mustPgUUID(in.ID))
	if err == nil {
		if existing.InputHash != hash {
			return DeploymentRegistryVerification{}, ErrConflict
		}
		value, err := registryVerificationRow(existing)
		if err != nil {
			return DeploymentRegistryVerification{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return DeploymentRegistryVerification{}, err
		}
		return value, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return DeploymentRegistryVerification{}, registryVerificationError(err)
	}
	raw, err = json.Marshal(in)
	if err != nil {
		return DeploymentRegistryVerification{}, err
	}
	if err := q.AuthorizeDeploymentRegistryVerificationInsert(ctx, tx, mustPgUUID(in.ID)); err != nil {
		return DeploymentRegistryVerification{}, err
	}
	row, err := q.InsertDeploymentRegistryVerification(ctx, tx, sqlc.InsertDeploymentRegistryVerificationParams{
		ID: mustPgUUID(in.ID), DeploymentID: mustPgUUID(in.DeploymentID), AppID: mustPgUUID(in.AppID), AccountID: mustPgUUID(in.AccountID),
		WorkloadName: in.WorkloadName, InputSnapshot: raw, InputHash: hash, Payload: in.Proof.Evidence.Payload,
		Signature: in.Proof.Evidence.Signature, TtlSeconds: api.ImageSignatureVerificationTTL.Seconds(),
	})
	if err != nil {
		return DeploymentRegistryVerification{}, registryVerificationError(err)
	}
	value, err := registryVerificationRow(row)
	if err != nil {
		return DeploymentRegistryVerification{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DeploymentRegistryVerification{}, err
	}
	return value, nil
}

func (s *PgStore) GetLatestDeploymentRegistryVerification(ctx context.Context, accountID, appID, depID, workload string) (DeploymentRegistryVerification, error) {
	if !validStandardResourceRead(accountID, appID) || !validStandardResourceRead(depID, depID) {
		return DeploymentRegistryVerification{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetLatestDeploymentRegistryVerification(ctx, s.pool, sqlc.GetLatestDeploymentRegistryVerificationParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(depID), WorkloadName: workload,
	})
	if err != nil {
		return DeploymentRegistryVerification{}, registryVerificationError(err)
	}
	return registryVerificationRow(row)
}
