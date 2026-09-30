package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type FeatureFlagEvidenceStore interface {
	ListFeatureFlagRequestEvidence(context.Context, sqlc.ListFeatureFlagRequestEvidenceParams) ([]sqlc.ListFeatureFlagRequestEvidenceRow, error)
}

func (s *PgStore) ListFeatureFlagRequestEvidence(ctx context.Context, p sqlc.ListFeatureFlagRequestEvidenceParams) ([]sqlc.ListFeatureFlagRequestEvidenceRow, error) {
	return sqlc.New().ListFeatureFlagRequestEvidence(ctx, s.pool, p)
}
