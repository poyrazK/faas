package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/rollback"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsRollbackStore = (*PgStore)(nil)

func (s *PgStore) PreviewEnvironmentGitOpsRollback(ctx context.Context, source EnvironmentGitSource, request api.PreviewEnvironmentGitRollbackRequest, desired environmentsync.DesiredState) (api.PreviewEnvironmentGitRollbackResponse, error) {
	var zero api.PreviewEnvironmentGitRollbackResponse
	if err := validateGitRollback(source, request, desired); err != nil {
		return zero, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	source, err = promotionSourceTx(ctx, tx, source)
	if err != nil {
		return zero, err
	}
	return s.previewEnvironmentGitOpsRollbackTx(ctx, tx, source, request, desired, time.Now().UTC())
}

func (s *PgStore) previewEnvironmentGitOpsRollbackTx(ctx context.Context, tx pgx.Tx, source EnvironmentGitSource, request api.PreviewEnvironmentGitRollbackRequest, desired environmentsync.DesiredState, now time.Time) (api.PreviewEnvironmentGitRollbackResponse, error) {
	var zero api.PreviewEnvironmentGitRollbackResponse
	q := sqlc.New()
	raw, err := q.ReadProjectReleaseSet(ctx, tx, sqlc.ReadProjectReleaseSetParams{AccountID: mustPgUUID(source.AccountID), ProjectID: mustPgUUID(source.ProjectID), Environment: source.EnvironmentSlug, ReleaseID: mustPgUUID(request.ReleaseSetID)})
	if err != nil {
		return zero, mapErr(err)
	}
	release, err := decodeProjectReleaseSet(raw)
	if err != nil {
		return zero, err
	}
	rows, err := q.EnvironmentWorkloadGraphsForRollback(ctx, tx, sqlc.EnvironmentWorkloadGraphsForRollbackParams{SourceID: mustPgUUID(source.ID), EnvironmentID: mustPgUUID(source.EnvironmentID), ReleaseID: mustPgUUID(release.ID)})
	if err != nil {
		return zero, mapErr(err)
	}
	if len(rows) != 1 {
		return zero, ErrEnvironmentGitOpsRollbackReleaseUnproven
	}
	graph := workloadGraphFromSQL(rows[0])
	revisionRow, err := q.GetEnvironmentDesiredRevision(ctx, tx, sqlc.GetEnvironmentDesiredRevisionParams{SourceID: mustPgUUID(source.ID), RevisionID: mustPgUUID(graph.RevisionID)})
	if err != nil {
		return zero, mapErr(err)
	}
	receipt, _, err := loadEnvironmentWorkloadServingReceipt(ctx, tx, rows[0].ID)
	if err != nil {
		return zero, err
	}
	input, err := retainedRollbackInput(source, request, desired, release, graph, environmentRevisionFromSQL(revisionRow), receipt)
	if err != nil {
		return zero, err
	}
	input.Now = now
	active, err := q.ReadProjectReleaseSet(ctx, tx, sqlc.ReadProjectReleaseSetParams{AccountID: mustPgUUID(source.AccountID), ProjectID: mustPgUUID(source.ProjectID), Environment: source.EnvironmentSlug})
	if err == nil {
		r, e := decodeProjectReleaseSet(active)
		if e != nil {
			return zero, e
		}
		input.CurrentReleaseSetID = r.ID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return zero, mapErr(err)
	}
	observed, _, err := readEnvironmentGitOpsIntent(ctx, tx, source, desired)
	if err != nil {
		return zero, err
	}
	for _, member := range graph.Members {
		dep, err := deploymentByIDDB(ctx, tx, releaseMemberForApp(release, member.AppID))
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return zero, err
		}
		if !rollbackMemberCompatible(&input, member, dep, observed) {
			continue
		}
		artifact := api.EnvironmentGitPromotionArtifact{Resource: member.Resource, DeploymentID: dep.ID, BuildID: dep.BuildID, Kind: string(dep.Kind), ImageDigest: dep.ImageDigest}
		if dep.Kind != DeploymentKindImage {
			selected, err := readSourceRuntimeProducer(ctx, tx, source.AccountID, member.AppID, dep.ID)
			if err != nil && !errors.Is(err, ErrApplicationStandardRuntimeStale) && !errors.Is(err, ErrNotFound) {
				return zero, err
			}
			if err == nil {
				root, err := readSourceRuntimeRootfs(ctx, tx, source.AccountID, member.AppID, dep.ID)
				if err != nil {
					return zero, err
				}
				if graph.PreparedAt == nil || root.PublishedAt.After(*graph.PreparedAt) {
					input.BlockingReasons = append(input.BlockingReasons, member.Resource+": rollback_artifact_replaced")
					continue
				}
				if root.ID != selected.Artifact.ProducerID {
					return zero, ErrConflict
				}
				origin, err := deploymentByIDDB(ctx, tx, root.Input.DeploymentID)
				if err != nil {
					return zero, err
				}
				artifact.BuildArtifactID, artifact.BuildArtifactHash, artifact.ArtifactDigest = root.ID, root.InputHash, root.Input.ArtifactDigest
				artifact.BuildID, artifact.BuildCommitSHA = origin.BuildID, origin.CommitSHA
			}
		}
		input.Artifacts = append(input.Artifacts, artifact)
	}
	_, proposed, err := rollback.Prepare(input)
	if err != nil {
		return zero, ErrInvalidArgument
	}
	observed, _, err = readEnvironmentGitOpsIntent(ctx, tx, source, proposed)
	if err != nil {
		return zero, err
	}
	return finishGitRollback(input, observed, request.ExpectedPlanHash)
}
