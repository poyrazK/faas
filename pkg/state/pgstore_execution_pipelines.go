package state

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"slices"
	"time"
)

var _ ExecutionPipelineStore = (*PgStore)(nil)

func pipelineJob(r sqlc.AgentExecutionWorkflow) ExecutionWorkflowJob {
	return ExecutionWorkflowJob{ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), RunsPrincipalID: executionUUIDPtr(r.RunsPrincipalID), WorkflowID: r.WorkflowID, PlanID: r.PlanID, Status: api.ManagedExecutionWorkflowStatus(r.Status), StepCount: int(r.StepCount), NextStep: int(r.NextStep), SealedPlan: r.SealedPlan, PayloadKID: r.PayloadKid, LeaseToken: executionUUIDPtr(r.LeaseToken), LeaseOwner: executionStringPtr(r.LeaseOwner), LeaseExpiresAt: timestamptzToTimePtr(r.LeaseExpiresAt), ScheduledFor: pipelineNever, LastError: r.LastError, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time, FinishedAt: timestamptzToTimePtr(r.FinishedAt)}
}
func pipelineFromSQL(r sqlc.ExecutionWorkflowPipeline) (ExecutionPipeline, error) {
	p := ExecutionPipeline{JobID: pgUUIDString(r.JobID), DeadlineAt: r.DeadlineAt.Time, DueAt: r.DueAt.Time, CancelRequested: r.CancelRequested}
	p.DeliveredAt = timestamptzToTimePtr(r.DeliveredAt)
	if err := json.Unmarshal(r.Journal, &p.Journal); err != nil {
		return p, fmt.Errorf("decode progress journal: %w", err)
	}
	if len(r.Completion) > 0 {
		if err := json.Unmarshal(r.Completion, &p.Completion); err != nil {
			return p, fmt.Errorf("decode pipeline completion: %w", err)
		}
	}
	if err := json.Unmarshal(r.Policy, &p.Policy); err != nil {
		return p, fmt.Errorf("decode pipeline policy: %w", err)
	}
	if err := validatePipelinePolicy(p.Policy, len(p.Policy.Steps)); err != nil {
		return p, err
	}
	return p, nil
}
func (s *PgStore) CreateExecutionPipeline(ctx context.Context, p CreateExecutionWorkflowJobParams, policy ExecutionPipelinePolicy) (ExecutionWorkflowJob, error) {
	if err := validateExecutionWorkflowJobParams(p); err != nil {
		return ExecutionWorkflowJob{}, err
	}
	if err := validatePipelinePolicy(policy, p.StepCount); err != nil {
		return ExecutionWorkflowJob{}, err
	}
	account, err := parsePgUUID(p.AccountID)
	if err != nil {
		return ExecutionWorkflowJob{}, err
	}
	var principal pgtype.UUID
	if p.RunsPrincipalID != nil {
		principal, err = parsePgUUID(*p.RunsPrincipalID)
		if err != nil {
			return ExecutionWorkflowJob{}, err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ExecutionWorkflowJob{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ExecutionLockAccount(ctx, tx, account); err != nil {
		return ExecutionWorkflowJob{}, mapErr(err)
	}
	if policy.Rerun != nil {
		parent, e := q.PipelineJobLock(ctx, tx, mustPgUUID(policy.Rerun.ParentID))
		if e != nil {
			return ExecutionWorkflowJob{}, mapErr(e)
		}
		row, e := q.PipelineLock(ctx, tx, parent.ID)
		if e != nil {
			return ExecutionWorkflowJob{}, mapErr(e)
		}
		meta, e := pipelineFromSQL(row)
		if e != nil {
			return ExecutionWorkflowJob{}, e
		}
		if e = validatePipelineRerun(pipelineJob(parent), meta, p, policy); e != nil {
			return ExecutionWorkflowJob{}, e
		}
	}
	if policy.Callback != nil {
		owned, e := q.RunTaskEndpointsOwned(ctx, tx, sqlc.RunTaskEndpointsOwnedParams{AccountID: account, EndpointIds: []pgtype.UUID{mustPgUUID(policy.Callback.EndpointID)}})
		if e != nil {
			return ExecutionWorkflowJob{}, e
		}
		if !owned {
			return ExecutionWorkflowJob{}, ErrNotFound
		}
		for _, step := range policy.Steps {
			row, e := q.RunTaskVersionAdmission(ctx, tx, sqlc.RunTaskVersionAdmissionParams{AccountID: account, ID: mustPgUUID(step.Task.ID)})
			if e != nil {
				return ExecutionWorkflowJob{}, ErrExecutionTaskUnavailable
			}
			v, e := taskVersionFromSQL(row)
			if e != nil {
				return ExecutionWorkflowJob{}, e
			}
			if v.Digest != step.Task.Digest || !slices.Contains(v.Metadata.AgentEndpointIDs, policy.Callback.EndpointID) {
				return ExecutionWorkflowJob{}, ErrExecutionTaskUnavailable
			}
		}
	}
	exists, err := q.PipelineJobExists(ctx, tx, sqlc.PipelineJobExistsParams{AccountID: account, WorkflowID: p.WorkflowID, PrincipalID: principal})
	if err != nil {
		return ExecutionWorkflowJob{}, err
	}
	if exists {
		return ExecutionWorkflowJob{}, ErrExecutionWorkflowJobExists
	}
	count, err := q.PipelineActiveCount(ctx, tx, account)
	if err != nil {
		return ExecutionWorkflowJob{}, err
	}
	if count >= ExecutionWorkflowManagedMaxActivePerAccount {
		return ExecutionWorkflowJob{}, ErrExecutionWorkflowQueueFull
	}
	id, err := q.PipelineJobInsert(ctx, tx, sqlc.PipelineJobInsertParams{AccountID: account, RunsPrincipalID: principal, WorkflowID: p.WorkflowID, PlanID: p.PlanID, StepCount: int16(p.StepCount), SealedPlan: p.SealedPlan, PayloadKid: p.PayloadKID, CreatedAt: executionTime(p.CreatedAt)})
	if err != nil {
		return ExecutionWorkflowJob{}, err
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		return ExecutionWorkflowJob{}, err
	}
	journal, e := initialPipelineJournal(policy, p.CreatedAt)
	if e != nil {
		return ExecutionWorkflowJob{}, e
	}
	journalRaw, e := json.Marshal(journal)
	if e != nil {
		return ExecutionWorkflowJob{}, e
	}
	if err = q.PipelineInsert(ctx, tx, sqlc.PipelineInsertParams{Journal: journalRaw, JobID: id, Policy: raw, DeadlineAt: executionTime(p.CreatedAt.Add(time.Duration(policy.TotalTimeoutMS) * time.Millisecond)), DueAt: executionTime(p.CreatedAt)}); err != nil {
		return ExecutionWorkflowJob{}, err
	}
	r, err := q.PipelineJobLock(ctx, tx, id)
	if err != nil {
		return ExecutionWorkflowJob{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return ExecutionWorkflowJob{}, err
	}
	return pipelineJob(r), nil
}
func (s *PgStore) ExecutionPipelineByID(ctx context.Context, id string) (ExecutionPipeline, error) {
	key, err := parsePgUUID(id)
	if err != nil {
		return ExecutionPipeline{}, err
	}
	r, err := sqlc.New().PipelineGet(ctx, s.pool, key)
	if err != nil {
		return ExecutionPipeline{}, mapErr(err)
	}
	return pipelineFromSQL(r)
}
func (s *PgStore) ClaimExecutionPipeline(ctx context.Context, owner string, at time.Time, lease time.Duration) (ExecutionWorkflowJobClaim, error) {
	if owner == "" || at.IsZero() || lease <= 0 {
		return ExecutionWorkflowJobClaim{}, ErrExecutionInvalid
	}
	token := uuid.NewString()
	r, err := sqlc.New().PipelineClaim(ctx, s.pool, sqlc.PipelineClaimParams{Token: mustPgUUID(token), Owner: owner, NowAt: executionTime(at), ExpiresAt: executionTime(at.Add(lease))})
	if err != nil {
		return ExecutionWorkflowJobClaim{}, mapErr(err)
	}
	return ExecutionWorkflowJobClaim{ExecutionWorkflowJob: pipelineJob(r), ClaimToken: token}, nil
}
func (s *PgStore) UpdateExecutionPipeline(ctx context.Context, id, token string, u ExecutionWorkflowJobUpdate) error {
	if u.UpdatedAt.IsZero() || u.ScheduledFor.IsZero() || len(u.LastError) > 2048 || u.NextStep < 0 || u.NextStep > api.ExecutionWorkflowManagedMaxSteps || (u.Status != api.ManagedExecutionWorkflowQueued && u.Status != api.ManagedExecutionWorkflowSucceeded && u.Status != api.ManagedExecutionWorkflowFailed) {
		return ErrExecutionInvalid
	}
	key, err := parsePgUUID(id)
	if err != nil {
		return err
	}
	lease, err := parsePgUUID(token)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	job, err := q.PipelineJobLock(ctx, tx, key)
	if err != nil {
		return mapErr(err)
	}
	if job.Status != string(api.ManagedExecutionWorkflowRunning) || !job.LeaseToken.Valid || pgUUIDString(job.LeaseToken) != token {
		return ErrExecutionWorkflowLeaseLost
	}
	metaRow, err := q.PipelineLock(ctx, tx, key)
	if err != nil {
		return mapErr(err)
	}
	meta, err := pipelineFromSQL(metaRow)
	if err != nil {
		return err
	}
	if err = validPipelineCompletion(pipelineJob(job), meta, u); err != nil {
		return err
	}
	if err = advancePipelineProgress(&meta, u.PipelineProgress, u.UpdatedAt); err != nil {
		return err
	}
	journalRaw, e := json.Marshal(meta.Journal)
	if e != nil {
		return e
	}
	if err = q.PipelineSetJournal(ctx, tx, sqlc.PipelineSetJournalParams{JobID: key, Journal: journalRaw}); err != nil {
		return err
	}
	if u.PipelineCompletion != nil {
		raw, e := json.Marshal(u.PipelineCompletion)
		if e != nil {
			return e
		}
		if err = q.PipelineSetCompletion(ctx, tx, sqlc.PipelineSetCompletionParams{JobID: key, Completion: raw}); err != nil {
			return err
		}
	}
	var finished pgtype.Timestamptz
	if u.Status == api.ManagedExecutionWorkflowSucceeded || u.Status == api.ManagedExecutionWorkflowFailed {
		finished = executionTime(u.UpdatedAt)
	}
	n, err := q.PipelineUpdateJob(ctx, tx, sqlc.PipelineUpdateJobParams{ID: key, LeaseToken: lease, Status: string(u.Status), NextStep: int16(u.NextStep), LastError: u.LastError, UpdatedAt: executionTime(u.UpdatedAt), FinishedAt: finished})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrExecutionWorkflowLeaseLost
	}
	if err = q.PipelineUpdateDue(ctx, tx, sqlc.PipelineUpdateDueParams{JobID: key, DueAt: executionTime(u.ScheduledFor)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *PgStore) CancelExecutionPipeline(ctx context.Context, account, workflow string, principal *string, at time.Time) error {
	acct, err := parsePgUUID(account)
	if err != nil {
		return err
	}
	var family pgtype.UUID
	if principal != nil {
		family, err = parsePgUUID(*principal)
		if err != nil {
			return err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ExecutionLockAccount(ctx, tx, acct); err != nil {
		return mapErr(err)
	}
	n, err := q.PipelineCancel(ctx, tx, sqlc.PipelineCancelParams{AccountID: acct, WorkflowID: workflow, PrincipalID: family, NowAt: executionTime(at)})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return tx.Commit(ctx)
}
func validatePipelineAdmissionTx(ctx context.Context, tx pgx.Tx, p CreateExecutionParams) error {
	if p.Pipeline == nil {
		return nil
	}
	id, err := parsePgUUID(p.Pipeline.JobID)
	if err != nil {
		return ErrExecutionInvalid
	}
	q := sqlc.New()
	j, err := q.PipelineJobLock(ctx, tx, id)
	if err != nil {
		return mapErr(err)
	}
	r, err := q.PipelineLock(ctx, tx, id)
	if err != nil {
		return mapErr(err)
	}
	meta, err := pipelineFromSQL(r)
	if err != nil {
		return err
	}
	var previous *Execution
	if p.Pipeline.PreviousRunID != "" {
		prevID, err := parsePgUUID(p.Pipeline.PreviousRunID)
		if err != nil {
			return ErrExecutionInvalid
		}
		r, err := q.PipelinePreviousRun(ctx, tx, prevID)
		if err != nil {
			return mapErr(err)
		}
		previous = &Execution{ID: pgUUIDString(r.ID), AccountID: pgUUIDString(r.AccountID), WorkflowID: r.WorkflowID.String, StepLabel: r.StepLabel.String, RunsPrincipalID: executionUUIDPtr(r.RunsPrincipalID), Status: api.ExecutionStatus(r.Status), FailureCode: executionStringPtr(r.FailureCode), FinishedAt: timestamptzToTimePtr(r.FinishedAt)}
	}
	now, err := q.RunControlNow(ctx, tx)
	if err != nil {
		return err
	}
	if err := validatePipelineAdmission(p, pipelineJob(j), meta, previous, now.Time); err != nil {
		return err
	}
	return q.PipelineWriterProtocol(ctx, tx)
}
