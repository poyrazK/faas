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

var _ ApplicationStandardReviewStore = (*PgStore)(nil)

func readStandardReviewSnapshot(ctx context.Context, db sqlc.DBTX, orgID, actorID string, r ApplicationStandardReviewRequest) (standardReviewSnapshot, error) {
	if tx, ok := db.(pgx.Tx); ok {
		return readStandardReviewSnapshotTx(ctx, tx, orgID, actorID, r)
	}
	beginner, ok := db.(interface {
		Begin(context.Context) (pgx.Tx, error)
	})
	if !ok {
		return standardReviewSnapshot{}, ErrInvalidArgument
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return standardReviewSnapshot{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := readStandardReviewSnapshotTx(ctx, tx, orgID, actorID, r)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return result, err
}

func readStandardReviewSnapshotTx(ctx context.Context, tx pgx.Tx, orgID, actorID string, r ApplicationStandardReviewRequest) (standardReviewSnapshot, error) {
	raw, err := sqlc.New().ReadApplicationStandardReviewSnapshot(ctx, tx, sqlc.ReadApplicationStandardReviewSnapshotParams{OrgID: mustPgUUID(orgID), ActorID: mustPgUUID(actorID), Scope: r.Scope, ScopeID: mustPgUUID(r.ScopeID), StandardID: mustPgUUID(r.StandardID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return standardReviewSnapshot{}, ErrNotFound
	}
	if err != nil {
		return standardReviewSnapshot{}, fmt.Errorf("read standard review snapshot: %w", err)
	}
	var result standardReviewSnapshot
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, fmt.Errorf("decode standard review snapshot: %w", err)
	}
	if err := completeStandardReviewArtifactSecurityTx(ctx, tx, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (s *PgStore) PreviewApplicationStandardAssignment(ctx context.Context, orgID, actorID string, input ApplicationStandardReviewRequest) (ApplicationStandardReviewPlan, error) {
	r, err := prepareStandardReview(orgID, actorID, input)
	if err != nil {
		return ApplicationStandardReviewPlan{}, err
	}
	snapshot, err := readStandardReviewSnapshot(ctx, s.pool, orgID, actorID, r)
	if err != nil {
		return ApplicationStandardReviewPlan{}, err
	}
	p, err := buildStandardReview(snapshot, r, uuid.NewString(), actorID, time.Now().UTC())
	if err != nil {
		return ApplicationStandardReviewPlan{}, err
	}
	request, err := json.Marshal(p.Request)
	if err != nil {
		return p, err
	}
	inputs, err := json.Marshal(p.approvalInputs)
	if err != nil {
		return p, err
	}
	apps, err := json.Marshal(p.Applications)
	if err != nil {
		return p, err
	}
	blockers, err := json.Marshal(p.Blockers)
	if err != nil {
		return p, err
	}
	err = sqlc.New().InsertApplicationStandardReviewPlan(ctx, s.pool, sqlc.InsertApplicationStandardReviewPlanParams{ID: mustPgUUID(p.ID), OrgID: mustPgUUID(p.OrgID), CreatedBy: mustPgUUID(p.CreatedBy), Request: request, ApprovalInputs: inputs, ApprovalHash: p.ApprovalHash, Applications: apps, Blockers: blockers, CreatedAt: pgtype.Timestamptz{Time: p.CreatedAt, Valid: true}, ExpiresAt: pgtype.Timestamptz{Time: p.ExpiresAt, Valid: true}})
	if err != nil {
		return ApplicationStandardReviewPlan{}, fmt.Errorf("persist standard review plan: %w", err)
	}
	return p, nil
}

func standardReviewPlanRow(row sqlc.ApplicationStandardReviewPlan) (ApplicationStandardReviewPlan, error) {
	p := ApplicationStandardReviewPlan{ID: uuid.UUID(row.ID.Bytes).String(), OrgID: uuid.UUID(row.OrgID.Bytes).String(), CreatedBy: uuid.UUID(row.CreatedBy.Bytes).String(), ApprovalHash: row.ApprovalHash, CreatedAt: row.CreatedAt.Time, ExpiresAt: row.ExpiresAt.Time}
	for _, part := range []struct {
		raw    []byte
		target any
	}{{row.Request, &p.Request}, {row.ApprovalInputs, &p.approvalInputs}, {row.Applications, &p.Applications}, {row.Blockers, &p.Blockers}} {
		if err := json.Unmarshal(part.raw, part.target); err != nil {
			return ApplicationStandardReviewPlan{}, fmt.Errorf("decode standard review plan: %w", err)
		}
	}
	hash, err := appStandardReviewProofHash(p)
	if err != nil || hash != p.ApprovalHash {
		return ApplicationStandardReviewPlan{}, fmt.Errorf("stored standard review proof is invalid")
	}
	return p, nil
}

func (s *PgStore) GetApplicationStandardReviewPlan(ctx context.Context, orgID, planID string) (ApplicationStandardReviewPlan, error) {
	if !validStandardResourceRead(orgID, planID) {
		return ApplicationStandardReviewPlan{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetApplicationStandardReviewPlan(ctx, s.pool, sqlc.GetApplicationStandardReviewPlanParams{OrgID: mustPgUUID(orgID), PlanID: mustPgUUID(planID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ApplicationStandardReviewPlan{}, ErrNotFound
	}
	if err != nil {
		return ApplicationStandardReviewPlan{}, fmt.Errorf("get standard review plan: %w", err)
	}
	return standardReviewPlanRow(row)
}

// Validate is a read-only freshness probe. Activation must call the same
// builder under its own mutation locks and commit the checkpoint atomically;
// a successful probe is never authority for a subsequent unlocked write.
func (s *PgStore) ValidateApplicationStandardReview(ctx context.Context, orgID, actorID, planID, expected string) (ApplicationStandardReviewPlan, error) {
	if !validStandardResourceRead(orgID, actorID) {
		return ApplicationStandardReviewPlan{}, ErrInvalidArgument
	}
	saved, err := s.GetApplicationStandardReviewPlan(ctx, orgID, planID)
	if err != nil {
		return ApplicationStandardReviewPlan{}, err
	}
	snapshot, err := readStandardReviewSnapshot(ctx, s.pool, orgID, actorID, saved.Request)
	if err != nil {
		return ApplicationStandardReviewPlan{}, standardReviewFreshnessError(err)
	}
	fresh, err := buildStandardReview(snapshot, saved.Request, saved.ID, saved.CreatedBy, time.Now().UTC())
	if err != nil {
		return ApplicationStandardReviewPlan{}, standardReviewFreshnessError(err)
	}
	return saved, validateStandardReviewHash(saved, fresh, expected, time.Now().UTC())
}
