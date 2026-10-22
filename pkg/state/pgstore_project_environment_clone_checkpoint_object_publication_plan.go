package state

import (
	"context"

	"github.com/jackc/pgx/v5"
)

var _ ProjectEnvironmentCloneCheckpointObjectPublicationPlanStore = (*PgStore)(nil)

func (s *PgStore) ProjectEnvironmentCloneCheckpointObjectPublicationPlan(ctx context.Context, l ProjectEnvironmentCloneLease, retention ProjectEnvironmentCloneCheckpointReleaseObservation) (ProjectEnvironmentCloneCheckpointObjectPublicationPlan, error) {
	var out ProjectEnvironmentCloneCheckpointObjectPublicationPlan
	err := s.checkpointReleaseTx(ctx, l, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
		r, err := checkpointObjectCredentialReservationTx(ctx, tx, op, l, retention, false)
		if err != nil {
			return err
		}
		values, err := readCheckpointObjectValuesTx(ctx, tx, op, r)
		if err != nil {
			return err
		}
		out, err = checkpointObjectPublicationPlan(r, values)
		return err
	})
	if err != nil {
		return ProjectEnvironmentCloneCheckpointObjectPublicationPlan{}, err
	}
	return out, nil
}
