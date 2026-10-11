package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// The publication transaction is the final qualification fence. Previewing a
// valid receipt does not authorize publishing after the source has changed.
func verifyProjectEnvironmentPromotionQualificationTx(ctx context.Context, tx pgx.Tx, promotion ProjectEnvironmentPromotion) error {
	if promotion.SourceQualificationID == "" {
		return nil
	}
	var found int
	// Receipt creation holds SHARE on this row. UPDATE prevents a newer
	// failed receipt from being inserted between this check and cutover.
	if err := tx.QueryRow(ctx, `select 1 from project_environments
		where account_id = $1 and project_id = $2 and slug = $3 for update`,
		promotion.AccountID, promotion.ProjectID, promotion.FromEnvironment).Scan(&found); err != nil {
		return mapErr(err)
	}
	qualification, err := scanProjectEnvironmentQualification(tx.QueryRow(ctx, `
		select id, account_id, project_id, environment_slug, release_set_id,
		configuration_version, configuration_hash, secret_revision_hashes, workload_config_hashes,
		status, checks, created_at, expires_at from project_environment_qualifications
		where account_id = $1 and project_id = $2 and environment_slug = $3 and release_set_id = $4
		order by created_at desc, id desc limit 1`, promotion.AccountID, promotion.ProjectID, promotion.FromEnvironment, promotion.SourceReleaseSetID))
	if err != nil {
		return err
	}
	if qualification.ID != promotion.SourceQualificationID || qualification.Status != "passed" || !time.Now().Before(qualification.ExpiresAt) {
		return ErrConflict
	}
	workloadHashes, scoped, err := projectEnvironmentWorkloadConfigHashesTx(ctx, tx, promotion.AccountID, promotion.ProjectID, promotion.FromEnvironment, promotion.SourceReleaseSetID)
	if err != nil {
		return err
	}
	if err := validateQualificationWorkloadHashes(qualification.WorkloadConfigHashes, workloadHashes, scoped); err != nil {
		return err
	}
	config, err := projectEnvironmentConfigLatestTx(ctx, tx, promotion.AccountID, promotion.ProjectID, promotion.FromEnvironment)
	if err != nil {
		return err
	}
	if config.Version != qualification.ConfigurationVersion || config.ConfigHash != qualification.ConfigurationHash {
		return ErrConflict
	}
	secretHashes, err := projectEnvironmentSecretRevisionHashesTx(ctx, tx, promotion.AccountID, promotion.SourceReleaseSetID, promotion.FromEnvironment)
	if err != nil {
		return err
	}
	if !sameProjectEnvironmentSecretRevisionHashes(secretHashes, qualification.SecretRevisionHashes) {
		return ErrConflict
	}
	return nil
}

func (m *MemStore) verifyProjectEnvironmentPromotionQualificationLocked(promotion ProjectEnvironmentPromotion) error {
	if promotion.SourceQualificationID == "" {
		return nil
	}
	records := m.projectEnvironmentQualifications[promotion.SourceReleaseSetID]
	if len(records) == 0 {
		return ErrConflict
	}
	qualification := records[len(records)-1]
	if qualification.ID != promotion.SourceQualificationID || qualification.Status != "passed" ||
		qualification.AccountID != promotion.AccountID || qualification.ProjectID != promotion.ProjectID ||
		qualification.EnvironmentSlug != promotion.FromEnvironment || !time.Now().Before(qualification.ExpiresAt) {
		return ErrConflict
	}
	release, ok := m.projectReleaseSets[promotion.SourceReleaseSetID]
	if !ok || !release.Active || m.activeProjectReleaseSets[releaseKey(promotion.ProjectID, promotion.FromEnvironment)] != release.ID {
		return ErrConflict
	}
	hashes, scoped, err := m.projectEnvironmentWorkloadConfigHashesLocked(promotion.AccountID, promotion.ProjectID, promotion.FromEnvironment, release.Members)
	if err != nil {
		return err
	}
	if err := validateQualificationWorkloadHashes(qualification.WorkloadConfigHashes, hashes, scoped); err != nil {
		return err
	}
	config := m.projectEnvironmentConfigLatestLocked(promotion.ProjectID, promotion.FromEnvironment)
	if config.Version != qualification.ConfigurationVersion || config.ConfigHash != qualification.ConfigurationHash {
		return ErrConflict
	}
	secretHashes, err := m.projectEnvironmentSecretRevisionHashesLocked(promotion.AccountID, promotion.FromEnvironment, release.Members)
	if err != nil {
		return err
	}
	if !sameProjectEnvironmentSecretRevisionHashes(secretHashes, qualification.SecretRevisionHashes) {
		return ErrConflict
	}
	return nil
}
