package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RouteRemovalBaselineDocStore reads the contract an app's route removal
// policy holds as its baseline (ADR-20261028110044 amendment 6).
type RouteRemovalBaselineDocStore interface {
	RouteRemovalBaselineOpenAPIDoc(ctx context.Context, appID, accountID string) ([]byte, error)
}

var _ RouteRemovalBaselineDocStore = (*PgStore)(nil)

// RouteRemovalBaselineOpenAPIDoc returns the baseline deployment's captured
// OpenAPI document, or ErrNotFound when the app has no removal policy, no
// baseline or only a truncated capture.
func (s *PgStore) RouteRemovalBaselineOpenAPIDoc(ctx context.Context, appID, accountID string) ([]byte, error) {
	doc, err := sqlc.New().RouteRemovalBaselineDoc(ctx, s.pool, sqlc.RouteRemovalBaselineDocParams{AppID: appID, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read route removal baseline contract: %w", err)
	}
	return doc, nil
}
