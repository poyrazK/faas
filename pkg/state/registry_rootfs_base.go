package state

// adr: 435

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func checkRegistryRootfsBase(in DeploymentRegistryRootfsInput, parent DeploymentRegistryVerification, base BaseImageProducer) error {
	if err := validateBaseImageProducer(base); err != nil {
		return err
	}
	if in.BaseProducerID != base.ID || in.BaseInputHash != base.InputHash || in.Kind != "app-layer" && in.Kind != "full-rootfs" || in.StorageKey == base.Input.Artifact.StorageKey {
		return ErrApplicationStandardRuntimeStale
	}
	if in.Kind == "full-rootfs" {
		if in.LayerStart != 0 || base.Input.GuestInitDigest == "" {
			return ErrApplicationStandardRuntimeStale
		}
		return nil // The default base is independent, not an OCI prefix.
	}
	full, err := imagechain.Validate(parent.Input.ImageChain, parent.Input.Proof.SubjectDigest, parent.Input.SelectedDigest)
	if err != nil {
		return ErrApplicationStandardRuntimeStale
	}
	prefix, err := imagechain.Validate(base.Input.ImageChain, base.Input.SourceDigest, base.Input.SelectedDigest)
	if err != nil || len(prefix.DiffIDs) != in.LayerStart || len(full.DiffIDs) < in.LayerStart {
		return ErrApplicationStandardRuntimeStale
	}
	for i, diff := range prefix.DiffIDs {
		if diff != full.DiffIDs[i] {
			return ErrApplicationStandardRuntimeStale
		}
	}
	return nil
}
func lockRegistryRootfsBase(ctx context.Context, tx pgx.Tx, in DeploymentRegistryRootfsInput, parent DeploymentRegistryVerification) error {
	if in.BaseProducerID == "" {
		return nil
	} // historical producers acquire no base authority
	q := sqlc.New()
	row, err := q.GetBaseImageProducerByID(ctx, tx, mustPgUUID(in.BaseProducerID))
	if err != nil {
		return registryVerificationError(err)
	}
	base, err := baseImageProducerRow(row)
	if err != nil {
		return err
	}
	if err := lockBaseImageProducerKeys(ctx, tx, base.Input.Artifact.StorageKey); err != nil {
		return err
	}
	current, err := q.GetCurrentBaseImageProducer(ctx, tx, base.Input.Artifact.StorageKey)
	if err != nil {
		return registryVerificationError(err)
	}
	if pgUUIDString(current.ID) != base.ID {
		return ErrApplicationStandardRuntimeStale
	}
	if err := checkRegistryRootfsBase(in, parent, base); err != nil {
		return err
	}
	return checkRegistryRuntimeDefaultBaseTx(ctx, tx, in, base)
}

func checkRegistryRuntimeDefaultBaseTx(ctx context.Context, tx pgx.Tx, in DeploymentRegistryRootfsInput, base BaseImageProducer) error {
	if in.Kind != "full-rootfs" {
		return nil
	}
	// The registry owner fence already holds the application's row and controls.
	app, err := sqlc.New().AppByID(ctx, tx, mustPgUUID(in.AppID))
	if err != nil {
		return registryVerificationError(err)
	}
	return checkRegistryRuntimeDefaultBase(in, base, app.Runtime)
}
