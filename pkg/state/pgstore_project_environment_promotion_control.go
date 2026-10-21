package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) RequestProjectPromotionControl(ctx context.Context, accountID, promotionID, action string, req api.ProjectPromotionControlRequest) (*api.ProjectPromotionControl, error) {
	if !req.Valid() || action != "abort" && action != "recover" {
		return nil, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	projectID, err := q.PromotionHealthProject(ctx, tx, sqlc.PromotionHealthProjectParams{AccountID: accountID, PromotionID: promotionID})
	if err != nil {
		return nil, mapErr(err)
	}
	if err = lockProjectForReleaseSetTx(ctx, tx, accountID, projectID); err != nil {
		return nil, err
	}
	owner, err := q.LockPromotionHealthOwner(ctx, tx, sqlc.LockPromotionHealthOwnerParams{AccountID: accountID, PromotionID: promotionID})
	if err != nil {
		return nil, mapErr(err)
	}
	raw, err := q.ReadPromotionControl(ctx, tx, sqlc.ReadPromotionControlParams{PromotionID: promotionID, RequestID: req.RequestID})
	if err == nil {
		var old api.ProjectPromotionControl
		if json.Unmarshal(raw, &old) != nil || old.Request != req || old.Action != action {
			return nil, ErrConflict
		}
		return clonePromotionControl(&old), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, mapErr(err)
	}
	h, err := readPromotionHealthDB(ctx, tx, accountID, promotionID)
	if err != nil {
		return nil, err
	}
	active, err := activeProjectReleaseSetIDTx(ctx, tx, projectID, owner.ToEnvironment)
	if err != nil {
		return nil, err
	}
	next, err := requestPromotionControl(promotionFromHealthOwner(owner), h, active, action, req, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(next.Control)
	if err != nil {
		return nil, err
	}
	if err = q.InsertPromotionControl(ctx, tx, sqlc.InsertPromotionControlParams{PromotionID: promotionID, RequestID: req.RequestID, Payload: payload}); err != nil {
		return nil, mapErr(err)
	}
	if err = savePromotionHealthDB(ctx, tx, promotionID, h.Generation, next); err != nil {
		return nil, err
	}
	if next.Phase == "aborted" {
		if err = q.RetireAbortedPromotionCandidates(ctx, tx, promotionID); err != nil {
			return nil, mapErr(err)
		}
	}
	status, verification, message := promotionHealthResult(next)
	if err = q.UpdatePromotionHealthResult(ctx, tx, sqlc.UpdatePromotionHealthResultParams{PromotionID: promotionID, Status: status, VerificationStatus: verification, ErrorMessage: message}); err != nil {
		return nil, mapErr(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return clonePromotionControl(next.Control), nil
}

func savePromotionControlDB(ctx context.Context, db sqlc.DBTX, id string, c *api.ProjectPromotionControl) error {
	if c == nil {
		return nil
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	n, err := sqlc.New().UpdatePromotionControl(ctx, db, sqlc.UpdatePromotionControlParams{PromotionID: id, RequestID: c.Request.RequestID, Payload: raw})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func guardPromotionControlRollbackDB(ctx context.Context, tx pgx.Tx, p ProjectEnvironmentPromotion) error {
	h, err := readPromotionHealthDB(ctx, tx, p.AccountID, p.ID)
	if err != nil {
		return err
	}
	if err = guardPromotionControlRollback(h, time.Now().UTC()); err != nil {
		return err
	}
	if h != nil && h.Control != nil {
		usable, err := sqlc.New().PromotionControlBaselineUsable(ctx, tx, p.PreviousTargetReleaseSetID)
		if err != nil {
			return mapErr(err)
		}
		if !usable {
			return ErrConflict
		}
		return mapErr(sqlc.New().AuthorizePromotionControlRollback(ctx, tx, sqlc.AuthorizePromotionControlRollbackParams{PromotionID: p.ID, Generation: h.Generation, RequestID: h.Control.Request.RequestID}))
	}
	return nil
}

// Take the same project/owner locks as abort before preparation locks environments,
// apps or deployments. Abort can then retire every owned candidate atomically.
func lockPromotionPreparationDB(ctx context.Context, tx pgx.Tx, id string) error {
	q := sqlc.New()
	owner, err := q.PromotionPreparationOwner(ctx, tx, id)
	if err != nil {
		return mapErr(err)
	}
	if err = lockProjectForReleaseSetTx(ctx, tx, owner.AccountID, owner.ProjectID); err != nil {
		return err
	}
	p, err := q.LockPromotionHealthOwner(ctx, tx, sqlc.LockPromotionHealthOwnerParams{AccountID: owner.AccountID, PromotionID: id})
	if err != nil {
		return mapErr(err)
	}
	if p.Status != "running" || p.TargetReleaseID != "" {
		return ErrConflict
	}
	h, err := readPromotionHealthDB(ctx, tx, owner.AccountID, id)
	if err != nil {
		return err
	}
	if h != nil && (h.Phase != "migrating" && h.Phase != "preflight" && h.Phase != "cutover" || !time.Now().Before(h.Deadline)) {
		return ErrConflict
	}
	return nil
}
