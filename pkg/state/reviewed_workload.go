package state

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func (m *MemStore) prepareReviewedWorkloadLocked(app App, d Deployment, review api.AppReleaseReview) (ProjectEnvironmentWorkloadSpec, error) {
	if review.EnvironmentID == "" {
		return ProjectEnvironmentWorkloadSpec{}, nil
	}
	if id := m.projectEnvironmentWorkloadHeads[workloadSpecHeadKey(review.EnvironmentID, app.ID)]; id != "" {
		return m.projectEnvironmentWorkloadSpecs[id], nil
	}
	if d.ReviewedSettings == nil {
		return ProjectEnvironmentWorkloadSpec{}, ErrInvalidArgument
	}
	settings, err := cloneWorkloadSettings(*d.ReviewedSettings)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	hash, err := WorkloadSettingsHash(settings)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	return ProjectEnvironmentWorkloadSpec{ID: newID(), AccountID: app.AccountID, ProjectID: app.ProjectID, EnvironmentID: review.EnvironmentID, EnvironmentSlug: d.Scope, AppID: app.ID, Revision: m.nextWorkloadSpecRevisionLocked(review.EnvironmentID, app.ID), Hash: hash, Settings: settings, CreatedAt: time.Now().UTC()}, nil
}

func prepareReviewedWorkloadTx(ctx context.Context, tx pgx.Tx, d Deployment, review api.AppReleaseReview) error {
	if review.EnvironmentID == "" || review.WorkloadRevision != 0 {
		return nil
	}
	if d.ReviewedSettings == nil {
		return ErrInvalidArgument
	}
	_, err := putWorkloadSpecTx(ctx, tx, review.EnvironmentID, d.AppID, 0, *d.ReviewedSettings)
	return err
}
