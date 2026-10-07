package state

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func readInvocationWorkEnvironmentAdmissionDB(ctx context.Context, db sqlc.DBTX, id string) (InvocationWorkEnvironmentAdmission, error) {
	if parsed, err := uuid.Parse(id); err != nil || parsed == uuid.Nil {
		return InvocationWorkEnvironmentAdmission{}, ErrInvalidArgument
	}
	row, err := sqlc.New().ReadInvocationWorkEnvironmentAdmission(ctx, db, mustPgUUID(id))
	if err != nil {
		return InvocationWorkEnvironmentAdmission{}, mapErr(err)
	}
	return InvocationWorkEnvironmentAdmission{InvocationID: pgUUIDString(row.InvocationID), EnvironmentID: pgUUIDString(row.EnvironmentID),
		WorkloadSpecID: pgUUIDString(row.WorkloadSpecID), SettingsHash: row.SettingsHash, AppID: pgUUIDString(row.AppID),
		PolicyName: row.PolicyName, PolicyRevision: row.PolicyRevision, KeyDigest: row.KeyDigest, FairnessDigest: row.FairnessDigest,
		FairnessLimit: int(row.FairnessLimit)}, nil
}

func (s *PgStore) InvocationWorkEnvironmentAdmission(ctx context.Context, id string) (InvocationWorkEnvironmentAdmission, error) {
	return readInvocationWorkEnvironmentAdmissionDB(ctx, s.pool, id)
}

func registerWorkEnvironmentDomainDB(ctx context.Context, db sqlc.DBTX, app App, environmentID, policyName, kind string, digest []byte) error {
	owner, err := sqlc.New().RegisterInvocationWorkEnvironmentDomain(ctx, db, sqlc.RegisterInvocationWorkEnvironmentDomainParams{
		AppID: mustPgUUID(app.ID), AccountID: mustPgUUID(app.AccountID), EnvironmentID: mustPgUUID(environmentID),
		PolicyName: policyName, Kind: kind, Digest: digest})
	if err != nil || pgUUIDString(owner) != environmentID {
		if errors.Is(err, pgx.ErrNoRows) || err == nil {
			return ErrInvocationEnvironmentWorkIsolation
		}
		return err
	}
	return nil
}

func registerInvocationWorkEnvironmentDB(ctx context.Context, db sqlc.DBTX, info invocationWorkEnvironment, inv Invocation) error {
	if info.environment.ID == "" {
		return nil
	}
	if err := registerWorkEnvironmentDomainDB(ctx, db, info.app, info.environment.ID, inv.WorkPolicyName, "key", inv.WorkKeyDigest); err != nil {
		return err
	}
	if len(inv.WorkFairnessDigest) != 0 {
		return registerWorkEnvironmentDomainDB(ctx, db, info.app, info.environment.ID, inv.WorkPolicyName, "fairness", inv.WorkFairnessDigest)
	}
	return nil
}

func insertInvocationWorkEnvironmentDB(ctx context.Context, db sqlc.DBTX, info invocationWorkEnvironment, inv Invocation) error {
	if info.environment.ID == "" {
		return nil
	}
	count, err := sqlc.New().CreateInvocationWorkEnvironmentAdmission(ctx, db, sqlc.CreateInvocationWorkEnvironmentAdmissionParams{
		AdmittedAt:   pgtype.Timestamptz{Time: inv.CreatedAt, Valid: true},
		InvocationID: mustPgUUID(inv.ID), AppID: mustPgUUID(info.app.ID), AccountID: mustPgUUID(info.app.AccountID),
		EnvironmentID: mustPgUUID(info.environment.ID), WorkloadSpecID: mustPgUUID(info.spec.ID), SettingsHash: info.spec.Hash,
		DeploymentID: mustPgUUID(info.deployment), PolicyName: inv.WorkPolicyName, PolicyRevision: inv.WorkPolicyRevision,
		KeyDigest: inv.WorkKeyDigest, FairnessDigest: inv.WorkFairnessDigest, FairnessLimit: int32(inv.WorkFairnessLimit)})
	if err != nil {
		return fmt.Errorf("state: persist environment work admission: %w", err)
	}
	if count != 1 {
		return ErrInvocationEnvironmentWorkIsolation
	}
	return nil
}

func validateWorkEnvironmentReplayDB(ctx context.Context, db sqlc.DBTX, info invocationWorkEnvironment, existing Invocation) error {
	owner, err := readInvocationWorkEnvironmentAdmissionDB(ctx, db, existing.ID)
	if info.environment.ID == "" {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return ErrInvocationEnvironmentWorkIsolation
	}
	if err != nil || owner.EnvironmentID != info.environment.ID || !admissionMatchesInvocation(owner, existing) {
		return ErrInvocationEnvironmentWorkIsolation
	}
	return nil
}

// A known stage domain cannot downgrade to a legacy claim if its proof is lost.
func validateWorkEnvironmentClaimDB(ctx context.Context, db sqlc.DBTX, id, appID, policyName string, digest []byte) error {
	proof, proofErr := readInvocationWorkEnvironmentAdmissionDB(ctx, db, id)
	if proofErr != nil && !errors.Is(proofErr, ErrNotFound) {
		return proofErr
	}
	validation, err := sqlc.New().ValidateInvocationWorkEnvironmentClaim(ctx, db, mustPgUUID(id))
	if err != nil {
		return mapErr(err)
	}
	ownerID, err := sqlc.New().ReadInvocationWorkEnvironmentDomain(ctx, db, sqlc.ReadInvocationWorkEnvironmentDomainParams{
		AppID: mustPgUUID(appID), PolicyName: policyName, Kind: "key", Digest: digest})
	if errors.Is(err, pgx.ErrNoRows) {
		if proof.InvocationID != "" || validation.StagePin.Bool {
			return ErrInvocationEnvironmentWorkIsolation
		}
		return nil
	}
	if err != nil {
		return err
	}
	if proofErr != nil || !validation.AdmissionValid || proof.EnvironmentID != pgUUIDString(ownerID) ||
		proof.AppID != appID || proof.PolicyName != policyName || !bytes.Equal(proof.KeyDigest, digest) {
		return ErrInvocationEnvironmentWorkIsolation
	}
	return nil
}
