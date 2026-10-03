package state

// adr: 435. Existing owner/control/artifact/base fences protect scanner bootstrap.

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ DeploymentRuntimeProducerInputStore = (*PgStore)(nil)

func (s *PgStore) GetFreshDeploymentRuntimeProducerInputs(ctx context.Context, accountID, appID, depID string) (DeploymentRuntimeProducerInputs, error) {
	if !validArtifactScanEvidenceRead(accountID, appID, depID, "") {
		return DeploymentRuntimeProducerInputs{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DeploymentRuntimeProducerInputs{}, err
	}
	defer tx.Rollback(ctx)
	owner, err := readArtifactEvidenceOwner(ctx, tx, accountID, appID, depID)
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
	inputs, err := finishRuntimeProducerInputs(identity, parents, now.Time)
	if err != nil {
		return DeploymentRuntimeProducerInputs{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DeploymentRuntimeProducerInputs{}, err
	}
	return inputs, nil
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

func readRuntimeProducerSet(ctx context.Context, tx pgx.Tx, accountID, appID, depID string, sidecars []byte) (deploymentRuntimeArtifactIdentity, []artifactScanParents, error) {
	names, err := artifactScanWorkloads(sidecars)
	if err != nil {
		return deploymentRuntimeArtifactIdentity{}, nil, err
	}
	var identity deploymentRuntimeArtifactIdentity
	parents, bases := []artifactScanParents{}, map[string]bool{}
	for _, name := range names {
		parent, err := readRuntimeProducer(ctx, tx, accountID, appID, depID, name)
		if err != nil {
			return identity, nil, err
		}
		root := parent.Rootfs
		if identity.Format == "" {
			identity = runtimeProducerIdentity(root)
		}
		identity.Artifacts = append(identity.Artifacts, runtimeArtifactFromRootfs(root))
		parents = append(parents, parent)
		if id := root.Input.BaseProducerID; id != "" && !bases[id] {
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
	// lockArtifactScanParent already holds this producer key's nonwaiting fence
	// and checked the current pointer, prefix consumption and immutable hash.
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
