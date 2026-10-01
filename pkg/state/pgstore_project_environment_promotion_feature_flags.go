package state

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func capturePromotionFeatureFlagsTx(ctx context.Context, tx pgx.Tx, promotion ProjectEnvironmentPromotion) error {
	// The caller already holds project -> promotion -> both environments in
	// UUID order. Flag publishers take project -> environment locks as well.
	source, err := readCloneFeatureFlagsTx(ctx, tx, promotion.AccountID, promotion.ProjectID, promotion.FromEnvironment)
	if err != nil {
		return err
	}
	target, err := readCloneFeatureFlagsTx(ctx, tx, promotion.AccountID, promotion.ProjectID, promotion.ToEnvironment)
	if err != nil {
		return err
	}
	captured, err := capturePromotionFeatureFlags(promotion, source, target)
	if err != nil {
		return err
	}
	sourceRaw, err := json.Marshal(captured.Source)
	if err != nil {
		return err
	}
	targetRaw, err := json.Marshal(captured.PreviousTarget)
	if err != nil {
		return err
	}
	return mapErr(sqlc.New().InsertPromotionFeatureFlags(ctx, tx, sqlc.InsertPromotionFeatureFlagsParams{
		PromotionID: mustPgUUID(promotion.ID), SourceSnapshot: sourceRaw, PreviousTargetSnapshot: targetRaw,
		SourceHash: captured.SourceHash, PreviousTargetHash: captured.PreviousTargetHash,
	}))
}

func readPromotionFeatureFlags(ctx context.Context, db sqlc.DBTX, accountID, promotionID string) (promotionFeatureFlags, error) {
	account, err := parsePgUUID(accountID)
	if err != nil {
		return promotionFeatureFlags{}, err
	}
	id, err := parsePgUUID(promotionID)
	if err != nil {
		return promotionFeatureFlags{}, err
	}
	row, err := sqlc.New().ReadPromotionFeatureFlags(ctx, db, sqlc.ReadPromotionFeatureFlagsParams{PromotionID: id, AccountID: account})
	if err != nil {
		return promotionFeatureFlags{}, mapErr(err)
	}
	captured := promotionFeatureFlags{ProjectEnvironmentPromotionFeatureFlags: ProjectEnvironmentPromotionFeatureFlags{
		SourceHash: row.SourceHash, PreviousTargetHash: row.PreviousTargetHash, TargetVersion: row.TargetVersion, RollbackVersion: row.RollbackVersion}}
	if json.Unmarshal(row.SourceSnapshot, &captured.Source) != nil || json.Unmarshal(row.PreviousTargetSnapshot, &captured.PreviousTarget) != nil {
		return promotionFeatureFlags{}, ErrConflict
	}
	if err := captured.authenticate(); err != nil {
		return promotionFeatureFlags{}, err
	}
	return captured, nil
}

func (s *PgStore) ProjectEnvironmentPromotionFeatureFlags(ctx context.Context, accountID, promotionID string) (ProjectEnvironmentPromotionFeatureFlags, error) {
	captured, err := readPromotionFeatureFlags(ctx, s.pool, accountID, promotionID)
	return captured.ProjectEnvironmentPromotionFeatureFlags, err
}

func promotionFeatureFlagsActivationTx(ctx context.Context, tx pgx.Tx, promotion ProjectEnvironmentPromotion, rollback, completed bool) error {
	if !promotion.SyncConfig {
		return nil
	}
	captured, err := readPromotionFeatureFlags(ctx, tx, promotion.AccountID, promotion.ID)
	if err != nil {
		if err == ErrNotFound {
			return ErrConflict // a legacy operation has no frozen flag evidence
		}
		return err
	}
	target, err := readCloneFeatureFlagsTx(ctx, tx, promotion.AccountID, promotion.ProjectID, promotion.ToEnvironment)
	if err != nil {
		return err
	}
	var source FeatureFlagVersion
	if !rollback && !completed {
		source, err = readCloneFeatureFlagsTx(ctx, tx, promotion.AccountID, promotion.ProjectID, promotion.FromEnvironment)
		if err != nil {
			return err
		}
	}
	next, err := preparePromotionFeatureFlagActivation(captured, source, target, rollback, completed)
	if err != nil || completed {
		return err
	}
	p, err := flagPGScope(FeatureFlagScope{AccountID: promotion.AccountID, ProjectID: promotion.ProjectID, EnvironmentID: next.EnvironmentID})
	if err != nil {
		return err
	}
	q := sqlc.New()
	for _, id := range flagCustomerIDs(next.Config) {
		if _, err := q.LockPromotionFeatureFlagCustomer(ctx, tx, sqlc.LockPromotionFeatureFlagCustomerParams{AccountID: p.AccountID, TenantID: mustPgUUID(id)}); err != nil {
			return mapErr(err)
		}
	}
	raw, err := json.Marshal(next.Config)
	if err != nil {
		return err
	}
	if _, err := q.InsertFeatureFlagVersion(ctx, tx, sqlc.InsertFeatureFlagVersionParams{
		AccountID: p.AccountID, ProjectID: p.ProjectID, EnvironmentID: p.EnvironmentID, Version: next.Version, Config: raw,
		Actor: next.Actor, RestoredFrom: pgtype.Int8{Int64: next.RestoredFrom, Valid: next.RestoredFrom > 0},
	}); err != nil {
		return mapErr(err)
	}
	param := sqlc.UpdatePromotionFeatureFlagReceiptParams{PromotionID: mustPgUUID(promotion.ID),
		TargetVersion: captured.TargetVersion, RollbackVersion: captured.RollbackVersion,
		PreviousTargetVersion: captured.TargetVersion, PreviousRollbackVersion: captured.RollbackVersion}
	if rollback {
		param.RollbackVersion = next.Version
	} else {
		param.TargetVersion = next.Version
	}
	_, err = q.UpdatePromotionFeatureFlagReceipt(ctx, tx, param)
	return mapErr(err)
}
