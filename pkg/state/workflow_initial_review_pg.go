package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) initialReviewWriteTx(ctx context.Context, account, app string) (pgx.Tx, api.WorkflowRecoveryPolicyResponse, string, error) {
	var p api.WorkflowRecoveryPolicyResponse
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, p, "", fmt.Errorf("begin initial recovery review: %w", err)
	}
	q := sqlc.New()
	if err = q.EventReplayBackfillLockAccountRange(ctx, tx, mustPgUUID(account)); err != nil {
		_ = tx.Rollback(ctx)
		return nil, p, "", fmt.Errorf("lock initial recovery account: %w", err)
	}
	row, err := q.WorkflowInitialReviewPolicy(ctx, tx, sqlc.WorkflowInitialReviewPolicyParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err != nil {
		_ = tx.Rollback(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, p, "", ErrNotFound
		}
		return nil, p, "", fmt.Errorf("lock initial recovery policy: %w", err)
	}
	p, err = pgPolicyResponse(uuidString(row.AppID), row.Version, row.Policy, row.UpdatedAt, row.ActorKind, row.ActorID, row.LastEvaluatedAt, row.Status, row.AutomaticFailures, row.AutomaticPausedAt, row.AutomaticPauseReason, row.AutomaticPollFailure, row.AutomaticPollNotification)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, p, "", err
	}
	return tx, p, row.Slug, nil
}
func initialReviewGetTx(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, app, id string, now time.Time) (api.WorkflowInitialRecoveryReview, error) {
	raw, err := q.WorkflowInitialReviewGet(ctx, db, sqlc.WorkflowInitialReviewGetParams{ID: mustPgUUID(id), AppID: mustPgUUID(app), AccountID: mustPgUUID(account)})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.WorkflowInitialRecoveryReview{}, ErrNotFound
	}
	if err != nil {
		return api.WorkflowInitialRecoveryReview{}, fmt.Errorf("read initial recovery review: %w", err)
	}
	r, err := decodeInitialReview(raw)
	if err == nil && !r.PreparedAt.After(now.Add(-api.WorkflowRecoveryHistoryRetention)) && !initialReviewUnresolved(r) {
		err = ErrNotFound
	}
	return r, err
}
func initialReviewSaveTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, r api.WorkflowInitialRecoveryReview) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encode initial recovery review: %w", err)
	}
	if err = q.WorkflowInitialReviewUpdate(ctx, tx, sqlc.WorkflowInitialReviewUpdateParams{ID: mustPgUUID(r.ID), Review: raw, JobID: recoveryRequestUUID(r.JobID), SubmittedAt: initialReviewSubmittedTime(r.SubmittedAt)}); err != nil {
		return fmt.Errorf("save initial recovery review: %w", err)
	}
	return nil
}
func (s *PgStore) PrepareWorkflowInitialRecoveryReview(ctx context.Context, account, app, failure string, req api.WorkflowInitialRecoveryReviewRequest) (api.WorkflowInitialRecoveryReview, error) {
	var out api.WorkflowInitialRecoveryReview
	if err := eventRecoveryIDs(account, app); err != nil {
		return out, err
	}
	if err := eventRecoveryIDs(account, failure); err != nil {
		return out, err
	}
	if err := req.Validate(); err != nil {
		return out, ErrEventRecoveryQuery
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, p, slug, err := s.initialReviewWriteTx(ctx, account, app)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if p.Version != req.ExpectedPolicyVersion {
		return out, ErrWorkflowRecoveryPolicyConflict
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	q := sqlc.New()
	id := initialReviewID(app, failure, p.Version)
	if err = q.WorkflowInitialReviewPrune(ctx, tx, sqlc.WorkflowInitialReviewPruneParams{AppID: mustPgUUID(app), BeforeAt: pgtypeFromTime(now.Add(-api.WorkflowRecoveryHistoryRetention))}); err != nil {
		return out, fmt.Errorf("prune initial reviews: %w", err)
	}
	existing, err := initialReviewGetTx(ctx, q, tx, account, app, id, now)
	if err == nil {
		preview, previewErr := previewWorkflowInitialRecoveryTx(ctx, q, tx, account, app, failure, now)
		out, err = initialReviewObserved(existing, p, preview, previewErr, now)
		if err != nil {
			return out, err
		}
		return out, tx.Commit(ctx)
	}
	if !errors.Is(err, ErrNotFound) {
		return out, err
	}
	preview, err := previewWorkflowInitialRecoveryTx(ctx, q, tx, account, app, failure, now)
	if err != nil {
		return out, err
	}
	if reason := api.WorkflowInitialFailurePolicyReason(p.Policy, preview); reason != "eligible" {
		return out, initialReviewBlocked(reason)
	}
	pending, err := q.WorkflowInitialRecoveryPending(ctx, tx, sqlc.WorkflowInitialRecoveryPendingParams{AppID: mustPgUUID(app), RunID: preview.Failure.RunID})
	if err != nil {
		return out, fmt.Errorf("inspect pending initial recovery: %w", err)
	}
	if pending {
		return out, initialReviewBlocked("pending_recovery")
	}
	b, err := q.WorkflowInitialReviewBudget(ctx, tx, sqlc.WorkflowInitialReviewBudgetParams{AppID: mustPgUUID(app), SinceAt: pgtypeFromTime(now.Add(-time.Hour))})
	if err != nil {
		return out, fmt.Errorf("read initial review budget: %w", err)
	}
	if b.Retained >= api.WorkflowRecoveryPolicyReviewsMax {
		return out, initialReviewBlocked("review_limit")
	}
	prep, err := q.WorkflowRecoveryPolicyBudget(ctx, tx, sqlc.WorkflowRecoveryPolicyBudgetParams{AppID: mustPgUUID(app), SinceAt: pgtypeFromTime(now.Add(-time.Hour))})
	if err != nil {
		return out, fmt.Errorf("read preparation budget: %w", err)
	}
	if reason := initialReviewBudgetReason(int(prep.HourlyActions), timeFromPgtype(prep.LastPreparedAt), p.Policy, now); reason != "" {
		return out, initialReviewBlocked(reason)
	}
	out = newInitialReview(ctx, slug, p, preview, now)
	if err = captureInitialReviewNotificationTx(ctx, q, tx, account, &out, now); err != nil {
		return out, err
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return out, fmt.Errorf("encode prepared initial review: %w", err)
	}
	if err = q.WorkflowInitialReviewInsert(ctx, tx, sqlc.WorkflowInitialReviewInsertParams{ID: mustPgUUID(id), AppID: mustPgUUID(app), FailureID: mustPgUUID(failure), PolicyVersion: p.Version, PreparedAt: pgtypeFromTime(now), ExpiresAt: pgtypeFromTime(out.ExpiresAt), Review: raw}); err != nil {
		return out, fmt.Errorf("prepare initial recovery review: %w", err)
	}
	if err = appendRecoveryHistoryEvent(ctx, q, tx, app, initialLifecycleHistory(out)); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) GetWorkflowInitialRecoveryReview(ctx context.Context, account, app, id string) (api.WorkflowInitialRecoveryReview, error) {
	var out api.WorkflowInitialRecoveryReview
	if err := eventRecoveryIDs(account, app); err != nil {
		return out, err
	}
	if err := eventRecoveryIDs(account, id); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.initialFailureReadTx(ctx, account, app)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	now := time.Now().UTC().Truncate(time.Microsecond)
	q := sqlc.New()
	out, err = initialReviewGetTx(ctx, q, tx, account, app, id, now)
	if err != nil {
		return out, err
	}
	if out.Status == "prepared" {
		row, err := q.WorkflowRecoveryPolicyGet(ctx, tx, sqlc.WorkflowRecoveryPolicyGetParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account)})
		if err != nil {
			return out, fmt.Errorf("read initial review policy: %w", err)
		}
		p, err := pgPolicyResponse(uuidString(row.AppID), row.Version, row.Policy, row.UpdatedAt, row.ActorKind, row.ActorID, row.LastEvaluatedAt, row.Status, row.AutomaticFailures, row.AutomaticPausedAt, row.AutomaticPauseReason, row.AutomaticPollFailure, row.AutomaticPollNotification)
		if err != nil {
			return out, err
		}
		preview, previewErr := previewWorkflowInitialRecoveryTx(ctx, q, tx, account, app, out.Failure.ID, now)
		out, err = initialReviewObserved(out, p, preview, previewErr, now)
		if err != nil {
			return out, err
		}
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) DecideWorkflowInitialRecoveryReview(ctx context.Context, account, app, id string, req api.WorkflowInitialRecoveryReviewDecisionRequest) (api.WorkflowInitialRecoveryReview, error) {
	var out api.WorkflowInitialRecoveryReview
	if err := eventRecoveryIDs(account, app); err != nil {
		return out, err
	}
	if err := eventRecoveryIDs(account, id); err != nil {
		return out, err
	}
	if err := req.Validate(); err != nil {
		return out, ErrEventRecoveryQuery
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, p, _, err := s.initialReviewWriteTx(ctx, account, app)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err = decideInitialReviewTx(ctx, sqlc.New(), tx, account, app, id, p, req, time.Now().UTC().Truncate(time.Microsecond))
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func decideInitialReviewTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account, app, id string, p api.WorkflowRecoveryPolicyResponse, req api.WorkflowInitialRecoveryReviewDecisionRequest, now time.Time) (api.WorkflowInitialRecoveryReview, error) {
	var out api.WorkflowInitialRecoveryReview
	var err error
	out, err = initialReviewGetTx(ctx, q, tx, account, app, id, now)
	if err != nil {
		return out, err
	}
	if out.Status != "prepared" {
		if req.Decision == "submit" && out.Status == "submitted" || req.Decision == "cancel" && out.Status == "cancelled" {
			return out, nil
		}
		return out, initialReviewBlocked(out.Status)
	}
	if req.Decision == "cancel" {
		initialReviewTransition(ctx, &out, "cancelled", req.Reason, now)
		if err = initialReviewSaveTx(ctx, q, tx, out); err != nil {
			return out, err
		}
		if err = appendRecoveryHistoryEvent(ctx, q, tx, app, initialLifecycleHistory(out)); err != nil {
			return out, err
		}
		return out, nil
	}

	preview, previewErr := previewWorkflowInitialRecoveryTx(ctx, q, tx, account, app, out.Failure.ID, now)
	observed, err := initialReviewObserved(out, p, preview, previewErr, now)
	if err != nil {
		return out, err
	}
	if observed.Status != "prepared" {
		initialReviewTransition(ctx, &out, observed.Status, observed.Reason, now)
	} else if req.Decision == "cancel" {
		initialReviewTransition(ctx, &out, "cancelled", req.Reason, now)
	} else {
		if observed.Reason != "eligible" {
			return out, initialReviewBlocked(observed.Reason)
		}
		if p.Policy.Mode == "automatic_initial" {
			reason, err := initialAutomaticSubmissionReasonTx(ctx, q, tx, app, p, now)
			if err != nil {
				return out, err
			}
			if reason != "" {
				return out, initialReviewBlocked(reason)
			}
		}
		if p.Policy.Mode != "automatic_initial" {
			b, err := q.WorkflowInitialReviewBudget(ctx, tx, sqlc.WorkflowInitialReviewBudgetParams{AppID: mustPgUUID(app), SinceAt: pgtypeFromTime(now.Add(-time.Hour))})
			if err != nil {
				return out, fmt.Errorf("read initial submission budget: %w", err)
			}
			if reason := initialReviewBudgetReason(int(b.Submitted), timeFromPgtype(b.LastSubmitted), p.Policy, now); reason != "" {
				return out, initialReviewBlocked(reason)
			}
		}
		job, err := createEventRecoveryTx(ctx, tx, account, app, initialReviewJobRequest(out, req.Reason))
		if err != nil {
			return out, err
		}
		out.JobID = job.ID
		out.SubmittedAt = &now
		initialReviewTransition(ctx, &out, "submitted", req.Reason, now)
	}
	if err = initialReviewSaveTx(ctx, q, tx, out); err != nil {
		return out, err
	}
	if err = appendRecoveryHistoryEvent(ctx, q, tx, app, initialLifecycleHistory(out)); err != nil {
		return out, err
	}
	return out, nil
}
func captureInitialReviewNotificationTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, account string, r *api.WorkflowInitialRecoveryReview, now time.Time) error {
	ids, err := q.WorkflowRecoveryReviewNotificationReceivers(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationReceiversParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(r.AppID), Event: string(AppWebhookEventRecoveryReviewReady)})
	if err != nil {
		return fmt.Errorf("capture initial review receivers: %w", err)
	}
	receivers := []string{}
	for _, id := range ids {
		receivers = append(receivers, uuidString(id))
	}
	r.Notification = newRecoveryReviewNotification(r.ID, "ready", true, receivers, now)
	if len(ids) == 0 {
		return nil
	}
	raw, err := json.Marshal(initialReviewReadyPayload(*r, now))
	if err != nil {
		return fmt.Errorf("encode initial review notification: %w", err)
	}
	if err = q.WorkflowRecoveryReviewNotificationEnqueue(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationEnqueueParams{EventID: mustPgUUID(r.Notification.EventID), Event: string(AppWebhookEventRecoveryReviewReady), AccountID: mustPgUUID(account), AppID: mustPgUUID(r.AppID), ReviewID: mustPgUUID(r.ID), Payload: raw, ReceiverIds: ids, NowAt: pgtypeFromTime(now)}); err != nil {
		return fmt.Errorf("enqueue initial review notification: %w", err)
	}
	return nil
}

func initialReviewSubmittedTime(at *time.Time) pgtype.Timestamptz {
	if at == nil {
		return pgtype.Timestamptz{}
	}
	return pgtypeFromTime(*at)
}
func initialReviewJobAdmissionTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, job string, now time.Time) (string, error) {
	row, err := q.WorkflowInitialReviewAdmission(ctx, tx, mustPgUUID(job))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read initial recovery admission authority: %w", err)
	}
	return initialReviewAuthorityReason(row.Review, row.Policy, row.Version, row.Enabled, timestamptzToTimePtr(row.AutomaticPausedAt), now)
}
func (s *PgStore) ListWorkflowInitialRecoveryReviews(ctx context.Context, account, app, after string, limit int) (api.WorkflowInitialRecoveryReviews, error) {
	out := api.WorkflowInitialRecoveryReviews{Reviews: []api.WorkflowInitialRecoveryReview{}}
	if err := validRecoveryPolicyReviewQuery(account, app, after, limit); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.initialFailureReadTx(ctx, account, app)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	row, err := q.WorkflowRecoveryPolicyGet(ctx, tx, sqlc.WorkflowRecoveryPolicyGetParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account)})
	p := defaultWorkflowRecoveryPolicy(canonicalMemUUID(app))
	if err == nil {
		p, err = pgPolicyResponse(uuidString(row.AppID), row.Version, row.Policy, row.UpdatedAt, row.ActorKind, row.ActorID, row.LastEvaluatedAt, row.Status, row.AutomaticFailures, row.AutomaticPausedAt, row.AutomaticPauseReason, row.AutomaticPollFailure, row.AutomaticPollNotification)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, fmt.Errorf("read initial review list policy: %w", err)
	}
	rows, err := q.WorkflowInitialReviewList(ctx, tx, sqlc.WorkflowInitialReviewListParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account), AfterID: recoveryRequestUUID(after), RetainedAfter: pgtypeFromTime(now.Add(-api.WorkflowRecoveryHistoryRetention)), PageLimit: int32(limit + 1)})
	if err != nil {
		return out, fmt.Errorf("list initial recovery reviews: %w", err)
	}
	for _, raw := range rows {
		r, err := decodeInitialReview(raw)
		if err != nil {
			return out, err
		}
		if r.Status == "prepared" {
			preview, previewErr := previewWorkflowInitialRecoveryTx(ctx, q, tx, account, app, r.Failure.ID, now)
			r, err = initialReviewObserved(r, p, preview, previewErr, now)
			if err != nil {
				return out, err
			}
		}
		out.Reviews = append(out.Reviews, r)
	}
	if len(out.Reviews) > limit {
		out.NextAfter = out.Reviews[limit-1].ID
		out.Reviews = out.Reviews[:limit]
	}
	return out, tx.Commit(ctx)
}

func initialReviewAuthorityReason(raw, policyRaw []byte, version int64, enabled bool, paused *time.Time, now time.Time) (string, error) {
	r, err := decodeInitialReview(raw)
	if err != nil {
		return "", err
	}
	var policy api.WorkflowRecoveryPolicy
	if err = json.Unmarshal(policyRaw, &policy); err != nil {
		return "", fmt.Errorf("decode initial recovery admission policy: %w", err)
	}
	p := api.WorkflowRecoveryPolicyResponse{Version: version, Policy: policy}
	p.Policy.Enabled = enabled
	p.AutomaticExecution = &api.WorkflowRecoveryPolicyAutomaticExecution{PausedAt: paused}
	return initialReviewAdmissionReason(r, p, now), nil
}

func initialReviewJobAuthority(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, job string, now time.Time) (string, error) {
	row, err := q.WorkflowInitialReviewAuthority(ctx, db, mustPgUUID(job))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read initial review authority: %w", err)
	}
	return initialReviewAuthorityReason(row.Review, row.Policy, row.Version, row.Enabled, timestamptzToTimePtr(row.AutomaticPausedAt), now)
}
