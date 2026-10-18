package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

func pgPolicyResponse(app string, version int64, raw []byte, updated pgtype.Timestamptz, actorKind, actorID string, last pgtype.Timestamptz, status string, failures int32, paused pgtype.Timestamptz, pauseReason string, pollFailure, pollNotification []byte) (api.WorkflowRecoveryPolicyResponse, error) {
	out := api.WorkflowRecoveryPolicyResponse{AppID: app, Version: version, UpdatedAt: updated.Time, ActorKind: actorKind, ActorID: actorID, LastEvaluationAt: timestamptzToTimePtr(last), Status: status}
	err := json.Unmarshal(raw, &out.Policy)
	if err != nil {
		return out, fmt.Errorf("decode recovery policy: %w", err)
	}
	if api.WorkflowRecoveryPolicyAutomaticMode(out.Policy.Mode) {
		out.AutomaticExecution = &api.WorkflowRecoveryPolicyAutomaticExecution{ConsecutiveFailures: int(failures), PausedAt: timestamptzToTimePtr(paused), PauseReason: pauseReason}
	}
	if out.AutomaticExecution != nil && len(pollFailure) > 0 {
		if err = json.Unmarshal(pollFailure, &out.AutomaticExecution.PollingFailure); err != nil {
			return out, fmt.Errorf("decode automatic polling failure: %w", err)
		}
	}
	if len(pollNotification) > 0 {
		if err = json.Unmarshal(pollNotification, &out.PollingNotification); err != nil {
			return out, fmt.Errorf("decode polling notification: %w", err)
		}
	}
	return out, nil
}
func (s *PgStore) GetWorkflowRecoveryPolicy(ctx context.Context, account, app string) (api.WorkflowRecoveryPolicyResponse, error) {
	if err := eventRecoveryIDs(account, app); err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	q := sqlc.New()
	if _, err := q.EventRecoveryApp(ctx, s.pool, sqlc.EventRecoveryAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)}); errors.Is(err, pgx.ErrNoRows) {
		return api.WorkflowRecoveryPolicyResponse{}, ErrNotFound
	} else if err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, err
	}
	row, err := q.WorkflowRecoveryPolicyGet(ctx, s.pool, sqlc.WorkflowRecoveryPolicyGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultWorkflowRecoveryPolicy(canonicalMemUUID(app)), nil
	}
	if err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, err
	}
	return pgPolicyResponse(uuidString(row.AppID), row.Version, row.Policy, row.UpdatedAt, row.ActorKind, row.ActorID, row.LastEvaluatedAt, row.Status, row.AutomaticFailures, row.AutomaticPausedAt, row.AutomaticPauseReason, row.AutomaticPollFailure, row.AutomaticPollNotification)
}
func (s *PgStore) SetWorkflowRecoveryPolicy(ctx context.Context, account, app string, req api.SetWorkflowRecoveryPolicyRequest) (api.WorkflowRecoveryPolicyResponse, error) {
	if err := eventRecoveryIDs(account, app); err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, err
	}
	if err := req.Validate(); err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, fmt.Errorf("begin recovery policy update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	finish := func(out api.WorkflowRecoveryPolicyResponse, err error) (api.WorkflowRecoveryPolicyResponse, error) {
		if err != nil {
			return out, err
		}
		if err = sqlc.New().WorkflowRecoveryAutomaticObsolete(ctx, tx, sqlc.WorkflowRecoveryAutomaticObsoleteParams{AppID: mustPgUUID(app), PolicyVersion: out.Version}); err != nil {
			return out, fmt.Errorf("suppress obsolete automatic plans: %w", err)
		}
		if err = appendRecoveryHistoryEvent(ctx, sqlc.New(), tx, app, recoveryPolicyHistoryEvent(out)); err != nil {
			return out, err
		}
		return out, tx.Commit(ctx)
	}
	q := sqlc.New()
	if _, err := q.EventRecoveryApp(ctx, tx, sqlc.EventRecoveryAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)}); errors.Is(err, pgx.ErrNoRows) {
		return api.WorkflowRecoveryPolicyResponse{}, ErrNotFound
	} else if err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, err
	}
	raw, _ := json.Marshal(req.Policy)
	actor := recoveryAuditActor(ctx, "policy_updated")
	now := pgtypeFromTime(time.Now().UTC())
	status := "disabled"
	if req.Policy.Enabled {
		status = "monitoring"
	}
	if req.ExpectedVersion > 0 {
		row, err := q.WorkflowRecoveryPolicyUpdate(ctx, tx, sqlc.WorkflowRecoveryPolicyUpdateParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account), ExpectedVersion: req.ExpectedVersion, Enabled: req.Policy.Enabled, Policy: raw, NowAt: now, ActorKind: actor.Kind, ActorID: actor.ID, Status: status})
		if errors.Is(err, pgx.ErrNoRows) {
			return api.WorkflowRecoveryPolicyResponse{}, ErrWorkflowRecoveryPolicyConflict
		}
		if err != nil {
			return api.WorkflowRecoveryPolicyResponse{}, fmt.Errorf("update recovery policy: %w", err)
		}
		return finish(pgPolicyResponse(uuidString(row.AppID), row.Version, row.Policy, row.UpdatedAt, row.ActorKind, row.ActorID, row.LastEvaluatedAt, row.Status, row.AutomaticFailures, row.AutomaticPausedAt, row.AutomaticPauseReason, row.AutomaticPollFailure, row.AutomaticPollNotification))
	}
	row, err := q.WorkflowRecoveryPolicySet(ctx, tx, sqlc.WorkflowRecoveryPolicySetParams{AppID: mustPgUUID(app), AccountID: mustPgUUID(account), ExpectedVersion: req.ExpectedVersion, Enabled: req.Policy.Enabled, Policy: raw, NowAt: now, ActorKind: actor.Kind, ActorID: actor.ID, Status: status})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.WorkflowRecoveryPolicyResponse{}, ErrWorkflowRecoveryPolicyConflict
	}
	if err != nil {
		return api.WorkflowRecoveryPolicyResponse{}, fmt.Errorf("create recovery policy: %w", err)
	}
	return finish(pgPolicyResponse(uuidString(row.AppID), row.Version, row.Policy, row.UpdatedAt, row.ActorKind, row.ActorID, row.LastEvaluatedAt, row.Status, row.AutomaticFailures, row.AutomaticPausedAt, row.AutomaticPauseReason, row.AutomaticPollFailure, row.AutomaticPollNotification))
}
func (s *PgStore) ListWorkflowRecoveryPolicyReviews(ctx context.Context, account, app, after string, limit int) (api.WorkflowRecoveryPolicyReviews, error) {
	out := api.WorkflowRecoveryPolicyReviews{Reviews: []api.WorkflowRecoveryPolicyReview{}}
	if err := validRecoveryPolicyReviewQuery(account, app, after, limit); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	q := sqlc.New()
	if _, err := q.EventRecoveryApp(ctx, s.pool, sqlc.EventRecoveryAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)}); errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	} else if err != nil {
		return out, err
	}
	rows, err := q.WorkflowRecoveryPolicyReviews(ctx, s.pool, sqlc.WorkflowRecoveryPolicyReviewsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), AfterID: recoveryRequestUUID(after), PageLimit: int32(limit + 1)})
	if err != nil {
		return out, fmt.Errorf("list recovery policy reviews: %w", err)
	}
	for i, row := range rows {
		if i == limit {
			out.NextAfter = out.Reviews[i-1].ID
			break
		}
		var review api.WorkflowRecoveryPolicyReview
		if err = json.Unmarshal(row.Review, &review); err != nil {
			return out, err
		}
		out.Reviews = append(out.Reviews, review)
	}
	return out, nil
}
func (s *PgStore) GetWorkflowRecoveryPolicyReview(ctx context.Context, account, app, id string) (api.WorkflowRecoveryPolicyReview, error) {
	var out api.WorkflowRecoveryPolicyReview
	if err := validRecoveryPolicyReviewQuery(account, app, id, 1); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	raw, err := sqlc.New().WorkflowRecoveryPolicyReviewGet(ctx, s.pool, sqlc.WorkflowRecoveryPolicyReviewGetParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), ID: mustPgUUID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, fmt.Errorf("get recovery policy review: %w", err)
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	return out, nil
}
func pgRecoveryPolicyCandidates(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account string, p api.WorkflowRecoveryPolicy, job api.EventRecoveryJob, now time.Time) ([]recoveryPolicyCandidate, error) {
	candidates := []recoveryPolicyCandidate{}
	attention, err := workflowRecoveryAttentionRead(ctx, q, db, account, job, now)
	if err != nil {
		return candidates, err
	}
	attentionByPosition := map[int64]string{}
	for _, a := range attention.Targets {
		attentionByPosition[a.Position] = a.Status
	}
	preflight := newRecoveryPreflight(job, now, now, now, 0)
	if workflowRecoveryRetryParentTerminal(job.State) {
		if err = workflowRecoveryPreflightRead(ctx, q, db, account, job, now, true, &preflight); err != nil {
			return candidates, err
		}
	}
	rowsByPosition := map[int64]api.EventRecoveryPreflightItem{}
	for _, row := range preflight.Sample {
		rowsByPosition[row.Position] = row
	}
	items, err := q.EventRecoveryItems(ctx, db, sqlc.EventRecoveryItemsParams{JobID: mustPgUUID(job.ID), PageLimit: api.AutomationRecoveryBatchMaxRuns})
	if err != nil {
		return candidates, err
	}
	observations, err := observeWorkflowRecoveryExecutions(ctx, q, db, account, job.ID, 0, job.SelectedCount, now)
	if err != nil {
		return candidates, err
	}
	for _, stored := range items {
		item := recoveryStoredItem(stored)
		if item.WorkflowRunID == "" {
			continue
		}
		var run *WorkflowRun
		row, e := q.GetNativeWorkflowRun(ctx, db, mustPgUUID(item.WorkflowRunID))
		if e == nil {
			run = workflowRunFromSQLC(row)
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return candidates, e
		}
		_, e = q.GetCustomerOperationForWorkflow(ctx, db, mustPgUUID(item.WorkflowRunID))
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return candidates, e
		}
		c, ok := recoveryPolicyCandidateForItem(p, job, item, rowsByPosition[item.Position], attentionByPosition[item.Position], run, observations[item.Position], e == nil)
		if ok {
			candidates = append(candidates, c)
		}
	}
	return candidates, nil
}
func (s *PgStore) ProcessNextWorkflowRecoveryPolicy(ctx context.Context, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrEventRecoveryQuery
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin recovery policy evaluation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	p, err := q.WorkflowRecoveryPolicyClaim(ctx, tx, pgtypeFromTime(now))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	response, err := pgPolicyResponse(uuidString(p.AppID), p.Version, p.Policy, p.UpdatedAt, p.ActorKind, p.ActorID, p.LastEvaluatedAt, p.Status, p.AutomaticFailures, p.AutomaticPausedAt, p.AutomaticPauseReason, p.AutomaticPollFailure, p.AutomaticPollNotification)
	if err != nil {
		return false, err
	}
	b, err := q.WorkflowRecoveryPolicyBudget(ctx, tx, sqlc.WorkflowRecoveryPolicyBudgetParams{AppID: p.AppID, SinceAt: pgtypeFromTime(now.Add(-time.Hour))})
	if err != nil {
		return false, err
	}
	budget := recoveryPolicyBudget{Hourly: int(b.HourlyActions), Last: b.LastPreparedAt.Time, Existing: map[string]bool{}}
	retained, err := q.WorkflowRecoveryPolicyReviewCount(ctx, tx, p.AppID)
	if err != nil {
		return false, err
	}
	jobs, err := q.WorkflowRecoveryPolicyJobs(ctx, tx, sqlc.WorkflowRecoveryPolicyJobsParams{AppID: p.AppID, AfterID: p.JobCursor, PageLimit: api.WorkflowRecoveryPolicyJobsPerPoll})
	if err != nil {
		return false, err
	}
	cursor := pgtype.UUID{}
	status := "monitoring"
	for _, id := range jobs {
		cursor = id
		job, e := getEventRecoveryMetadata(ctx, q, tx, uuidString(p.AccountID), uuidString(id))
		if e != nil {
			return false, e
		}
		if !recoveryPolicyHas(response.Policy.WorkflowNames, job.Selection.WorkflowRuns.Name) {
			continue
		}
		candidates, e := pgRecoveryPolicyCandidates(ctx, q, tx, uuidString(p.AccountID), response.Policy, job, now)
		if e != nil {
			return false, e
		}
		if len(candidates) == 0 {
			continue
		}
		runIDs := []pgtype.UUID{}
		for _, c := range candidates {
			runIDs = append(runIDs, mustPgUUID(c.Decision.RunID))
		}
		targets, e := q.WorkflowRecoveryPolicyTargets(ctx, tx, sqlc.WorkflowRecoveryPolicyTargetsParams{AppID: p.AppID, RunIds: runIDs})
		if e != nil {
			return false, e
		}
		for _, t := range targets {
			budget.Existing[recoveryPolicyTargetKey(uuidString(t.RunID), int(t.ResumeCount), t.Action)] = true
		}
		review, fingerprint := buildRecoveryPolicyReview(response, p.Slug, job, candidates, budget, now)

		_, existingErr := q.WorkflowRecoveryPolicyReviewGet(ctx, tx, sqlc.WorkflowRecoveryPolicyReviewGetParams{AccountID: p.AccountID, AppID: p.AppID, ID: mustPgUUID(review.ID)})
		if existingErr == nil {
			continue
		}
		if !errors.Is(existingErr, pgx.ErrNoRows) {
			return false, existingErr
		}
		if retained >= api.WorkflowRecoveryPolicyReviewsMax {
			status = "review_limit"
			break
		}
		raw, e := json.Marshal(review)
		if e != nil {
			return false, e
		}
		inserted, e := q.WorkflowRecoveryPolicyReviewInsert(ctx, tx, sqlc.WorkflowRecoveryPolicyReviewInsertParams{ID: mustPgUUID(review.ID), AppID: p.AppID, JobID: id, Fingerprint: fingerprint, Review: raw, NowAt: pgtypeFromTime(now)})
		if e != nil {
			return false, e
		}
		if inserted == 0 {
			continue
		}
		if e = appendRecoveryHistoryEvent(ctx, q, tx, uuidString(p.AppID), recoveryCandidateHistoryEvent(review, job.Selection.WorkflowRuns.Name)); e != nil {
			return false, e
		}
		retained++
		if review.Plan != nil {
			if e = q.WorkflowRecoveryReviewNotificationInsert(ctx, tx, sqlc.WorkflowRecoveryReviewNotificationInsertParams{ReviewID: mustPgUUID(review.ID), NowAt: pgtypeFromTime(now)}); e != nil {
				return false, e
			}
			if review.Policy.Mode == "automatic_retry" {
				for _, a := range review.Plan.Actions {
					if a.Kind != "retry" {
						continue
					}
					if e = q.WorkflowRecoveryAutomaticExecutionInsert(ctx, tx, sqlc.WorkflowRecoveryAutomaticExecutionInsertParams{ActionID: mustPgUUID(api.WorkflowRecoveryResponseActionID(*review.Plan, a)), AppID: p.AppID, ReviewID: mustPgUUID(review.ID), PolicyVersion: review.PolicyVersion, Position: a.Position}); e != nil {
						return false, e
					}
				}
			}
			budget.Last = now
			for _, d := range review.Decisions {
				if d.Outcome != "prepared" {
					continue
				}
				e = q.WorkflowRecoveryPolicyTargetInsert(ctx, tx, sqlc.WorkflowRecoveryPolicyTargetInsertParams{AppID: p.AppID, ReviewID: mustPgUUID(review.ID), RunID: mustPgUUID(d.RunID), ResumeCount: int32(d.ResumeCount), Action: d.Action, NowAt: pgtypeFromTime(now)})
				if e != nil {
					return false, e
				}
				budget.Hourly++
				budget.Existing[recoveryPolicyTargetKey(d.RunID, d.ResumeCount, d.Action)] = true
			}
		}
	}
	if len(jobs) < api.WorkflowRecoveryPolicyJobsPerPoll {
		cursor = pgtype.UUID{}
	}
	var failures []string
	if initialPolicyPreparationEnabled(response.Policy) {
		ids, e := q.WorkflowInitialRecoveryPolicyFailures(ctx, tx, sqlc.WorkflowInitialRecoveryPolicyFailuresParams{AppID: p.AppID, RetainedAfter: pgtypeFromTime(now.Add(-api.WorkflowRecoveryFailureRetention)), NowAt: pgtypeFromTime(now), Workflows: response.Policy.WorkflowNames, Statuses: response.Policy.FailureStatuses, AfterID: p.InitialFailureCursor, PolicyVersion: p.Version, PageLimit: api.WorkflowRecoveryPolicyJobsPerPoll})
		if e != nil {
			return false, fmt.Errorf("scan initial recovery failures: %w", e)
		}
		for _, id := range ids {
			failures = append(failures, uuidString(id))
		}
	}
	err = q.WorkflowRecoveryPolicyAdvance(ctx, tx, sqlc.WorkflowRecoveryPolicyAdvanceParams{AppID: p.AppID, NextAt: pgtypeFromTime(now.Add(api.WorkflowRecoveryPolicyPollInterval)), NowAt: pgtypeFromTime(now), JobCursor: cursor, InitialFailureCursor: recoveryRequestUUID(initialPolicyNextCursor(failures)), Status: status})
	if err != nil {
		return false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, prepareInitialPolicyReviews(ctx, s, uuidString(p.AccountID), uuidString(p.AppID), p.Version, failures)
}
