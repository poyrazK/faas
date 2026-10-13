package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointPgPublicationStore = (*PgStore)(nil)

func checkpointPgPublicationSecret(c ProjectEnvironmentCloneCheckpointPostgresCredentialPlan, in AppSecret) (AppSecret, error) {
	want := ProjectEnvironmentClonePostgresBindingTarget{ID: c.TargetBindingID, AccountID: c.Runtime.Verification.Isolation.AccountID, AppID: c.AppID, Scope: c.Scope, EnvironmentKey: c.EnvironmentKey}
	if validateClonePostgresPreparationSecret(want, in) != nil || in.ManagedPostgresAccess != c.Access || in.SecretClass != SecretClassPersistent || in.SecretVersion != 1 || len(in.Ciphertext) > api.PostgresCheckpointConnectionCiphertextMaxBytes || len(in.ValueHash) != 16 {
		return AppSecret{}, ErrConflict
	}
	if _, err := age.ParseX25519Recipient(in.Kid); err != nil {
		return AppSecret{}, ErrConflict
	}
	prepared, _, err := normalizeClonePostgresPreparation(ProjectEnvironmentClonePostgresBindingPreparation{Secret: in})
	prepared.Secret.ManagedPostgresAccess = c.Access
	return prepared.Secret, err
}
func readCheckpointPgPublicationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) (ProjectEnvironmentCloneCheckpointPgPublication, error) {
	row, err := sqlc.New().ReadCloneCheckpointPgPublication(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return ProjectEnvironmentCloneCheckpointPgPublication{}, mapErr(err)
	}
	in := ProjectEnvironmentCloneCheckpointPgPublication{State: row.State, PlanSHA256: row.PlanSha256}
	if json.Unmarshal(row.Plan, &in.Plan) != nil || in.Plan.OperationID != op.ID || cloneCheckpointSHA(in.Plan) != in.PlanSHA256 {
		return in, ErrConflict
	}
	activation, err := readCheckpointPgActivationTx(ctx, tx, op)
	if err != nil {
		return in, err
	}
	if activation.State != "active" || in.Plan.ActivationPlanSHA256 != cloneCheckpointSHA(activation.Plan) || in.Plan.ActivationObservationSHA256 != activation.ObservationSHA256 || len(in.Plan.Secrets) != len(activation.Plan.Bindings) {
		return in, ErrConflict
	}
	for i, member := range activation.Plan.Bindings {
		secret, err := checkpointPgPublicationSecret(member.Connection.Credential, in.Plan.Secrets[i])
		if err != nil || !reflect.DeepEqual(secret, in.Plan.Secrets[i]) {
			return in, ErrConflict
		}
	}
	return in, nil
}
func checkpointPgPublicationRowsTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, in ProjectEnvironmentCloneCheckpointPgPublication) error {
	activation, err := readCheckpointPgActivationTx(ctx, tx, op)
	if err != nil {
		return err
	}
	q := sqlc.New()
	if _, err = q.LockProjectEnvironmentCloneApps(ctx, tx, sqlc.LockProjectEnvironmentCloneAppsParams{AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID)}); err != nil {
		return mapErr(err)
	}
	for i, member := range activation.Plan.Bindings {
		c := member.Connection.Credential
		s := in.Plan.Secrets[i]
		if err = q.LockProjectEnvironmentCloneSecretTarget(ctx, tx, sqlc.LockProjectEnvironmentCloneSecretTargetParams{AppID: mustPgUUID(c.AppID), Scope: c.Scope, Key: c.EnvironmentKey}); err != nil {
			return mapErr(err)
		}
		b, err := q.LockProjectEnvironmentClonePostgresBinding(ctx, tx, sqlc.LockProjectEnvironmentClonePostgresBindingParams{ID: mustPgUUID(c.TargetBindingID), AccountID: mustPgUUID(op.AccountID)})
		if err != nil {
			return mapErr(err)
		}
		if pgUUIDString(b.DatabaseID) != c.Runtime.Verification.Isolation.OwnerID || pgUUIDString(b.AppID) != c.AppID || b.Scope != c.Scope || b.EnvironmentKey != c.EnvironmentKey || b.Access != c.Access || b.CredentialGeneration != 1 || b.ProviderIdentityID.String != c.ProviderIdentityID || b.CredentialRef.String != s.ManagedCredentialRef || b.LeaseToken.Valid || b.LeaseUntil.Valid || b.RotationPreviousGeneration.Valid || b.RotationWakeID.Valid || b.RotationCleanupReady || b.CutoverID.Valid {
			return ErrConflict
		}
		rows, err := q.ReadProjectEnvironmentClonePostgresBindingSecrets(ctx, tx, mustPgUUID(c.TargetBindingID))
		if err != nil {
			return mapErr(err)
		}
		if in.State == "withdrawn" {
			if b.State != "deleted" || !b.DeletedAt.Valid || len(rows) != 0 {
				return ErrConflict
			}
			continue
		}
		if in.State != "published" || b.State != "ready" || b.DeletedAt.Valid || len(rows) != 1 {
			return ErrConflict
		}
		r := rows[0]
		if r.DeliveryVersion != 1 || r.DeliveredVersion.Valid || r.DeliveryStatus != "pending" {
			return ErrConflict
		}
		actual := AppSecret{AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), Scope: r.Scope, Key: r.Key, Ciphertext: r.Ciphertext, Kid: r.Kid.String, ValueHash: r.ValueHash.String, SecretClass: r.SecretClass, SecretVersion: r.SecretVersion.Int64, ManagedPostgresBindingID: pgUUIDString(r.ManagedPostgresBindingID), ManagedCredentialRef: r.ManagedCredentialRef.String, ManagedCredentialGeneration: r.ManagedCredentialGeneration.Int64, ManagedObjectStorageCredentialID: pgUUIDString(r.ManagedObjectStorageCredentialID), ManagedPostgresAccess: b.Access}
		if !reflect.DeepEqual(actual, s) {
			return ErrConflict
		}
	}
	return nil
}

// Shared activation replay only accepts published metadata owned by this journal.
func checkpointPgPublicationOwnedTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) (bool, error) {
	in, err := readCheckpointPgPublicationTx(ctx, tx, op)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = checkpointPgPublicationRowsTx(ctx, tx, op, in); err != nil {
		return false, err
	}
	return in.State == "published", nil
}
func (s *PgStore) ProjectEnvironmentCloneCheckpointPgPublicationInputs(ctx context.Context, l ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneCheckpointPgPublicationInputs, error) {
	var out ProjectEnvironmentCloneCheckpointPgPublicationInputs
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		var err error
		out.Activation, err = readCheckpointPgActivationTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if out.Activation.State != "active" {
			return ErrConflict
		}
		if err = validateCheckpointPgActivationTx(ctx, tx, op, l, out.Activation, nil); err != nil {
			return err
		}
		publication, err := readCheckpointPgPublicationTx(ctx, tx, op)
		if err == nil {
			if publication.State != "published" {
				return ErrConflict
			}
			out.Publication = &publication
			out.Preparations, err = checkpointPgWorkloadPreparations(op, out.Activation, publication)
			if err != nil {
				return err
			}
			records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
			if err != nil {
				return err
			}
			views, err := cloneBindingViews(records)
			if err != nil {
				return err
			}
			graph, err := checkpointPgBindingGraph(op, views, out.Activation, publication, out.Preparations)
			if err != nil {
				return err
			}
			out.BindingGraph = &graph
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		for _, member := range out.Activation.Plan.Bindings {
			c := member.Connection.Credential
			credential, err := readCheckpointPostgresCredentialTx(ctx, tx, op, c.SourceBindingID)
			if err != nil {
				return err
			}
			row, err := sqlc.New().ReadCloneCheckpointPgConnectionValue(ctx, tx, sqlc.ReadCloneCheckpointPgConnectionValueParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(c.SourceBindingID)})
			if err != nil {
				return mapErr(err)
			}
			connection, err := checkpointPgConnectionFromSQL(row, member.Connection)
			if err != nil {
				return err
			}
			out.Credentials = append(out.Credentials, credential)
			out.Connections = append(out.Connections, connection)
		}
		return nil
	})
	return out, err
}
func (s *PgStore) PublishProjectEnvironmentCloneCheckpointPgBindings(ctx context.Context, l ProjectEnvironmentCloneLease, secrets []AppSecret, observed ProjectEnvironmentCloneCheckpointPgActivationObservation, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointPgPublication, error) {
	var out ProjectEnvironmentCloneCheckpointPgPublication
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		activation, err := readCheckpointPgActivationTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if activation.State != "active" {
			return ErrConflict
		}
		if err = validateCheckpointPgActivationTx(ctx, tx, op, l, activation, nil); err != nil {
			return err
		}
		if err = validateCheckpointPgActivationObservation(activation, observed, l.ExpiresAt); err != nil {
			return err
		}
		release, err := readCloneCheckpointReleaseTx(ctx, tx, op)
		if err != nil {
			return err
		}
		if err = validateCloneCheckpointReleaseObservation(release, retention, l.ExpiresAt); err != nil {
			return err
		}
		out, err = readCheckpointPgPublicationTx(ctx, tx, op)
		if err == nil {
			if out.State != "published" {
				return ErrConflict
			}
			if secrets != nil && !reflect.DeepEqual(out.Plan.Secrets, secrets) {
				return ErrConflict
			}
			return checkpointPgPublicationRowsTx(ctx, tx, op, out)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if len(secrets) != len(activation.Plan.Bindings) {
			return ErrConflict
		}
		q := sqlc.New()
		_, err = q.LockProjectEnvironmentCloneDatabaseAccount(ctx, tx, mustPgUUID(op.AccountID))
		if err != nil {
			return mapErr(err)
		}
		account, err := q.AccountByID(ctx, tx, mustPgUUID(op.AccountID))
		if err != nil {
			return mapErr(err)
		}
		limits, ok := api.LimitsFor(api.Plan(account.Plan))
		if !ok {
			return ErrConflict
		}
		plan := ProjectEnvironmentCloneCheckpointPgPublicationPlan{OperationID: op.ID, ActivationPlanSHA256: cloneCheckpointSHA(activation.Plan), ActivationObservationSHA256: activation.ObservationSHA256}
		// Lock apps in canonical order before taking any secret target locks.
		if _, err = q.LockProjectEnvironmentCloneApps(ctx, tx, sqlc.LockProjectEnvironmentCloneAppsParams{AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID)}); err != nil {
			return mapErr(err)
		}
		for i, member := range activation.Plan.Bindings {
			c := member.Connection.Credential
			secret, err := checkpointPgPublicationSecret(c, secrets[i])
			if err != nil {
				return err
			}
			if err = q.LockProjectEnvironmentCloneSecretTarget(ctx, tx, sqlc.LockProjectEnvironmentCloneSecretTargetParams{AppID: mustPgUUID(c.AppID), Scope: c.Scope, Key: c.EnvironmentKey}); err != nil {
				return mapErr(err)
			}
			exists, err := q.ProjectEnvironmentCloneSecretTargetExists(ctx, tx, sqlc.ProjectEnvironmentCloneSecretTargetExistsParams{AppID: mustPgUUID(c.AppID), Scope: c.Scope, Key: c.EnvironmentKey})
			if err != nil {
				return mapErr(err)
			}
			if exists {
				return ErrConflict
			}
			count, err := q.ObjectS3BindingSecretCount(ctx, tx, sqlc.ObjectS3BindingSecretCountParams{AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(c.AppID)})
			if err != nil {
				return mapErr(err)
			}
			if count >= int64(limits.SecretCountMax) {
				return ErrQuotaExceeded
			}
			if _, err = q.InsertProjectEnvironmentClonePostgresBinding(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresBindingParams{ID: mustPgUUID(c.TargetBindingID), AccountID: mustPgUUID(op.AccountID), DatabaseID: mustPgUUID(c.Runtime.Verification.Isolation.OwnerID), AppID: mustPgUUID(c.AppID), Scope: c.Scope, EnvironmentKey: c.EnvironmentKey, Access: c.Access}); err != nil {
				return mapErr(err)
			}
			if err = q.InsertCloneCheckpointPgAppSecret(ctx, tx, sqlc.InsertCloneCheckpointPgAppSecretParams{AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(c.AppID), Scope: c.Scope, Key: c.EnvironmentKey, Ciphertext: secret.Ciphertext, Kid: pgtype.Text{String: secret.Kid, Valid: true}, ValueHash: pgtype.Text{String: secret.ValueHash, Valid: true}, ManagedPostgresBindingID: mustPgUUID(c.TargetBindingID), ManagedCredentialRef: pgtype.Text{String: secret.ManagedCredentialRef, Valid: true}}); err != nil {
				return mapErr(err)
			}
			n, err := q.FinishProjectEnvironmentClonePostgresBinding(ctx, tx, sqlc.FinishProjectEnvironmentClonePostgresBindingParams{ID: mustPgUUID(c.TargetBindingID), AccountID: mustPgUUID(op.AccountID), ProviderIdentityID: pgtype.Text{String: c.ProviderIdentityID, Valid: true}, CredentialRef: pgtype.Text{String: secret.ManagedCredentialRef, Valid: true}})
			if err != nil {
				return mapErr(err)
			}
			if n != 1 {
				return ErrConflict
			}
			plan.Secrets = append(plan.Secrets, secret)
		}
		raw, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		if len(raw) > api.PostgresCheckpointPublicationMaxBytes {
			return ErrQuotaExceeded
		}
		if _, err = q.InsertCloneCheckpointPgPublication(ctx, tx, sqlc.InsertCloneCheckpointPgPublicationParams{OperationID: mustPgUUID(op.ID), Plan: raw, PlanSha256: cloneCheckpointSHA(plan), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)}); err != nil {
			return mapErr(err)
		}
		out, err = readCheckpointPgPublicationTx(ctx, tx, op)
		if err != nil {
			return err
		}
		return checkpointPgPublicationRowsTx(ctx, tx, op, out)
	})
	return out, err
}
func (s *PgStore) WithdrawProjectEnvironmentCloneCheckpointPgBindings(ctx context.Context, l ProjectEnvironmentCloneLease) error {
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
	in, err := readCheckpointPgPublicationTx(ctx, tx, op)
	if errors.Is(err, ErrNotFound) {
		if err = authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
			return err
		}
		return mapErr(tx.Commit(ctx))
	}
	if err != nil {
		return err
	}
	if err = checkpointPgPublicationRowsTx(ctx, tx, op, in); err != nil {
		return err
	}
	if in.State == "published" {
		q := sqlc.New()
		owner, err := prepareClonePostgresSecretWithdrawalTx(ctx, tx, op)
		if err != nil {
			return err
		}
		for _, secret := range in.Plan.Secrets {
			n, err := q.DeleteCloneCheckpointPgAppSecret(ctx, tx, sqlc.DeleteCloneCheckpointPgAppSecretParams{ManagedPostgresBindingID: mustPgUUID(secret.ManagedPostgresBindingID), AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(secret.AppID), Scope: secret.Scope, Key: secret.Key})
			if err != nil {
				return mapErr(err)
			}
			if n != 1 {
				return ErrConflict
			}
			n, err = q.WithdrawCloneCheckpointPgBinding(ctx, tx, sqlc.WithdrawCloneCheckpointPgBindingParams{ID: mustPgUUID(secret.ManagedPostgresBindingID), AccountID: mustPgUUID(op.AccountID)})
			if err != nil {
				return mapErr(err)
			}
			if n != 1 {
				return ErrConflict
			}
		}
		n, err := q.WithdrawCloneCheckpointPgPublication(ctx, tx, sqlc.WithdrawCloneCheckpointPgPublicationParams{OperationID: mustPgUUID(op.ID), ExpectedRevision: op.Revision, WorkerToken: mustPgUUID(l.Token)})
		if err != nil {
			return mapErr(err)
		}
		if n != 1 {
			return ErrConflict
		}
		in.State = "withdrawn"
		if err = checkpointPgPublicationRowsTx(ctx, tx, op, in); err != nil {
			return err
		}
		if err = saveClonePostgresSecretWithdrawalTx(ctx, tx, op, owner, in.PlanSHA256); err != nil {
			return err
		}
	}
	owned, err := sqlc.New().ReadCloneConfigurationOwnership(ctx, tx, mustPgUUID(op.ID))
	if err == nil {
		if _, err = verifyCloneConfigurationOwnershipTx(ctx, tx, op, pgUUIDString(owned.EnvironmentID)); err != nil {
			return err
		}
	} else if !errors.Is(mapErr(err), ErrNotFound) {
		return mapErr(err)
	}
	if err = authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}
