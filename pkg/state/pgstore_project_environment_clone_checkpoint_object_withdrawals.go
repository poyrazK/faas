package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Remove only authenticated local gateway rights. This is not evidence of native
// target deletion, provider-grant revocation or released checkpoint retention.
func (s *PgStore) WithdrawProjectEnvironmentCloneCheckpointObjects(ctx context.Context, l ProjectEnvironmentCloneLease) error {
	if !validCloneLeaseIdentity(l) {
		return ErrInvalidArgument
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err = sqlc.New().LockCloneCheckpointUnusedReservationClock(ctx, tx); err != nil {
		return mapErr(err)
	}
	op, err := lockCloneWorkloadOperationTx(ctx, tx, l.Operation.AccountID, l.Operation.ProjectID, l.Operation.ID)
	if err != nil {
		return err
	}
	if op.Status != CloneOperationCompensating {
		return ErrConflict
	}
	if err = authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
		return err
	}
	in, err := readCheckpointObjectPublicationTx(ctx, tx, op)
	if !errors.Is(err, ErrNotFound) {
		if err != nil {
			return err
		}
		if _, err = sqlc.New().LockProjectEnvironmentCloneApps(ctx, tx, sqlc.LockProjectEnvironmentCloneAppsParams{AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID)}); err != nil {
			return mapErr(err)
		}
		if err = checkpointObjectPublicationRowsTx(ctx, tx, in); err != nil {
			return err
		}
		if in.State == "published" {
			owner, err := prepareCloneObjectSecretWithdrawalTx(ctx, tx, op)
			if err != nil {
				return err
			}
			if err = withdrawCheckpointObjectPublicationRowsTx(ctx, tx, in); err != nil {
				return err
			}
			n, err := sqlc.New().WithdrawCloneCheckpointObjectPublication(ctx, tx, sqlc.WithdrawCloneCheckpointObjectPublicationParams{OperationID: mustPgUUID(op.ID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
			if err != nil {
				return mapErr(err)
			}
			if n != 1 {
				return ErrConflict
			}
			in, err = readCheckpointObjectPublicationTx(ctx, tx, op)
			if err != nil {
				return err
			}
			if err = checkpointObjectPublicationRowsTx(ctx, tx, in); err != nil {
				return err
			}
			if err = saveCloneObjectSecretWithdrawalTx(ctx, tx, op, owner, in.Plan.SHA256); err != nil {
				return err
			}
		}
		owner, err := sqlc.New().ReadCloneConfigurationOwnership(ctx, tx, mustPgUUID(op.ID))
		if err == nil {
			if _, err = verifyCloneConfigurationOwnershipTx(ctx, tx, op, pgUUIDString(owner.EnvironmentID)); err != nil {
				return err
			}
		} else if !errors.Is(mapErr(err), ErrNotFound) {
			return mapErr(err)
		}
	}
	if err = authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}

func withdrawCheckpointObjectPublicationRowsTx(ctx context.Context, tx pgx.Tx, in ProjectEnvironmentCloneCheckpointObjectPublication) error {
	q := sqlc.New()
	for _, b := range in.Plan.Buckets {
		for _, p := range b.Preparations {
			for _, secret := range p.Secrets {
				if err := q.LockProjectEnvironmentCloneSecretTarget(ctx, tx, sqlc.LockProjectEnvironmentCloneSecretTargetParams{AppID: mustPgUUID(secret.AppID), Scope: secret.Scope, Key: secret.Key}); err != nil {
					return mapErr(err)
				}
				n, err := q.DeleteCloneCheckpointObjectPublicationSecret(ctx, tx, sqlc.DeleteCloneCheckpointObjectPublicationSecretParams{CredentialID: mustPgUUID(p.Credential.ID), AccountID: mustPgUUID(in.Plan.AccountID), AppID: mustPgUUID(secret.AppID), Scope: secret.Scope, Key: secret.Key})
				if err != nil {
					return mapErr(err)
				}
				if n != 1 {
					return ErrConflict
				}
			}
			n, err := q.RevokeCloneCheckpointObjectPublicationCredential(ctx, tx, sqlc.RevokeCloneCheckpointObjectPublicationCredentialParams{CredentialID: mustPgUUID(p.Credential.ID), AccountID: mustPgUUID(in.Plan.AccountID), BucketID: mustPgUUID(b.TargetBucketID)})
			if err != nil {
				return mapErr(err)
			}
			if n != 1 {
				return ErrConflict
			}
		}
		for _, g := range b.AccessGrants {
			n, err := q.DeleteCloneCheckpointObjectPublicationGrant(ctx, tx, sqlc.DeleteCloneCheckpointObjectPublicationGrantParams{AccountID: mustPgUUID(in.Plan.AccountID), BucketID: mustPgUUID(b.TargetBucketID), ApiKeyID: mustPgUUID(g.APIKeyID), Permission: g.Permission})
			if err != nil {
				return mapErr(err)
			}
			if n != 1 {
				return ErrConflict
			}
		}
	}
	return nil
}
