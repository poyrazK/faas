package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func imagePreparationFromRow(p sqlc.DeploymentImagePreparation) ImagePreparation {
	return ImagePreparation{DeploymentID: uuid.UUID(p.DeploymentID.Bytes).String(), NodeName: p.NodeName,
		InputPath: p.InputPath, InputKey: p.InputKey, InputBytes: p.InputBytes,
		ClaimToken: uuid.UUID(p.ClaimToken.Bytes).String(), Phase: ImagePreparationPhase(p.Phase), UpdatedAt: p.UpdatedAt.Time,
		PhaseStartedAt: p.PhaseStartedAt.Time}
}

func (s *PgStore) BeginImagePreparation(ctx context.Context, id, node string) (ImagePreparation, error) {
	deploymentID, err := parsePgUUID(id)
	if err != nil {
		return ImagePreparation{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ImagePreparation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	d, err := q.LockImagePreparationDeployment(ctx, tx, deploymentID)
	if err != nil {
		return ImagePreparation{}, mapErr(err)
	}
	row, err := q.GetImagePreparation(ctx, tx, deploymentID)
	var prior *ImagePreparation
	if err == nil {
		p := imagePreparationFromRow(row)
		prior = &p
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ImagePreparation{}, err
	}
	node = strings.TrimSpace(node)
	if err := imagePreparationCanBegin(DeploymentStatus(d.Status), prior, node); err != nil {
		return ImagePreparation{}, err
	}
	if prior == nil && d.InputPath == "" {
		return ImagePreparation{}, ErrInvalidArgument
	}
	token, _ := parsePgUUID(uuid.NewString())
	row, err = q.BeginImagePreparation(ctx, tx, sqlc.BeginImagePreparationParams{
		DeploymentID: deploymentID, NodeName: node, InputPath: d.InputPath,
		InputKey: d.RootfsKey, InputBytes: d.InputBytes, ClaimToken: token})
	if err != nil {
		return ImagePreparation{}, fmt.Errorf("begin image preparation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ImagePreparation{}, err
	}
	return imagePreparationFromRow(row), nil
}

func (s *PgStore) PublishImagePreparationLayer(ctx context.Context, id, token, path, key string, bytes int64) error {
	if path == "" || key == "" || bytes < 0 {
		return ErrInvalidArgument
	}
	deploymentID, err := parsePgUUID(id)
	if err != nil {
		return err
	}
	claimToken, err := parsePgUUID(token)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockImagePreparationDeployment(ctx, tx, deploymentID); err != nil {
		return mapErr(err)
	}
	rows, err := q.PublishImagePreparationLayer(ctx, tx, sqlc.PublishImagePreparationLayerParams{
		DeploymentID: deploymentID, ClaimToken: claimToken, Path: path, Key: key, Bytes: bytes})
	if err != nil {
		return fmt.Errorf("publish image preparation layer: %w", err)
	}
	if rows != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (s *PgStore) AdvanceImagePreparation(ctx context.Context, id, token string, from, to ImagePreparationPhase) error {
	deploymentID, err := parsePgUUID(id)
	if err != nil {
		return err
	}
	claimToken, err := parsePgUUID(token)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	d, err := q.LockImagePreparationDeployment(ctx, tx, deploymentID)
	if err != nil {
		return mapErr(err)
	}
	if !imagePreparationCanAdvance(DeploymentStatus(d.Status), from, to) {
		return ErrInvalidStateTransition
	}
	rows, err := q.AdvanceImagePreparation(ctx, tx, sqlc.AdvanceImagePreparationParams{
		DeploymentID: deploymentID, ClaimToken: claimToken, ExpectedPhase: string(from), NextPhase: string(to)})
	if err != nil {
		return fmt.Errorf("advance image preparation: %w", err)
	}
	if rows != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (s *PgStore) ListResumableImagePreparations(ctx context.Context, node string, limit int) ([]BuildImageWork, error) {
	if limit <= 0 {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListResumableImagePreparations(ctx, s.pool, sqlc.ListResumableImagePreparationsParams{NodeID: strings.TrimSpace(node), BatchLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]BuildImageWork, 0, len(rows))
	for _, p := range rows {
		out = append(out, BuildImageWork{AppID: uuid.UUID(p.AppID.Bytes).String(), DeploymentID: uuid.UUID(p.DeploymentID.Bytes).String(), NodeID: p.NodeID})
	}
	return out, nil
}

// imagePreparationTransitionAttempts bounds how often a transition that
// Postgres picked as a deadlock victim is retried. Its status update locks
// the deployment row, then the owning account row (the lifecycle successor
// trigger), while a wake admission can hold the account row and wait on the
// deployment row. Two deploys of one app at once lost a deployment to that
// cycle (Hunt 31 H31-2); the transition is short and claim-guarded, so a
// fresh attempt is safe.
const imagePreparationTransitionAttempts = 3

func (s *PgStore) TransitionImagePreparation(ctx context.Context, id, token string, status DeploymentStatus) error {
	deploymentID, err := parsePgUUID(id)
	if err != nil {
		return err
	}
	claimToken, err := parsePgUUID(token)
	if err != nil {
		return err
	}
	for attempt := 1; ; attempt++ {
		err = s.transitionImagePreparationOnce(ctx, deploymentID, claimToken, status)
		if !transientLockConflict(err) || attempt == imagePreparationTransitionAttempts {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(time.Duration(attempt) * 50 * time.Millisecond):
		}
	}
}

// transientLockConflict reports a transaction Postgres aborted to break a
// deadlock or a serialization conflict; retrying it from the start is safe.
func transientLockConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40P01" || pgErr.Code == "40001")
}

func (s *PgStore) transitionImagePreparationOnce(ctx context.Context, deploymentID, claimToken pgtype.UUID, status DeploymentStatus) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockImagePreparationDeployment(ctx, tx, deploymentID); err != nil {
		return mapErr(err)
	}
	rows, err := q.TransitionImagePreparation(ctx, tx, sqlc.TransitionImagePreparationParams{DeploymentID: deploymentID, ClaimToken: claimToken, NextStatus: string(status)})
	if err != nil {
		return fmt.Errorf("transition image preparation: %w", err)
	}
	if rows != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
