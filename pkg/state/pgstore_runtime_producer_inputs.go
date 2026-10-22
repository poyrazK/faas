package state

// adr: 435. Existing owner/control/artifact/base fences protect scanner bootstrap.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ DeploymentRuntimeProducerPresenceStore = (*PgStore)(nil)

var _ DeploymentRuntimeProducerInputStore = (*PgStore)(nil)

func (s *PgStore) GetFreshDeploymentRuntimeProducerInputs(ctx context.Context, accountID, appID, depID string) (DeploymentRuntimeProducerInputs, error) {
	if !validArtifactScanEvidenceRead(accountID, appID, depID, "") {
		return DeploymentRuntimeProducerInputs{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DeploymentRuntimeProducerInputs{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	inputs, err := readRuntimeProducerInputsTx(ctx, tx, accountID, appID, depID)
	if err != nil {
		return DeploymentRuntimeProducerInputs{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DeploymentRuntimeProducerInputs{}, err
	}
	return inputs, nil
}

func readRuntimeProducerInputsTx(ctx context.Context, tx pgx.Tx, accountID, appID, depID string) (DeploymentRuntimeProducerInputs, error) {
	owner, err := readRuntimeProducerOwner(ctx, tx, accountID, appID, depID)
	if err != nil {
		return DeploymentRuntimeProducerInputs{}, err
	}
	identity, parents, err := readRuntimeProducerSet(ctx, tx, accountID, appID, depID, owner.Sidecars)
	if err != nil {
		return DeploymentRuntimeProducerInputs{}, err
	}
	now, err := sqlc.New().ArtifactEvidenceStorageTime(ctx, tx)
	if err != nil || !now.Valid {
		return DeploymentRuntimeProducerInputs{}, errors.Join(ErrApplicationStandardRuntimeStale, err)
	}
	return finishRuntimeProducerInputs(identity, parents, now.Time)
}

func readRuntimeProducer(ctx context.Context, tx pgx.Tx, accountID, appID, depID, name string) (artifactScanParents, error) {
	q := sqlc.New()
	row, err := q.GetCurrentDeploymentRegistryRootfs(ctx, tx, sqlc.GetCurrentDeploymentRegistryRootfsParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(depID), WorkloadName: name,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return artifactScanParents{}, ErrApplicationStandardRuntimeStale
	}
	if err != nil {
		return artifactScanParents{}, registryVerificationError(err)
	}
	root, err := registryRootfsRow(row)
	if err != nil {
		return artifactScanParents{}, err
	}
	latest, err := q.GetLatestDeploymentRegistryVerification(ctx, tx, sqlc.GetLatestDeploymentRegistryVerificationParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(depID), WorkloadName: name,
	})
	if err != nil {
		return artifactScanParents{}, registryVerificationError(err)
	}
	proof, err := registryVerificationRow(latest)
	if err != nil {
		return artifactScanParents{}, err
	}
	parent, err := lockArtifactScanParent(ctx, tx, runtimeProducerParentInput(root, proof))
	if errors.Is(err, ErrApplicationStandardReviewBusy) {
		err = ErrApplicationStandardRuntimeBusy
	}
	return parent, err
}

func readRuntimeProducerSet(ctx context.Context, tx pgx.Tx, accountID, appID, depID string, sidecars []byte) (deploymentRuntimeArtifactIdentity, []runtimeProducerLease, error) {
	names, err := artifactScanWorkloads(sidecars)
	if err != nil {
		return deploymentRuntimeArtifactIdentity{}, nil, err
	}
	var identity deploymentRuntimeArtifactIdentity
	parents, bases := []runtimeProducerLease{}, map[string]bool{}
	for _, name := range names {
		selected, err := readRuntimeProducerSelection(ctx, tx, accountID, appID, depID, name)
		if err != nil {
			return identity, nil, err
		}
		if identity.Format == "" {
			identity = selected.Identity
		}
		identity.Artifacts = append(identity.Artifacts, selected.Artifact)
		parents = append(parents, selected.Lease)
		if id := selected.Artifact.BaseProducerID; id != "" && !bases[id] {
			base, err := readRuntimeProducerBase(ctx, tx, id)
			if err != nil {
				return identity, nil, err
			}
			identity.Artifacts, bases[id] = append(identity.Artifacts, runtimeArtifactFromBaseProducer(base)), true
		}
	}
	return identity, parents, nil
}

func readRuntimeProducerBase(ctx context.Context, tx pgx.Tx, id string) (BaseImageProducer, error) {
	// Registry or source selection already holds the current base key fence.
	row, err := sqlc.New().GetBaseImageProducerByID(ctx, tx, mustPgUUID(id))
	if err != nil {
		return BaseImageProducer{}, registryVerificationError(err)
	}
	base, err := baseImageProducerRow(row)
	if err != nil {
		return BaseImageProducer{}, err
	}
	now, err := sqlc.New().ArtifactEvidenceStorageTime(ctx, tx)
	if err != nil || !now.Valid || base.PublishedAt.After(now.Time) {
		return BaseImageProducer{}, errors.Join(ErrApplicationStandardRuntimeStale, err)
	}
	return base, nil
}

func (s *PgStore) HasDeploymentRuntimeProducers(ctx context.Context, accountID, appID, depID string) (bool, error) {
	if !validArtifactScanEvidenceRead(accountID, appID, depID, "") {
		return false, ErrInvalidArgument
	}
	row, err := sqlc.New().GetDeploymentArtifactWorkloads(ctx, s.pool, sqlc.GetDeploymentArtifactWorkloadsParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(depID)})
	if err != nil {
		return false, registryVerificationError(err)
	}
	return row.HasRegistryProducers, ctx.Err()
}
