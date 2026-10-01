package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type FeatureFlagEvidenceStore interface {
	ListFeatureFlagRequestEvidence(context.Context, sqlc.ListFeatureFlagRequestEvidenceParams) ([]sqlc.ListFeatureFlagRequestEvidenceRow, error)
	FeatureFlagRequestOutcomes(context.Context, sqlc.FeatureFlagRequestOutcomesParams) ([]sqlc.FeatureFlagRequestOutcomesRow, error)
}

func (s *PgStore) ListFeatureFlagRequestEvidence(ctx context.Context, p sqlc.ListFeatureFlagRequestEvidenceParams) ([]sqlc.ListFeatureFlagRequestEvidenceRow, error) {
	return sqlc.New().ListFeatureFlagRequestEvidence(ctx, s.pool, p)
}

func (s *PgStore) FeatureFlagRequestOutcomes(ctx context.Context, p sqlc.FeatureFlagRequestOutcomesParams) ([]sqlc.FeatureFlagRequestOutcomesRow, error) {
	return sqlc.New().FeatureFlagRequestOutcomes(ctx, s.pool, p)
}
