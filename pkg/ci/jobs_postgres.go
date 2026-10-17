package ci

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci/db/sqlc"
)

var _ JobsStore = (*PostgresStore)(nil)

// JobsRunOwned prevents the ordinary Jobs replay API from creating a second
// execution of a CI command with the first attempt's source/output identity.
func (s *PostgresStore) JobsRunOwned(ctx context.Context, account, runID string) (bool, error) {
	owned, err := s.q.CIJobsRunOwned(ctx, s.pool, sqlc.CIJobsRunOwnedParams{AccountID: dbUUID(account), RunID: dbUUID(runID)})
	return owned, storeError(err)
}

// JobsRetryBlocked is a scheduler/store guard, independent of the caller's
// retry budget check. A CI attempt must never gain a second task attempt.
func (s *PostgresStore) JobsRetryBlocked(ctx context.Context, runID string) (bool, error) {
	blocked, err := s.q.CIJobsRetryBlocked(ctx, s.pool, dbUUID(runID))
	return blocked, storeError(err)
}

func (s *PostgresStore) submitJobs(ctx context.Context, account, key string, req JobsSubmitRequest, binding GitHubBinding) (Execution, bool, error) {
	tx, err := s.accountTx(ctx, account)
	if err != nil {
		return Execution{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.jobsAccount(ctx, tx, account); err != nil {
		return Execution{}, false, err
	}
	if req.rerun != nil {
		if row, replay, err := s.rerunReplay(ctx, tx, account, req.rerun.Parent.ID, key); replay || err != nil {
			return row, replay, err
		}
	}
	if err := s.jobsBinding(ctx, tx, binding); err != nil {
		return Execution{}, false, err
	}
	if err := s.jobsAutomation(ctx, tx, account, key, req, binding); err != nil {
		return Execution{}, false, err
	}
	profile, err := s.jobsProfile(ctx, tx, account, req, binding)
	if err != nil {
		return Execution{}, false, err
	}
	existing, err := s.q.GetCIExecutionByKey(ctx, tx, sqlc.GetCIExecutionByKeyParams{AccountID: dbUUID(account), IdempotencyKey: key})
	if err == nil {
		if existing.ArchivedAt.Valid {
			return Execution{}, false, ErrExecutionArchived
		}
		stored, err := executionRow(existing)
		if err != nil {
			return Execution{}, false, err
		}
		row := stored.Execution
		if !sameJobsSubmission(row, req, binding) {
			return Execution{}, false, ErrConflict
		}
		if req.automation != nil {
			if err := s.finishAutomationAdmission(ctx, tx, req.automation, row.ID, req.automationPaths); err != nil {
				return Execution{}, false, err
			}
			if err := tx.Commit(ctx); err != nil {
				return Execution{}, false, storeError(err)
			}
		}
		return row, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Execution{}, false, storeError(err)
	}
	if err := s.jobsRerunParent(ctx, tx, account, req, binding); err != nil {
		return Execution{}, false, err
	}
	proceed, err := s.automationBranchAdmission(ctx, tx, req)
	if err != nil {
		return Execution{}, false, err
	}
	if !proceed {
		if err := tx.Commit(ctx); err != nil {
			return Execution{}, false, storeError(err)
		}
		return Execution{}, false, errAutomationDeferred
	}
	if req.automationPaths != nil && req.automationPaths.Outcome == "skip" {
		if err := s.skipAutomationPaths(ctx, tx, req, binding); err != nil {
			return Execution{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Execution{}, false, storeError(err)
		}
		return Execution{}, false, errAutomationDeferred
	}
	if err := s.jobsAdmissionBudget(ctx, tx, account); err != nil {
		return Execution{}, false, err
	}
	count, err := s.q.CountCIJobsExecutions(ctx, tx, dbUUID(account))
	if err != nil {
		return Execution{}, false, storeError(err)
	}
	if count >= api.CIMaxJobsExecutionsPerAccount {
		return Execution{}, false, &LimitError{Resource: "jobs_executions", Limit: api.CIMaxJobsExecutionsPerAccount, Observed: int(count) + 1}
	}
	record, err := s.q.GetCIRevision(ctx, tx, sqlc.GetCIRevisionParams{AccountID: dbUUID(account), ID: dbUUID(req.RevisionID)})
	if err != nil {
		return Execution{}, false, storeError(err)
	}
	revision, err := revisionRow(record)
	if err != nil {
		return Execution{}, false, err
	}
	now, err := s.executionNow(ctx, tx)
	if err != nil {
		return Execution{}, false, err
	}
	row, err := newExecution(revision, req.SubmitRequest, now)
	if err != nil {
		return Execution{}, false, err
	}
	if row.Plan.Repository != binding.Repository || len(req.Templates) != len(row.Plan.Jobs) {
		return Execution{}, false, ErrConflict
	}
	row.Mode = ExecutionJobs
	row.ProfileID = req.ProfileID
	row.Automation = automationProvenance(req.automation)
	if req.rerun != nil {
		row.Rerun = rerunLineage(req.rerun.Parent)
	}
	row.Compute = &JobsExecution{Binding: clone(binding), Templates: make(map[string]JobsTemplate, len(req.Templates))}
	for _, job := range row.Plan.Jobs {
		template, err := s.jobsTemplate(ctx, tx, account, req.Templates[job.Name], job)
		if err != nil {
			return Execution{}, false, err
		}
		if profile != nil && !reflect.DeepEqual(profile.Templates[job.Name], template) {
			return Execution{}, false, ErrConflict
		}
		row.Compute.Templates[job.Name] = template
		// Refuse a plan that cannot be carried by the guest protocol before any work.
		key := AttemptKey{AccountID: row.AccountID, ExecutionID: row.ID, AttemptID: row.ID, JobName: job.Name, Fence: 1}
		placeholder := SourceArchive{Key: jobsObjectPrefix(key) + "source/" + strings.Repeat("a", 64) + ".tar.gz", Digest: "sha256:" + strings.Repeat("a", 64), Bytes: api.CIJobsSourceArchiveMaxBytes}
		if _, err := jobsManifest(StartRequest{Key: key, Repository: row.Plan.Repository, CommitSHA: row.CommitSHA, Job: job, Deadline: now}, placeholder); err != nil {
			return Execution{}, false, err
		}
	}
	if req.rerun != nil && !sameRerunConfiguration(row, req.rerun.Parent) {
		return Execution{}, false, ErrConflict
	}
	raw, err := executionJSON(row)
	if err != nil {
		return Execution{}, false, err
	}
	var parentID, rootID pgtype.UUID
	if row.Rerun != nil {
		parentID, rootID = dbUUID(row.Rerun.ParentExecutionID), dbUUID(row.Rerun.RootExecutionID)
	}
	_, err = s.q.InsertCIJobsExecution(ctx, tx, sqlc.InsertCIJobsExecutionParams{ID: dbUUID(row.ID), AccountID: dbUUID(account), RevisionID: dbUUID(row.RevisionID), CommitSha: row.CommitSHA, IdempotencyKey: key, Snapshot: raw, CreatedAt: timestamp(now), RerunParentID: parentID, RerunRootID: rootID})
	if err != nil {
		return Execution{}, false, storeError(err)
	}
	if err := s.finishAutomationAdmission(ctx, tx, req.automation, row.ID, req.automationPaths); err != nil {
		return Execution{}, false, err
	}
	if req.captureChecks {
		if err := s.q.InsertCIExecutionCheck(ctx, tx, sqlc.InsertCIExecutionCheckParams{AccountID: dbUUID(account), ExecutionID: dbUUID(row.ID)}); err != nil {
			return Execution{}, false, storeError(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Execution{}, false, storeError(err)
	}
	return row, false, nil
}

func sameJobsSubmission(row Execution, req JobsSubmitRequest, binding GitHubBinding) bool {
	var lineage *ExecutionRerun
	if req.rerun != nil {
		lineage = rerunLineage(req.rerun.Parent)
	}
	if row.Mode != ExecutionJobs || row.Compute == nil || row.ProfileID != req.ProfileID || row.RevisionID != req.RevisionID || row.CommitSHA != req.CommitSHA ||
		!sameJobsBinding(row.Compute.Binding, binding) || !reflect.DeepEqual(row.Rerun, lineage) || !reflect.DeepEqual(row.Automation, automationProvenance(req.automation)) || len(row.Compute.Templates) != len(req.Templates) {
		return false
	}
	for name, template := range row.Compute.Templates {
		if req.Templates[name] != template.ID {
			return false
		}
	}
	return true
}

func (s *PostgresStore) jobsAccount(ctx context.Context, db sqlc.DBTX, account string) error {
	value, err := s.q.CIJobsAccount(ctx, db, dbUUID(account))
	if err != nil {
		return ErrGitHubAccess
	}
	plan := api.Plan(value)
	if !plan.Valid() || !plan.JobsAllowed() || api.JobRAMMB[plan.PlanIndex()] < api.CIJobsRAMMB {
		return ErrGitHubAccess
	}
	return nil
}

func (s *PostgresStore) jobsBinding(ctx context.Context, db sqlc.DBTX, expected GitHubBinding) error {
	record, err := s.q.LockCIJobsBinding(ctx, db, sqlc.LockCIJobsBindingParams{AccountID: dbUUID(expected.AccountID), ID: dbUUID(expected.ID)})
	if err != nil {
		return storeError(err)
	}
	binding, err := bindingRow(record)
	if err != nil {
		return err
	}
	if !binding.Enabled || !sameJobsBinding(binding, expected) {
		return ErrGitHubAccess
	}
	return nil
}

func sameJobsBinding(a, b GitHubBinding) bool {
	a.CreatedAt = b.CreatedAt
	return reflect.DeepEqual(a, b)
}

func (s *PostgresStore) jobsTemplate(ctx context.Context, db sqlc.DBTX, account, id string, job PlannedJob) (JobsTemplate, error) {
	record, err := s.q.LockCIJobsTemplate(ctx, db, sqlc.LockCIJobsTemplateParams{AccountID: dbUUID(account), ID: dbUUID(id)})
	if err != nil {
		return JobsTemplate{}, storeError(err)
	}
	planName, err := s.q.CIJobsAccount(ctx, db, dbUUID(account))
	if err != nil {
		return JobsTemplate{}, ErrGitHubAccess
	}
	plan := api.Plan(planName)
	_, digest, pinned := strings.Cut(job.Image, "@")
	var env map[string]string
	if record.Kind != "batch" || record.Status != "active" || record.ImageMaterializationStatus != "ready" ||
		!pinned || record.ImageRef != job.Image || !record.ImageResolvedDigest.Valid || record.ImageResolvedDigest.String != digest ||
		!record.ImageStorageKey.Valid || record.ImageStorageKey.String == "" || record.RamMb != api.CIJobsRAMMB ||
		!plan.Valid() || !plan.JobsAllowed() || job.TimeoutSeconds > api.JobTaskTimeoutSec[plan.PlanIndex()] ||
		!slices.Equal(record.Command, []string{JobsRunnerPath, JobsRunnerProtocol}) ||
		json.Unmarshal(record.EnvOverrides, &env) != nil || len(env) != 0 || len(record.FailureRules) != 0 {
		return JobsTemplate{}, ErrConflict
	}
	return JobsTemplate{ID: uuidString(record.ID), Image: record.ImageRef, Digest: record.ImageResolvedDigest.String, StorageKey: record.ImageStorageKey.String, RAMMB: int(record.RamMb)}, nil
}

// jobsAttemptTx shares the execution lease lock with the engine. A source fetch
// outside this transaction cannot commit a late Start after a newer fence or a
// cancellation. All mutations use the lock order account -> execution -> intent.
func (s *PostgresStore) jobsAttemptTx(ctx context.Context, key AttemptKey) (pgx.Tx, jobsIntent, sqlc.CiJobAttempt, error) {
	tx, err := s.accountTx(ctx, key.AccountID)
	if err != nil {
		return nil, jobsIntent{}, sqlc.CiJobAttempt{}, err
	}
	failed := func(err error) (pgx.Tx, jobsIntent, sqlc.CiJobAttempt, error) {
		_ = tx.Rollback(ctx)
		return nil, jobsIntent{}, sqlc.CiJobAttempt{}, err
	}
	record, err := s.q.LockCIExecution(ctx, tx, sqlc.LockCIExecutionParams{AccountID: dbUUID(key.AccountID), ID: dbUUID(key.ExecutionID)})
	if err != nil {
		return failed(storeError(err))
	}
	row, err := executionRow(record)
	if err != nil {
		return failed(err)
	}
	now, err := s.executionNow(ctx, tx)
	if err != nil {
		return failed(err)
	}
	if row.Fence != key.Fence || !now.Before(row.Until) {
		return failed(ErrLeaseLost)
	}
	req, template, err := jobsAttemptFor(row.Execution, key)
	if err != nil {
		return failed(err)
	}
	if err := s.q.EnsureCIJobsAttempt(ctx, tx, sqlc.EnsureCIJobsAttemptParams{AttemptID: dbUUID(key.AttemptID), AccountID: dbUUID(key.AccountID), ExecutionID: dbUUID(key.ExecutionID), JobName: key.JobName, Fence: key.Fence}); err != nil {
		return failed(storeError(err))
	}
	attempt, err := s.q.LockCIJobsAttempt(ctx, tx, sqlc.LockCIJobsAttemptParams{AccountID: dbUUID(key.AccountID), ExecutionID: dbUUID(key.ExecutionID), AttemptID: dbUUID(key.AttemptID)})
	if err != nil {
		return failed(storeError(err))
	}
	if attempt.JobName != key.JobName || attempt.Fence > key.Fence {
		return failed(ErrLeaseLost)
	}
	if err := s.q.FenceCIJobsAttempt(ctx, tx, sqlc.FenceCIJobsAttemptParams{AttemptID: dbUUID(key.AttemptID), Fence: key.Fence}); err != nil {
		return failed(storeError(err))
	}
	intent := jobsIntent{Request: req, Template: template, Binding: row.Execution.Compute.Binding, Cancelled: attempt.CancelRequested || row.Execution.CancelRequested}
	if len(attempt.Progress) > 0 {
		progress, err := decodeJobsProgress(attempt.Progress, req.Job)
		if err != nil {
			return failed(err)
		}
		intent.Progress = &progress
	}
	for _, job := range row.Execution.Jobs {
		if job.Name == key.JobName && job.Attempt.CancelReason != "" {
			intent.Cancelled = true
		}
	}
	if attempt.RunID.Valid {
		intent.RunID = uuidString(attempt.RunID)
	}
	if len(attempt.Output) != 0 {
		if err := json.Unmarshal(attempt.Output, &intent.Output); err != nil {
			return failed(err)
		}
	}
	return tx, intent, attempt, nil
}

func (s *PostgresStore) prepareJobsAttempt(ctx context.Context, req StartRequest) (jobsIntent, error) {
	tx, intent, _, err := s.jobsAttemptTx(ctx, req.Key)
	if err != nil {
		return jobsIntent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if !sameJobsRequest(intent.Request, req) {
		return jobsIntent{}, ErrConflict
	}
	if !intent.Cancelled && intent.RunID == "" {
		if err := s.jobsAccount(ctx, tx, req.Key.AccountID); err != nil {
			return jobsIntent{}, err
		}
		if err := s.jobsBinding(ctx, tx, intent.Binding); err != nil {
			return jobsIntent{}, err
		}
	}
	return intent, storeError(tx.Commit(ctx))
}

func (s *PostgresStore) dispatchJobsAttempt(ctx context.Context, key AttemptKey, source SourceArchive) error {
	if err := validateSourceArchive(key, source); err != nil {
		return err
	}
	tx, intent, _, err := s.jobsAttemptTx(ctx, key)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if intent.Cancelled || intent.RunID != "" || intent.Output != nil {
		return storeError(tx.Commit(ctx))
	}
	if err := s.jobsAccount(ctx, tx, key.AccountID); err != nil {
		return err
	}
	if err := s.jobsBinding(ctx, tx, intent.Binding); err != nil {
		return err
	}
	current, err := s.jobsTemplate(ctx, tx, key.AccountID, intent.Template.ID, intent.Request.Job)
	if err != nil {
		return err
	}
	if current != intent.Template {
		return ErrConflict
	}
	now, err := s.executionNow(ctx, tx)
	if err != nil {
		return err
	}
	if !now.Before(intent.Request.Deadline) {
		return ErrLeaseLost
	}
	inputs, err := s.dispatchArtifactInputs(ctx, tx, intent.Request)
	if err != nil {
		return err
	}
	manifest, err := jobsManifest(intent.Request, source, inputs...)
	if err != nil {
		return err
	}
	err = s.q.InsertCIJobsRun(ctx, tx, sqlc.InsertCIJobsRunParams{ID: dbUUID(key.AttemptID), JobID: dbUUID(intent.Template.ID), AccountID: dbUUID(key.AccountID),
		TaskTimeoutS: pgtype.Int4{Int32: int32(intent.Request.Job.TimeoutSeconds), Valid: true}, Command: []string{JobsRunnerPath, JobsRunnerProtocol, base64.StdEncoding.EncodeToString(manifest)},
		ImageRefSnapshot: pgtype.Text{String: current.Image, Valid: true}, ImageResolvedDigestSnapshot: pgtype.Text{String: current.Digest, Valid: true},
		ImageStorageKeySnapshot: pgtype.Text{String: current.StorageKey, Valid: true}, RamMbSnapshot: pgtype.Int4{Int32: int32(current.RAMMB), Valid: true}, StartDeadlineAt: timestamp(intent.Request.Deadline)})
	if err != nil {
		return storeError(err)
	}
	if err := s.q.InsertCIJobsTask(ctx, tx, dbUUID(key.AttemptID)); err != nil {
		return storeError(err)
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return err
	}
	if err := s.q.DispatchCIJobsAttempt(ctx, tx, sqlc.DispatchCIJobsAttemptParams{AttemptID: dbUUID(key.AttemptID), RunID: dbUUID(key.AttemptID), Source: raw}); err != nil {
		return storeError(err)
	}
	return storeError(tx.Commit(ctx))
}

func (s *PostgresStore) cancelJobsAttempt(ctx context.Context, key AttemptKey) error {
	tx, intent, _, err := s.jobsAttemptTx(ctx, key)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := s.q.CancelCIJobsAttempt(ctx, tx, dbUUID(key.AttemptID)); err != nil {
		return storeError(err)
	}
	if intent.RunID != "" {
		// Match ordinary Jobs cancellation: task first, aggregate second.
		if err := s.q.CancelCIJobsTask(ctx, tx, dbUUID(intent.RunID)); err != nil {
			return storeError(err)
		}
		if err := s.q.CancelCIJobsRun(ctx, tx, dbUUID(intent.RunID)); err != nil {
			return storeError(err)
		}
	}
	return storeError(tx.Commit(ctx))
}

func (s *PostgresStore) observeJobsAttempt(ctx context.Context, key AttemptKey) (jobsIntent, jobsTask, error) {
	tx, intent, _, err := s.jobsAttemptTx(ctx, key)
	if err != nil {
		return jobsIntent{}, jobsTask{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task := jobsTask{}
	if intent.RunID != "" {
		runtime, runtimeErr := s.q.ReadCIJobsRuntime(ctx, tx, dbUUID(intent.RunID))
		if runtimeErr != nil && !errors.Is(runtimeErr, pgx.ErrNoRows) {
			return jobsIntent{}, task, storeError(runtimeErr)
		}
		intent.RuntimeUnsealed = runtimeErr == nil && !runtime.SealedAt.Valid
		intent.RuntimeSealed = runtimeErr == nil && runtime.SealedAt.Valid
		record, err := s.q.ReadCIJobsTask(ctx, tx, dbUUID(intent.RunID))
		if err != nil {
			return jobsIntent{}, task, storeError(err)
		}
		if record.Attempt != 1 {
			return jobsIntent{}, task, protocolError("Jobs task was retried")
		}
		task.Status, task.Stopped, task.HadInstance = record.Status, record.Stopped, record.HadInstance
		if record.ExitCode.Valid {
			value := int(record.ExitCode.Int32)
			task.ExitCode = &value
		}
	}
	return intent, task, storeError(tx.Commit(ctx))
}

func (s *PostgresStore) finishJobsOutput(ctx context.Context, key AttemptKey, output Observation) (Observation, error) {
	tx, intent, _, err := s.jobsAttemptTx(ctx, key)
	if err != nil {
		return Observation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if intent.Output != nil {
		return *intent.Output, storeError(tx.Commit(ctx))
	}
	if intent.RunID == "" {
		return Observation{}, ErrConflict
	}
	output, err = reconcileProgressOutput(intent.Progress, output)
	if err != nil {
		return Observation{}, err
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return Observation{}, err
	}
	if len(raw) > api.CIJobsOutputSnapshotMaxBytes {
		return Observation{}, protocolError("Jobs output snapshot exceeds bound")
	}
	if err := s.q.FinishCIJobsOutput(ctx, tx, sqlc.FinishCIJobsOutputParams{AttemptID: dbUUID(key.AttemptID), Output: raw}); err != nil {
		return Observation{}, storeError(err)
	}
	return output, storeError(tx.Commit(ctx))
}
