package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneCheckpointObjectPublicationStore = (*PgStore)(nil)

// This read deliberately does not call inventory/reservation reauthentication:
// those paths use this journal to prove ownership of already published rows.
func readCheckpointObjectPublicationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) (ProjectEnvironmentCloneCheckpointObjectPublication, error) {
	zero := ProjectEnvironmentCloneCheckpointObjectPublication{}
	q := sqlc.New()
	row, err := q.ReadCloneCheckpointObjectPublication(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return zero, mapErr(err)
	}
	var stored ProjectEnvironmentCloneCheckpointObjectPublicationPlan
	if (row.State != "published" && row.State != "withdrawn") || (row.State == "withdrawn") != row.WithdrawnAt.Valid || json.Unmarshal(row.Plan, &stored) != nil {
		return zero, ErrConflict
	}
	stored.SHA256 = row.PlanSha256
	reservationRow, err := q.ReadCloneCheckpointObjectCredentialReservation(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return zero, mapErr(err)
	}
	r, err := checkpointObjectCredentialReservationFromSQL(reservationRow)
	if err != nil {
		return zero, err
	}
	if r.Inputs.AccountID != op.AccountID || r.Inputs.ProjectID != op.ProjectID || r.Inputs.TargetEnvironment != op.TargetEnvironment || r.Inputs.SourceRevisionHash != op.SourceRevisionHash {
		return zero, ErrConflict
	}
	values, err := readCheckpointObjectValuesTx(ctx, tx, op, r)
	if err != nil {
		return zero, err
	}
	expected, err := checkpointObjectPublicationPlan(r, values)
	if err != nil {
		return zero, err
	}
	if !reflect.DeepEqual(stored, expected) {
		return zero, fmt.Errorf("checkpoint object publication plan: %w", ErrConflict)
	}
	return ProjectEnvironmentCloneCheckpointObjectPublication{Plan: expected, State: row.State, WithdrawnAt: optionalObjectS3Time(row.WithdrawnAt)}, nil
}

func checkpointObjectPublicationSecretsTx(ctx context.Context, tx pgx.Tx, p ProjectEnvironmentCloneObjectCredentialPreparation) error {
	rows, err := sqlc.New().ReadCloneCheckpointObjectPublicationSecrets(ctx, tx, mustPgUUID(p.Credential.ID))
	if err != nil {
		return mapErr(err)
	}
	if len(rows) != len(p.Secrets) {
		return fmt.Errorf("checkpoint object publication secret ownership: %w", ErrConflict)
	}
	for i, r := range rows {
		if r.DeliveryVersion != 1 || r.DeliveredVersion.Valid || r.DeliveryStatus != "pending" || !r.OrgMatchesApp {
			return fmt.Errorf("checkpoint object publication secret ownership: %w", ErrConflict)
		}
		actual := AppSecret{AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), Scope: r.Scope, Key: r.Key, Ciphertext: r.Ciphertext, Kid: r.Kid.String, ValueHash: r.ValueHash.String, SecretClass: r.SecretClass, SecretVersion: r.SecretVersion.Int64, ManagedPostgresBindingID: pgUUIDStringNullable(r.ManagedPostgresBindingID), ManagedCredentialRef: r.ManagedCredentialRef.String, ManagedCredentialGeneration: r.ManagedCredentialGeneration.Int64, ManagedObjectStorageCredentialID: pgUUIDStringNullable(r.ManagedObjectStorageCredentialID)}
		if !reflect.DeepEqual(actual, p.Secrets[i]) {
			return fmt.Errorf("checkpoint object publication secret ownership: %w", ErrConflict)
		}
	}
	return nil
}
func checkpointObjectPublicationRowsTx(ctx context.Context, tx pgx.Tx, in ProjectEnvironmentCloneCheckpointObjectPublication) error {
	q := sqlc.New()
	for _, b := range in.Plan.Buckets {
		bucket, err := q.LockCloneCheckpointObjectPublicationBucket(ctx, tx, sqlc.LockCloneCheckpointObjectPublicationBucketParams{AccountID: mustPgUUID(in.Plan.AccountID), AppID: mustPgUUID(b.AppID), ID: mustPgUUID(b.TargetBucketID), EnvironmentCloneOperationID: mustPgUUID(in.Plan.OperationID)})
		if err != nil {
			return mapErr(err)
		}
		owner := ProjectEnvironmentCloneOperation{ID: in.Plan.OperationID, ProjectID: in.Plan.ProjectID, TargetEnvironment: in.Plan.TargetEnvironment}
		if bucket.Scope != in.Plan.TargetEnvironment || bucket.Name != ProjectEnvironmentCloneObjectBucketName(owner, b.AppID, b.SourceBucketID) || bucket.PublicRead || bucket.ServeAt.Valid || (bucket.State != "ready" && (in.State != "withdrawn" || (bucket.State != "deleting" && bucket.State != "deleted"))) {
			return ErrConflict
		}

		rows, err := q.ReadCloneCheckpointObjectPublicationCredentials(ctx, tx, mustPgUUID(b.TargetBucketID))
		if err != nil {
			return mapErr(err)
		}
		if len(rows) != len(b.Preparations) {
			return fmt.Errorf("checkpoint object publication credential/grant ownership: %w", ErrConflict)
		}
		byID := map[string]sqlc.ObjectStorageS3Credential{}
		for _, r := range rows {
			byID[pgUUIDString(r.ID)] = r
		}
		for _, p := range b.Preparations {
			r, ok := byID[p.Credential.ID]
			if !ok || r.LastUsedAt.Valid || r.RotationParentID.Valid || r.RotationWakeID.Valid || r.RotationStampedAt.Valid || len(r.UrlRequest) != 0 || r.UrlApiKeyID.Valid || r.UrlExpiresAt.Valid || r.UrlReceiptID.Valid {
				return fmt.Errorf("checkpoint object publication credential/grant ownership: %w", ErrConflict)
			}
			actual := objectS3CredentialFromSQL(r)
			actual.CreatedAt = time.Time{}
			expected := p.Credential
			if in.State == "withdrawn" {
				if in.WithdrawnAt == nil || !r.RevokedAt.Valid || !r.RevokedAt.Time.Equal(*in.WithdrawnAt) {
					return ErrConflict
				}
				expected.Status = ObjectS3CredentialStatusRevoked
				actual.RevokedAt = nil
			} else if in.State != "published" || r.RevokedAt.Valid {
				return ErrConflict
			}
			if !reflect.DeepEqual(actual, expected) {
				return fmt.Errorf("checkpoint object publication credential/grant ownership: %w", ErrConflict)
			}
			secretOwner := p
			if in.State == "withdrawn" {
				secretOwner.Secrets = nil
			}
			if err = checkpointObjectPublicationSecretsTx(ctx, tx, secretOwner); err != nil {
				return err
			}
		}
		grants, err := q.ReadCloneCheckpointObjectPublicationGrants(ctx, tx, mustPgUUID(b.TargetBucketID))
		if err != nil {
			return mapErr(err)
		}
		if in.State == "withdrawn" {
			if len(grants) != 0 {
				return ErrConflict
			}
			continue
		}
		if len(grants) != len(b.AccessGrants) {
			return fmt.Errorf("checkpoint object publication credential/grant ownership: %w", ErrConflict)
		}
		for i, g := range grants {
			if pgUUIDString(g.AccountID) != in.Plan.AccountID || pgUUIDString(g.ApiKeyID) != b.AccessGrants[i].APIKeyID || g.Permission != b.AccessGrants[i].Permission {
				return fmt.Errorf("checkpoint object publication credential/grant ownership: %w", ErrConflict)
			}
		}
	}
	return nil
}

func checkpointObjectPublicationOwnedTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) (bool, error) {
	in, err := readCheckpointObjectPublicationTx(ctx, tx, op)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err = checkpointObjectPublicationRowsTx(ctx, tx, in); err != nil {
		return false, err
	}
	return in.State == "published", nil
}

func checkpointObjectPublicationAdmissionTx(ctx context.Context, tx pgx.Tx, plan ProjectEnvironmentCloneCheckpointObjectPublicationPlan) error {
	q := sqlc.New()
	if _, err := q.LockProjectEnvironmentCloneDatabaseAccount(ctx, tx, mustPgUUID(plan.AccountID)); err != nil {
		return mapErr(err)
	}
	account, err := q.AccountByID(ctx, tx, mustPgUUID(plan.AccountID))
	if err != nil {
		return mapErr(err)
	}
	limits, ok := api.LimitsFor(api.Plan(account.Plan))
	if !ok {
		return ErrConflict
	}
	apps, err := q.LockProjectEnvironmentCloneApps(ctx, tx, sqlc.LockProjectEnvironmentCloneAppsParams{AccountID: mustPgUUID(plan.AccountID), ProjectID: mustPgUUID(plan.ProjectID)})
	if err != nil {
		return mapErr(err)
	}
	ownedApps := map[string]bool{}
	for _, id := range apps {
		ownedApps[id] = true
	}
	needed := map[string]int{}
	for _, b := range plan.Buckets {
		if !ownedApps[b.AppID] {
			return ErrConflict
		}
		if len(b.Preparations) > api.MaxObjectS3CredentialsPerBucket {
			return ErrQuotaExceeded
		}
		for _, p := range b.Preparations {
			needed[b.AppID] += len(p.Secrets)
			for _, s := range p.Secrets {
				args := sqlc.LockProjectEnvironmentCloneSecretTargetParams{AppID: mustPgUUID(s.AppID), Scope: s.Scope, Key: s.Key}
				if err = q.LockProjectEnvironmentCloneSecretTarget(ctx, tx, args); err != nil {
					return mapErr(err)
				}
				exists, err := q.ProjectEnvironmentCloneSecretTargetExists(ctx, tx, sqlc.ProjectEnvironmentCloneSecretTargetExistsParams(args))
				if err != nil {
					return mapErr(err)
				}
				if exists {
					return ErrConflict
				}
			}
		}
	}
	for _, id := range apps {
		if needed[id] == 0 {
			continue
		}
		count, err := q.ObjectS3BindingSecretCount(ctx, tx, sqlc.ObjectS3BindingSecretCountParams{AccountID: mustPgUUID(plan.AccountID), AppID: mustPgUUID(id)})
		if err != nil {
			return mapErr(err)
		}
		if count+int64(needed[id]) > int64(limits.SecretCountMax) {
			return ErrQuotaExceeded
		}
	}
	if _, err = q.LockCloneCheckpointObjectPublicationKeys(ctx, tx, mustPgUUID(plan.AccountID)); err != nil {
		return mapErr(err)
	}
	return nil
}

func insertCheckpointObjectPublicationRowsTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, l ProjectEnvironmentCloneLease, plan ProjectEnvironmentCloneCheckpointObjectPublicationPlan) error {
	q := sqlc.New()
	for _, b := range plan.Buckets {
		if _, err := q.LockProjectEnvironmentCloneCredentialBucket(ctx, tx, sqlc.LockProjectEnvironmentCloneCredentialBucketParams{AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(b.AppID), ID: mustPgUUID(b.TargetBucketID), EnvironmentCloneOperationID: mustPgUUID(op.ID)}); err != nil {
			return mapErr(err)
		}
		for _, p := range b.Preparations {
			if _, err := insertObjectS3CredentialTx(ctx, tx, p.Credential, api.MaxObjectS3CredentialsPerBucket); err != nil {
				return err
			}
			for _, secret := range p.Secrets {
				if err := q.InsertCloneCheckpointObjectPublicationSecret(ctx, tx, sqlc.InsertCloneCheckpointObjectPublicationSecretParams{AccountID: mustPgUUID(secret.AccountID), AppID: mustPgUUID(secret.AppID), Scope: secret.Scope, Key: secret.Key, Ciphertext: secret.Ciphertext, Kid: secret.Kid, ValueHash: secret.ValueHash, CredentialID: mustPgUUID(p.Credential.ID)}); err != nil {
					return mapErr(err)
				}
			}
		}
		for _, g := range b.AccessGrants {
			n, err := q.InsertCloneCheckpointObjectPublicationGrant(ctx, tx, sqlc.InsertCloneCheckpointObjectPublicationGrantParams{AccountID: mustPgUUID(op.AccountID), BucketID: mustPgUUID(b.TargetBucketID), ApiKeyID: mustPgUUID(g.APIKeyID), Permission: g.Permission, OperationID: mustPgUUID(op.ID), ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)})
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

func (s *PgStore) PublishProjectEnvironmentCloneCheckpointObjects(ctx context.Context, l ProjectEnvironmentCloneLease, planSHA256 string, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectPublication, error) {
	var out ProjectEnvironmentCloneCheckpointObjectPublication
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		r, err := checkpointObjectCredentialReservationTx(ctx, tx, op, l, retention, false)
		if err != nil {
			return err
		}
		values, err := readCheckpointObjectValuesTx(ctx, tx, op, r)
		if err != nil {
			return err
		}
		plan, err := checkpointObjectPublicationPlan(r, values)
		if err != nil {
			return err
		}
		if !validCloneObjectSHA256(planSHA256) || plan.SHA256 != planSHA256 {
			return ErrConflict
		}
		out, err = readCheckpointObjectPublicationTx(ctx, tx, op)
		if err == nil {
			if out.State != "published" {
				return ErrConflict
			}
			return checkpointObjectPublicationRowsTx(ctx, tx, out)
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err = checkpointObjectPublicationAdmissionTx(ctx, tx, plan); err != nil {
			return err
		}
		if err = insertCheckpointObjectPublicationRowsTx(ctx, tx, op, l, plan); err != nil {
			return err
		}
		raw, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		if _, err = sqlc.New().InsertCloneCheckpointObjectPublication(ctx, tx, sqlc.InsertCloneCheckpointObjectPublicationParams{OperationID: mustPgUUID(op.ID), Plan: raw, PlanSha256: plan.SHA256, ExpectedRevision: l.Operation.Revision, WorkerToken: mustPgUUID(l.Token)}); err != nil {
			return mapErr(err)
		}
		out, err = readCheckpointObjectPublicationTx(ctx, tx, op)
		if err != nil {
			return err
		}
		return checkpointObjectPublicationRowsTx(ctx, tx, out)
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectPublication{}, err
	}
	return out, nil
}
