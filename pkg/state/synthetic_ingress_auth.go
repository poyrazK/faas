// adr: 570
package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ReadSyntheticIngressAuthMode returns only the fresh declared ingress mode of
// an existing app that has not been deleted. Callers bound its read context.
func (s *PgStore) ReadSyntheticIngressAuthMode(ctx context.Context, appID string) (string, error) {
	mode, err := sqlc.New().ReadSyntheticIngressAuthMode(ctx, s.pool, uuidToPgtype(appID))
	if err != nil {
		return "", mapErr(err)
	}
	return mode, nil
}

func (m *MemStore) ReadSyntheticIngressAuthMode(ctx context.Context, appID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	app, err := m.appByIDLocked(appID)
	if err != nil {
		return "", err
	}
	if app.Status == AppDeleted || app.DeletedAt != nil {
		return "", ErrNotFound
	}
	return app.PublicAuthMode, nil
}
