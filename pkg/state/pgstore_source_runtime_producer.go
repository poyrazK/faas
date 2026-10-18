package state

// adr: 435. Source and registry approvals stay typed and independently fenced.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func readRuntimeProducerSelection(ctx context.Context, tx pgx.Tx, accountID, appID, depID, name string) (runtimeProducerSelection, error) {
	if name == "" {
		present, err := sqlc.New().HasSourceBuildRootfs(ctx, tx, mustPgUUID(depID))
		if err != nil {
			return runtimeProducerSelection{}, err
		}
		if present {
			return readSourceRuntimeProducer(ctx, tx, accountID, appID, depID)
		}
	}
	parent, err := readRuntimeProducer(ctx, tx, accountID, appID, depID, name)
	if err != nil {
		return runtimeProducerSelection{}, err
	}
	return runtimeProducerSelection{runtimeProducerIdentity(parent.Rootfs), runtimeArtifactFromRootfs(parent.Rootfs), registryRuntimeProducerLease(parent)}, nil
}

func readSourceRuntimeProducer(ctx context.Context, tx pgx.Tx, accountID, appID, depID string) (runtimeProducerSelection, error) {
	root, err := readSourceRuntimeRootfs(ctx, tx, accountID, appID, depID)
	if err != nil {
		return runtimeProducerSelection{}, err
	}
	proof, now, err := lockSourceRuntimeApproval(ctx, tx, root)
	if err != nil {
		return runtimeProducerSelection{}, fmt.Errorf("source runtime publisher: %w", err)
	}
	if err := lockSourceBuildRootfsBase(ctx, tx, root.Input, now); err != nil {
		return runtimeProducerSelection{}, fmt.Errorf("source runtime base: %w", err)
	}
	return sourceRuntimeProducerSelection(root, proof), nil
}

func readSourceRuntimeRootfs(ctx context.Context, tx pgx.Tx, accountID, appID, depID string) (SourceBuildRootfs, error) {
	row, err := sqlc.New().GetCurrentSourceBuildRootfs(ctx, tx, sourceBuildRootfsScope(accountID, appID, depID))
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceBuildRootfs{}, ErrApplicationStandardRuntimeStale
	}
	if err != nil {
		return SourceBuildRootfs{}, buildExportError(err)
	}
	root, err := sourceBuildRootfsRow(row)
	if err != nil {
		return SourceBuildRootfs{}, fmt.Errorf("source runtime rootfs: %w", err)
	}
	if err := lockSourceRuntimeOwner(ctx, tx, root); err != nil {
		return SourceBuildRootfs{}, fmt.Errorf("source runtime owner and intent: %w", err)
	}
	return root, nil
}

func readRuntimeProducerOwner(ctx context.Context, tx pgx.Tx, accountID, appID, depID string) (sqlc.GetDeploymentArtifactWorkloadsRow, error) {
	q := sqlc.New()
	scope := sqlc.GetDeploymentArtifactWorkloadsParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(depID)}
	owner, err := q.GetDeploymentArtifactWorkloads(ctx, tx, scope)
	if err != nil {
		return owner, registryVerificationError(err)
	}
	if !owner.HasRegistryProducers {
		return owner, ErrDeploymentArtifactScanEvidenceAbsent
	}
	present, err := q.HasSourceBuildRootfs(ctx, tx, scope.DeploymentID)
	if err != nil {
		return owner, err
	}
	if !present {
		return readArtifactEvidenceOwner(ctx, tx, accountID, appID, depID)
	}
	if _, err := readSourceRuntimeRootfs(ctx, tx, accountID, appID, depID); err != nil {
		return owner, err
	}
	// Read workload membership after the source owner/deployment fences, so a
	// concurrent sidecar update cannot disappear from the composed scan input.
	return q.GetDeploymentArtifactWorkloads(ctx, tx, scope)
}

func lockSourceRuntimeOwner(ctx context.Context, tx pgx.Tx, root SourceBuildRootfs) error {
	raw, err := json.Marshal(root.Input)
	if err != nil {
		return err
	}
	raw, err = sqlc.New().LockSourceBuildRuntimeRootfs(ctx, tx, sqlc.LockSourceBuildRuntimeRootfsParams{Input: raw, ID: mustPgUUID(root.ID)})
	if err != nil {
		return buildExportError(err)
	}
	var current struct {
		Intent sourceBuildRootfsIntent `json:"intent"`
	}
	if err := json.Unmarshal(raw, &current); err != nil {
		return err
	}
	return checkSourceRuntimeIntent(root.Input, current.Intent)
}

func lockSourceRuntimeApproval(ctx context.Context, tx pgx.Tx, root SourceBuildRootfs) (BuildExportPublication, time.Time, error) {
	q := sqlc.New()
	row, err := q.GetBuildExportPublicationByID(ctx, tx, mustPgUUID(root.Input.PublicationID))
	if err != nil {
		return BuildExportPublication{}, time.Time{}, buildExportError(err)
	}
	origin, err := buildExportPublicationRow(row)
	if err != nil {
		return BuildExportPublication{}, time.Time{}, err
	}
	in := root.Input
	row, err = q.GetLatestScopedBuildExportPublication(ctx, tx, sqlc.GetLatestScopedBuildExportPublicationParams{
		AccountID: mustPgUUID(in.AccountID), AppID: mustPgUUID(in.AppID), DeploymentID: mustPgUUID(in.DeploymentID), BuildID: mustPgUUID(origin.Input.Claims.BuildID)})
	if err != nil {
		return BuildExportPublication{}, time.Time{}, buildExportError(err)
	}
	proof, err := buildExportPublicationRow(row)
	if err != nil {
		return proof, time.Time{}, err
	}
	now, err := lockBuildExportPublication(ctx, tx, proof.Input, true)
	if err == nil {
		err = checkSourceRuntimeApproval(root, origin, proof, now)
	}
	return proof, now, err
}
