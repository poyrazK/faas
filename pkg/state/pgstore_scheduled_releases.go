package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ScheduledReleaseStore = (*PgStore)(nil)

func scheduledReleaseFromRow(r sqlc.ScheduledRelease, revision int32) ScheduledRelease {
	s := ScheduledRelease{
		ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID),
		DeploymentID: pgUUIDString(r.DeploymentID), ReleaseAt: r.ReleaseAt.Time.UTC(),
		NotifyChannelIDs: pgUUIDStrings(r.NotifyChannelIds), OverrideFreeze: r.OverrideFreeze,
		OverrideFreezeIDs: pgUUIDStrings(r.OverrideFreezeIds), State: r.State, OutcomeCode: r.OutcomeCode,
		OutcomeDetail: r.OutcomeDetail, ScheduledBy: r.ScheduledBy, CreatedAt: r.CreatedAt.Time.UTC(),
		UpdatedAt: r.UpdatedAt.Time.UTC(), Revision: int(revision),
	}
	if len(r.Canary) > 0 {
		s.Canary = json.RawMessage(r.Canary)
	}
	if r.StartedAt.Valid {
		at := r.StartedAt.Time.UTC()
		s.StartedAt = &at
	}
	return s
}

func (s *PgStore) ScheduleRelease(ctx context.Context, sr ScheduledRelease, replacedCode, replacedDetail string, now time.Time) (_ ScheduledRelease, _ []ScheduledRelease, resultErr error) {
	defer func() { resultErr = mapErr(resultErr) }()
	if sr.ID == "" {
		sr.ID = uuid.NewString()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ScheduledRelease{}, nil, fmt.Errorf("state: schedule release begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	q := sqlc.New()
	cancelled, err := q.CancelPendingScheduledReleasesForApp(ctx, tx, sqlc.CancelPendingScheduledReleasesForAppParams{
		OutcomeCode: replacedCode, OutcomeDetail: replacedDetail, Now: pgTimestamptzUTC(now), AppID: mustPgUUID(sr.AppID),
	})
	if err != nil {
		return ScheduledRelease{}, nil, fmt.Errorf("state: cancel earlier scheduled release: %w", err)
	}
	var canary []byte
	if len(sr.Canary) > 0 {
		canary = []byte(sr.Canary)
	}
	row, err := q.InsertScheduledRelease(ctx, tx, sqlc.InsertScheduledReleaseParams{
		ID: mustPgUUID(sr.ID), AccountID: mustPgUUID(sr.AccountID), AppID: mustPgUUID(sr.AppID),
		DeploymentID: mustPgUUID(sr.DeploymentID), ReleaseAt: pgTimestamptzUTC(sr.ReleaseAt), Canary: canary,
		NotifyChannelIds: pgUUIDList(sr.NotifyChannelIDs), OverrideFreeze: sr.OverrideFreeze,
		OverrideFreezeIds: pgUUIDList(sr.OverrideFreezeIDs), ScheduledBy: sr.ScheduledBy, Now: pgTimestamptzUTC(now),
	})
	if err != nil {
		return ScheduledRelease{}, nil, fmt.Errorf("state: insert scheduled release: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ScheduledRelease{}, nil, fmt.Errorf("state: schedule release commit: %w", err)
	}
	created, err := s.ScheduledRelease(ctx, pgUUIDString(row.ID))
	if err != nil {
		return ScheduledRelease{}, nil, err
	}
	replaced := make([]ScheduledRelease, 0, len(cancelled))
	for _, c := range cancelled {
		previous, err := s.ScheduledRelease(ctx, pgUUIDString(c.ID))
		if err != nil {
			return ScheduledRelease{}, nil, err
		}
		replaced = append(replaced, previous)
	}
	return created, replaced, nil
}

func (s *PgStore) ScheduledRelease(ctx context.Context, id string) (ScheduledRelease, error) {
	row, err := sqlc.New().GetScheduledRelease(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return ScheduledRelease{}, mapErr(err)
	}
	return scheduledReleaseFromRow(row.ScheduledRelease, row.Revision), nil
}

func (s *PgStore) LatestScheduledRelease(ctx context.Context, deploymentID string) (ScheduledRelease, error) {
	row, err := sqlc.New().GetLatestScheduledReleaseForDeployment(ctx, s.pool, mustPgUUID(deploymentID))
	if err != nil {
		return ScheduledRelease{}, mapErr(err)
	}
	return scheduledReleaseFromRow(row.ScheduledRelease, row.Revision), nil
}

func (s *PgStore) ListScheduledReleases(ctx context.Context, accountID, appID string, limit int) ([]ScheduledRelease, error) {
	app := pgtype.UUID{}
	if appID != "" {
		app = mustPgUUID(appID)
	}
	rows, err := sqlc.New().ListScheduledReleases(ctx, s.pool, sqlc.ListScheduledReleasesParams{
		AccountID: mustPgUUID(accountID), AppID: app, MaxRows: int32(limit),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ScheduledRelease, 0, len(rows))
	for _, r := range rows {
		out = append(out, scheduledReleaseFromRow(r.ScheduledRelease, r.Revision))
	}
	return out, nil
}

func (s *PgStore) DueScheduledReleases(ctx context.Context, now time.Time, limit int) ([]ScheduledRelease, error) {
	rows, err := sqlc.New().ListDueScheduledReleases(ctx, s.pool, sqlc.ListDueScheduledReleasesParams{
		Now: pgTimestamptzUTC(now), MaxRows: int32(limit),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ScheduledRelease, 0, len(rows))
	for _, r := range rows {
		out = append(out, scheduledReleaseFromRow(r.ScheduledRelease, r.Revision))
	}
	return out, nil
}

func (s *PgStore) FinishScheduledRelease(ctx context.Context, id, state, code, detail string, now time.Time) (ScheduledRelease, error) {
	if _, err := sqlc.New().FinishScheduledRelease(ctx, s.pool, sqlc.FinishScheduledReleaseParams{
		State: state, OutcomeCode: code, OutcomeDetail: detail, Now: pgTimestamptzUTC(now), ID: mustPgUUID(id),
	}); err != nil {
		return ScheduledRelease{}, mapErr(err)
	}
	return s.ScheduledRelease(ctx, id)
}

func (s *PgStore) StartScheduledRelease(ctx context.Context, start ScheduledReleaseStart) (Deployment, error) {
	return s.updateDeploymentTraffic(withScheduledStart(ctx, start), start.DeploymentID, start.startPercent(), nil, nil)
}

// checkScheduledStartTx locks the schedule a traffic change starts and
// refuses a schedule that is no longer pending or a release that is not
// parked.
func checkScheduledStartTx(ctx context.Context, tx pgx.Tx, start ScheduledReleaseStart, deploymentID string) error {
	q := sqlc.New()
	row, err := q.LockScheduledRelease(ctx, tx, mustPgUUID(start.ScheduleID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("state: lock scheduled release: %w", err)
	}
	if row.State != ScheduledReleasePending || !sameDeploymentID(pgUUIDString(row.DeploymentID), deploymentID) {
		return ErrNotFound
	}
	parked, err := q.ReadScheduledStartDeployment(ctx, tx, mustPgUUID(deploymentID))
	if err != nil {
		return fmt.Errorf("state: read scheduled release: %w", mapErr(err))
	}
	if parked.TrafficPercent != 0 || parked.CanaryTotalSteps != 0 {
		return ErrScheduledReleaseNotParked
	}
	return nil
}

// stampScheduledStartTx records the started rollout on the release and the
// schedule, inside the traffic change that gave the release its traffic.
func stampScheduledStartTx(ctx context.Context, tx pgx.Tx, start ScheduledReleaseStart, deploymentID string) error {
	q := sqlc.New()
	now := pgTimestamptzUTC(start.Now)
	var err error
	if start.Ladder.TotalSteps > 0 {
		var stages []byte
		if len(start.Ladder.Stages) > 0 {
			stages = []byte(start.Ladder.Stages)
		}
		err = q.StampScheduledCanaryStart(ctx, tx, sqlc.StampScheduledCanaryStartParams{
			CanaryPreset: start.Ladder.Preset, CanaryStages: stages, CanaryTotalSteps: int32(start.Ladder.TotalSteps),
			Now: now, ID: mustPgUUID(deploymentID),
		})
	} else {
		err = q.StampScheduledCutoverStart(ctx, tx, sqlc.StampScheduledCutoverStartParams{Now: now, ID: mustPgUUID(deploymentID)})
	}
	if err != nil {
		return fmt.Errorf("state: stamp scheduled release rollout: %w", mapErr(err))
	}
	if err := q.MarkScheduledReleaseStarted(ctx, tx, sqlc.MarkScheduledReleaseStartedParams{Now: now, ID: mustPgUUID(start.ScheduleID)}); err != nil {
		return fmt.Errorf("state: mark scheduled release started: %w", err)
	}
	return nil
}
