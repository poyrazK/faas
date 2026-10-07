package state

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func completeStandardReviewArtifactSecurityTx(ctx context.Context, tx pgx.Tx, snapshot *standardReviewSnapshot) error {
	normalizeStandardReviewArchivedPublishers(snapshot)
	for i := range snapshot.Applications {
		app := &snapshot.Applications[i]
		for j := range app.Artifacts {
			artifact := &app.Artifacts[j]
			inputs, err := readRuntimeProducerInputsTx(ctx, tx, app.AccountID, app.AppID, artifact.ID)
			if standardReviewArtifactEvidenceUnavailable(err) {
				continue
			}
			if err != nil {
				return err
			}
			scan, err := currentRuntimeScanRow(ctx, tx, app.AccountID, app.AppID, artifact.ID)
			if standardReviewArtifactEvidenceUnavailable(err) {
				continue
			}
			if err != nil {
				return err
			}
			now, err := sqlc.New().ArtifactEvidenceStorageTime(ctx, tx)
			if err != nil || !now.Valid {
				return errors.Join(ErrApplicationStandardRuntimeStale, err)
			}
			inputs.CheckedAt = now.Time
			artifact.Security, err = prepareStandardReviewArtifactSecurity(inputs, scan)
			if err != nil && !standardReviewArtifactEvidenceUnavailable(err) {
				return err
			}
		}
	}
	return nil
}
