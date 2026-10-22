package state

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func attachBuiltArtifactTx(ctx context.Context, tx pgx.Tx, input Deployment, plan api.Plan) (Deployment, error) {
	frozen := candidateFrozenInputs(input)
	row, err := sqlc.New().GetSourceBuildRootfsByID(ctx, tx, mustPgUUID(frozen.Source.BuildArtifactID))
	if err != nil {
		return input, mapErr(err)
	}
	root, err := sourceBuildRootfsRow(row)
	if err != nil {
		return input, err
	}
	origin, err := deploymentByIDDB(ctx, tx, root.Input.DeploymentID)
	if err != nil {
		return input, err
	}
	selected, err := readSourceRuntimeProducer(ctx, tx, root.Input.AccountID, input.AppID, origin.ID)
	if err != nil || selected.Artifact.ProducerID != root.ID {
		return input, ErrApplicationStandardRuntimeStale
	}
	input, err = attachBuiltArtifact(input, origin, root, plan)
	if err == nil {
		err = requireLayerArtifactsRetainedTx(ctx, tx, []string{root.Input.StorageKey})
	}
	return input, err
}
