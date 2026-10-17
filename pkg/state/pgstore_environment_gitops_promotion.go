package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/promotion"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsPromotionStore = (*PgStore)(nil)

func (s *PgStore) PreviewEnvironmentGitOpsPromotion(ctx context.Context, from, to EnvironmentGitSource, request api.PreviewEnvironmentGitPromotionRequest, desired environmentsync.DesiredState) (api.PreviewEnvironmentGitPromotionResponse, error) {
	var zero api.PreviewEnvironmentGitPromotionResponse
	if err := validateGitPromotion(from, to, request, desired); err != nil {
		return zero, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	from, err = promotionSourceTx(ctx, tx, from)
	if err != nil {
		return zero, err
	}
	to, err = promotionSourceTx(ctx, tx, to)
	if err != nil {
		return zero, err
	}
	if from.ApprovedRevisionID == "" {
		return zero, ErrConflict
	}
	row, err := q.GetEnvironmentDesiredRevision(ctx, tx, sqlc.GetEnvironmentDesiredRevisionParams{SourceID: mustPgUUID(from.ID), RevisionID: mustPgUUID(from.ApprovedRevisionID)})
	if err != nil {
		return zero, mapErr(err)
	}
	input := promotionInput(from, to, environmentRevisionFromSQL(row), request, desired)
	input.Evidence, err = currentEnvironmentGitOpsWorkloadEvidenceTx(ctx, tx, from.AccountID, from.ID)
	if err != nil {
		return zero, err
	}
	rawRelease, err := q.ReadProjectReleaseSet(ctx, tx, sqlc.ReadProjectReleaseSetParams{AccountID: mustPgUUID(from.AccountID), ProjectID: mustPgUUID(from.ProjectID), Environment: from.EnvironmentSlug})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return zero, mapErr(err)
	}
	if err == nil {
		release, err := decodeProjectReleaseSet(rawRelease)
		if err != nil {
			return zero, err
		}
		input.ReleaseID = release.ID
		if err := promotionArtifactsTx(ctx, tx, &input, release); err != nil {
			return zero, err
		}
	}
	out, proposed, err := promotion.Prepare(input)
	if err != nil {
		return zero, ErrInvalidArgument
	}
	observed, _, err := readEnvironmentGitOpsIntent(ctx, tx, to, proposed)
	if err != nil {
		return zero, err
	}
	return finishGitPromotion(input, out, proposed, observed, request.ExpectedPlanHash)
}

func promotionSourceTx(ctx context.Context, tx pgx.Tx, expected EnvironmentGitSource) (EnvironmentGitSource, error) {
	row, err := sqlc.New().GetEnvironmentGitSourceByID(ctx, tx, mustPgUUID(expected.ID))
	if err != nil {
		return EnvironmentGitSource{}, mapErr(err)
	}
	if pgUUIDString(row.AccountID) != expected.AccountID {
		return EnvironmentGitSource{}, ErrNotFound
	}
	scope, err := sqlc.New().GetEnvironmentGitOpsScope(ctx, tx, row.ID)
	if err != nil {
		return EnvironmentGitSource{}, mapErr(err)
	}
	current := environmentGitSourceFromSQL(row, scope.EnvironmentSlug)
	if !promotionSourceMatches(current, expected) {
		return EnvironmentGitSource{}, ErrConflict
	}
	return current, nil
}

func promotionArtifactsTx(ctx context.Context, tx pgx.Tx, input *promotion.Input, release ProjectReleaseSet) error {
	if input.Evidence == nil {
		return nil
	}
	rows, err := sqlc.New().ActiveEnvironmentWorkloadReleaseMembers(ctx, tx, sqlc.ActiveEnvironmentWorkloadReleaseMembersParams{SourceID: mustPgUUID(input.From.ID), EnvironmentID: mustPgUUID(input.From.EnvironmentID)})
	if err != nil {
		return mapErr(err)
	}
	active := map[string]string{}
	for _, row := range rows {
		active[row.AppID] = row.DeploymentID
	}
	for _, workload := range input.Evidence.Workloads {
		if workload.DeploymentID == "" || active[workload.AppID] != workload.DeploymentID || releaseMemberForApp(release, workload.AppID) != workload.DeploymentID {
			continue
		}
		dep, err := sqlc.New().DeploymentByID(ctx, tx, mustPgUUID(workload.DeploymentID))
		if err != nil {
			return mapErr(err)
		}
		buildID, _ := dep.Coalesce.(string)
		artifact := api.EnvironmentGitPromotionArtifact{Resource: workload.Resource, DeploymentID: workload.DeploymentID, BuildID: buildID, Kind: dep.Kind, ImageDigest: dep.ImageDigest}
		if dep.Kind != string(DeploymentKindImage) {
			root, err := readSourceRuntimeRootfs(ctx, tx, input.From.AccountID, workload.AppID, workload.DeploymentID)
			if err == nil {
				if _, _, err = lockSourceRuntimeApproval(ctx, tx, root); err == nil {
					origin, err := deploymentByIDDB(ctx, tx, root.Input.DeploymentID)
					if err != nil {
						return err
					}
					artifact.BuildArtifactID, artifact.BuildArtifactHash, artifact.ArtifactDigest = root.ID, root.InputHash, root.Input.ArtifactDigest
					artifact.BuildID, artifact.BuildCommitSHA = origin.BuildID, origin.CommitSHA
				}
			}
			// A failed SQL fence aborts the transaction; never hide it as a blocker.
			if err != nil {
				return err
			}
		}
		input.Artifacts = append(input.Artifacts, artifact)
	}
	return nil
}
