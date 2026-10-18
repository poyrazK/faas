package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) UpdateDeploymentTrafficReviewed(ctx context.Context, id string, percent int, serving string, review api.AppReleaseReview) (Deployment, error) {
	return m.updateDeploymentTraffic(ctx, id, percent, []string{serving}, nil, &review)
}

func (s *PgStore) UpdateDeploymentTrafficReviewed(ctx context.Context, id string, percent int, serving string, review api.AppReleaseReview) (Deployment, error) {
	return s.updateDeploymentTraffic(ctx, id, percent, []string{serving}, nil, &review)
}
