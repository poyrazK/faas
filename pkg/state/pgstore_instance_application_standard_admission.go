package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ InstanceApplicationStandardAdmissionStore = (*PgStore)(nil)

func (s *PgStore) GetInstanceApplicationStandardAdmission(ctx context.Context, id string) (InstanceApplicationStandardAdmission, error) {
	if !validStandardResourceRead(id, id) {
		return InstanceApplicationStandardAdmission{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetInstanceApplicationStandardAdmission(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return InstanceApplicationStandardAdmission{}, fmt.Errorf("read instance standards capture: %w", mapErr(err))
	}
	return decodeInstanceStandardAdmission(id, row.InputSnapshot, row.CapturedAt.Time)
}
