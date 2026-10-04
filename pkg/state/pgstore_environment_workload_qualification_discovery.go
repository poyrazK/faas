package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentGitOpsQualificationDiscoveryStore = (*PgStore)(nil)

func (s *PgStore) ListEnvironmentWorkloadQualificationsForDispatch(ctx context.Context, nodeID, afterRequestID string, limit int) ([]string, error) {
	if !qualificationDispatchPageValid(nodeID, afterRequestID, limit) {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListEnvironmentWorkloadQualificationsForDispatch(ctx, s.pool, sqlc.ListEnvironmentWorkloadQualificationsForDispatchParams{
		NodeID: mustPgUUID(nodeID), AfterRequestID: afterRequestID, PageLimit: int32(limit)})
	if err != nil {
		return nil, mapErr(err)
	}
	ids := make([]string, 0, len(rows))
	for _, id := range rows {
		ids = append(ids, pgUUIDString(id))
	}
	return ids, nil
}
