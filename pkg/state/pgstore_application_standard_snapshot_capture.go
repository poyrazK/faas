package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ApplicationStandardSnapshotCaptureStore = (*PgStore)(nil)

type standardSnapshotLockedInputs struct {
	nativeBootLockedInputs
	Parent                  runtimeadmission.Receipt `json:"parent"`
	SourceStartedAtUnixNano int64                    `json:"source_started_at_unix_nano"`
}

func lockStandardSnapshotCapture(ctx context.Context, tx pgx.Tx, id, expectedState string) (standardSnapshotLockedInputs, error) {
	raw, err := sqlc.New().LockApplicationStandardSnapshotCapture(ctx, tx, sqlc.LockApplicationStandardSnapshotCaptureParams{InstanceID: mustPgUUID(id), ExpectedState: expectedState})
	if err != nil {
		return standardSnapshotLockedInputs{}, mapErr(err)
	}
	var input standardSnapshotLockedInputs
	if err := json.Unmarshal(raw, &input); err != nil {
		return input, err
	}
	err = lockStandardNativeArtifactInputs(ctx, tx, &input.nativeBootLockedInputs, id, false)
	return input, err
}

func decodeStandardSnapshotRecord(row sqlc.GetApplicationStandardSnapshotCaptureRow) (ApplicationStandardSnapshotCaptureRecord, error) {
	r := ApplicationStandardSnapshotCaptureRecord{ExpectedState: row.ExpectedState, CreatedAt: row.CreatedAt.Time, ReceivedAt: row.ReceivedAt.Time}
	if err := json.Unmarshal(row.GrantData, &r.Grant); err != nil {
		return r, err
	}
	if len(row.Acknowledgment) > 0 {
		var ack runtimeadmission.SnapshotAcknowledgment
		if err := json.Unmarshal(row.Acknowledgment, &ack); err != nil {
			return r, err
		}
		r.Acknowledgment = &ack
	}
	return r, standardSnapshotRecordValid(r)
}

func standardSnapshotQuery(g runtimeadmission.SnapshotGrant) sqlc.GetApplicationStandardSnapshotCaptureParams {
	b := g.Parent.Binding
	return sqlc.GetApplicationStandardSnapshotCaptureParams{Token: mustPgUUID(g.Token), AccountID: mustPgUUID(b.AccountID), AppID: mustPgUUID(b.AppID), DeploymentID: mustPgUUID(b.DeploymentID)}
}

func (s *PgStore) IssueApplicationStandardSnapshotCapture(ctx context.Context, expectedState string, req ApplicationStandardSnapshotCaptureRequest) (runtimeadmission.SnapshotGrant, error) {
	if err := req.validate(expectedState); err != nil {
		return runtimeadmission.SnapshotGrant{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return runtimeadmission.SnapshotGrant{}, err
	}
	defer tx.Rollback(ctx)
	g, err := issueStandardSnapshotCaptureTx(ctx, tx, expectedState, req)
	if err != nil {
		return runtimeadmission.SnapshotGrant{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return runtimeadmission.SnapshotGrant{}, mapErr(err)
	}
	return g, nil
}

func issueStandardSnapshotCaptureTx(ctx context.Context, tx pgx.Tx, expectedState string, req ApplicationStandardSnapshotCaptureRequest) (runtimeadmission.SnapshotGrant, error) {
	input, err := lockStandardSnapshotCapture(ctx, tx, req.InstanceID, expectedState)
	if err != nil {
		return runtimeadmission.SnapshotGrant{}, err
	}
	if req.SourceStartedAtUnixNano != input.SourceStartedAtUnixNano {
		return runtimeadmission.SnapshotGrant{}, ErrApplicationStandardRuntimeStale
	}
	now := time.Unix(0, input.ClockUnixNano).UTC()
	expires, err := standardNativeGrantExpiry(now, input.artifactDeadline())
	if err != nil {
		return runtimeadmission.SnapshotGrant{}, err
	}
	g := req.grant(input.Parent, now, expires)
	if g.Validate(now) != nil {
		return runtimeadmission.SnapshotGrant{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetApplicationStandardSnapshotCapture(ctx, tx, standardSnapshotQuery(g))
	if err == nil {
		old, err := decodeStandardSnapshotRecord(row)
		if err != nil {
			return runtimeadmission.SnapshotGrant{}, err
		}
		return standardSnapshotIssueRetry(old, expectedState, g, now, input.artifactDeadline())
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return runtimeadmission.SnapshotGrant{}, mapErr(err)
	}
	if err := insertStandardSnapshotCapture(ctx, tx, expectedState, g, input.Snapshot); err != nil {
		return runtimeadmission.SnapshotGrant{}, err
	}
	return g, nil
}

func insertStandardSnapshotCapture(ctx context.Context, tx pgx.Tx, state string, g runtimeadmission.SnapshotGrant, input []byte) error {
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	b := g.Parent.Binding
	return mapErr(sqlc.New().InsertApplicationStandardSnapshotCapture(ctx, tx, sqlc.InsertApplicationStandardSnapshotCaptureParams{Token: mustPgUUID(g.Token), InstanceID: mustPgUUID(b.InstanceID), AppID: mustPgUUID(b.AppID), DeploymentID: mustPgUUID(b.DeploymentID), AccountID: mustPgUUID(b.AccountID), NodeID: mustPgUUID(b.NodeID), ParentToken: mustPgUUID(b.Token), MemoryKey: g.MemoryKey, ExpectedState: state, GrantData: raw, InputSnapshot: input}))
}

func (s *PgStore) PublishApplicationStandardSnapshotCapture(ctx context.Context, ack runtimeadmission.SnapshotAcknowledgment) error {
	if ack.Grant.Validate(time.Unix(0, ack.Grant.IssuedAtUnixNano)) != nil {
		return ErrApplicationStandardRuntimeStale
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := publishStandardSnapshotCaptureTx(ctx, tx, ack); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}

func publishStandardSnapshotCaptureTx(ctx context.Context, tx pgx.Tx, ack runtimeadmission.SnapshotAcknowledgment) error {
	q := sqlc.New()
	row, err := q.GetApplicationStandardSnapshotCapture(ctx, tx, standardSnapshotQuery(ack.Grant))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrApplicationStandardRuntimeStale
		}
		return mapErr(err)
	}
	r, err := decodeStandardSnapshotRecord(row)
	if err != nil || !r.Grant.Equal(ack.Grant) {
		return ErrApplicationStandardRuntimeStale
	}
	if r.Acknowledgment != nil {
		if !r.Acknowledgment.Equal(ack) {
			return ErrConflict
		}
		return nil // Read an exact committed acknowledgment, without new authority.
	}
	input, err := lockStandardSnapshotCapture(ctx, tx, r.Grant.Parent.Binding.InstanceID, r.ExpectedState)
	if err != nil {
		return err
	}
	if !input.Parent.Equal(r.Grant.Parent) || input.SourceStartedAtUnixNano != r.Grant.SourceStartedAtUnixNano || !standardNativeGrantWithinArtifactLease(r.Grant.ExpiresAtUnixNano, input.artifactDeadline()) || ack.Check(r.Grant, time.Unix(0, input.ClockUnixNano)) != nil {
		return ErrApplicationStandardRuntimeStale
	}
	return recordStandardSnapshotCapture(ctx, tx, ack)
}

func recordStandardSnapshotCapture(ctx context.Context, tx pgx.Tx, ack runtimeadmission.SnapshotAcknowledgment) error {
	raw, err := json.Marshal(ack)
	if err != nil {
		return err
	}
	count, err := sqlc.New().RecordApplicationStandardSnapshotCapture(ctx, tx, sqlc.RecordApplicationStandardSnapshotCaptureParams{Token: mustPgUUID(ack.Grant.Token), Acknowledgment: raw})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) GetApplicationStandardSnapshotCapture(ctx context.Context, accountID, appID, deploymentID, token string) (ApplicationStandardSnapshotCaptureRecord, error) {
	for _, value := range []string{accountID, appID, deploymentID, token} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return ApplicationStandardSnapshotCaptureRecord{}, ErrNotFound
		}
	}
	row, err := sqlc.New().GetApplicationStandardSnapshotCapture(ctx, s.pool, sqlc.GetApplicationStandardSnapshotCaptureParams{Token: mustPgUUID(token), AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), DeploymentID: mustPgUUID(deploymentID)})
	if err != nil {
		return ApplicationStandardSnapshotCaptureRecord{}, mapErr(err)
	}
	return decodeStandardSnapshotRecord(row)
}
