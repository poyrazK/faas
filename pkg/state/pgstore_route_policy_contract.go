package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func pgRoutePolicyContract(ctx context.Context, tx pgx.Tx, snapshot *RoutePolicySnapshot, deploymentID string, lock bool) error {
	if deploymentID == "" {
		return nil
	}
	q := &sqlc.Queries{}
	var err error
	if lock {
		_, err = q.LockRoutePolicyDeployment(ctx, tx, sqlc.LockRoutePolicyDeploymentParams{DeploymentID: deploymentID, AppID: snapshot.App.ID})
	} else {
		_, err = q.ReadRoutePolicyDeployment(ctx, tx, sqlc.ReadRoutePolicyDeploymentParams{DeploymentID: deploymentID, AppID: snapshot.App.ID})
	}
	if err != nil {
		return routePolicyReadError(err)
	}
	contract := &RoutePolicyContract{DeploymentID: deploymentID}
	var doc, digest []byte
	var truncated bool
	if lock {
		row, readErr := q.LockRoutePolicyContract(ctx, tx, sqlc.LockRoutePolicyContractParams{DeploymentID: deploymentID, AccountID: snapshot.Account.ID, AppID: snapshot.App.ID})
		doc, digest, truncated, err = row.Doc, row.DocSha256, row.Truncated, readErr
	} else {
		row, readErr := q.ReadRoutePolicyContract(ctx, tx, sqlc.ReadRoutePolicyContractParams{DeploymentID: deploymentID, AccountID: snapshot.Account.ID, AppID: snapshot.App.ID})
		doc, digest, truncated, err = row.Doc, row.DocSha256, row.Truncated, readErr
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	contract.Doc, contract.Truncated = doc, truncated
	if len(digest) > 0 {
		contract.SHA256 = fmt.Sprintf("%x", digest)
	}
	snapshot.Contract = contract
	return nil
}
