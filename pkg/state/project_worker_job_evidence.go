package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// BeginProjectWorkerJobEvidence runs after durable queue identity and target
// validation, before forwarding. A duplicate/stale lease never executes again.
func (s *PgStore) BeginProjectWorkerJobEvidence(ctx context.Context, inv Invocation) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockInvocationAttemptClaim(ctx, tx, inv.ID, InvocationClaim{Attempt: inv.Attempts, ReplayGeneration: inv.ReplayGeneration}); err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrConflict
		}
		return err
	}
	n, err := sqlc.New().BeginProjectWorkerJobEvidence(ctx, tx, sqlc.BeginProjectWorkerJobEvidenceParams{
		InvocationID: mustPgUUID(inv.ID), AppID: mustPgUUID(inv.AppID), DeploymentID: mustPgUUID(inv.ResolvedDeploymentID), InstanceID: mustPgUUID(inv.InstanceID), Attempt: int32(inv.Attempts), ReplayGeneration: inv.ReplayGeneration,
	})
	if err != nil {
		return fmt.Errorf("begin worker job evidence: %w", err)
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
func (s *PgStore) FinishProjectWorkerJobEvidence(ctx context.Context, inv Invocation, outcome string) error {
	if outcome != "succeeded" && outcome != "retry" && outcome != "dead_letter" {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockInvocationAttemptClaim(ctx, tx, inv.ID, InvocationClaim{Attempt: inv.Attempts, ReplayGeneration: inv.ReplayGeneration}); err != nil {
		if errors.Is(err, ErrNotFound) {
			return ErrConflict
		}
		return err
	}
	n, err := sqlc.New().FinishProjectWorkerJobEvidence(ctx, tx, sqlc.FinishProjectWorkerJobEvidenceParams{
		InvocationID: mustPgUUID(inv.ID), AppID: mustPgUUID(inv.AppID), DeploymentID: mustPgUUID(inv.ResolvedDeploymentID), Attempt: int32(inv.Attempts), ReplayGeneration: inv.ReplayGeneration, Outcome: outcome,
	})
	if err != nil {
		return fmt.Errorf("finish worker job evidence: %w", err)
	}
	if n != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

// ProjectWorkerJobVerdict requires enough complete, attributed baseline and
// candidate attempts. Rates use integers; retry/replay attempts cannot erase failures.
func ProjectWorkerJobVerdict(candidate, baseline api.ProjectWorkerJobSample) string {
	valid := func(s api.ProjectWorkerJobSample) bool {
		return s.Attempts >= api.ProjectWorkerHealthMinAttempts && s.Failures >= 0 && s.Failures <= s.Attempts && s.Incomplete == 0 && s.DurationMS >= 0 && s.QueueDelayMS >= 0 && s.Retries >= 0 && s.Retries <= s.Attempts
	}
	if !valid(candidate) || !valid(baseline) || baseline.Failures*100 > baseline.Attempts*api.ProjectWorkerHealthMaxBaselineErrorPercent {
		return "insufficient_evidence"
	}
	failureRate := candidate.Failures * 100 / candidate.Attempts
	baselineRate := baseline.Failures * 100 / baseline.Attempts
	retryRate := candidate.Retries * 100 / candidate.Attempts
	baselineRetries := baseline.Retries * 100 / baseline.Attempts
	slower := func(a, b int64) bool {
		return a/candidate.Attempts >= api.ProjectWorkerHealthLatencyFloorMS && a/candidate.Attempts > b/baseline.Attempts*api.ProjectWorkerHealthLatencyRatio
	}
	if candidate.Failures >= api.ProjectWorkerHealthMinFailures && failureRate >= baselineRate+api.ProjectWorkerHealthErrorMarginPercent || candidate.Retries >= api.ProjectWorkerHealthMinFailures && retryRate >= baselineRetries+api.ProjectWorkerHealthErrorMarginPercent || slower(candidate.DurationMS, baseline.DurationMS) || slower(candidate.QueueDelayMS, baseline.QueueDelayMS) {
		return "regressed"
	}
	return "healthy"
}
func workerHealthWindows(safe *api.ProjectSafeRelease) (time.Time, time.Time) {
	cutover := safe.Verification.Deadline.Add(-api.ProjectReleaseVerificationTimeout)
	start := cutover
	if safe.Verification.CheckedAt != nil && safe.Verification.CheckedAt.After(start) {
		start = *safe.Verification.CheckedAt
	}
	return start, cutover
}
func readWorkerHealthDB(ctx context.Context, db sqlc.DBTX, safe *api.ProjectSafeRelease, now time.Time) error {
	start, cutover := workerHealthWindows(safe)
	read := func(deployment string, from, to time.Time) (api.ProjectWorkerJobSample, error) {
		row, err := sqlc.New().ReadProjectWorkerJobSample(ctx, db, sqlc.ReadProjectWorkerJobSampleParams{AppID: mustPgUUID(safe.Worker.AppID), BindingID: mustPgUUID(safe.Worker.BindingID), DeploymentID: mustPgUUID(deployment), WindowStart: pgtype.Timestamptz{Time: from, Valid: true}, WindowEnd: pgtype.Timestamptz{Time: to, Valid: true}})
		return api.ProjectWorkerJobSample{Attempts: row.Attempts, Failures: row.Failures, Incomplete: row.Incomplete, DurationMS: row.DurationMs, Retries: row.Retries, QueueDelayMS: row.QueueDelayMs}, err
	}
	health := safe.Verification.WorkerHealth
	var err error
	health.Candidate, err = read(safe.Worker.DeploymentID, start, now)
	if err != nil {
		return err
	}
	health.Baseline, err = read(safe.Worker.PreviousDeploymentID, cutover.Add(-api.ProjectWorkerHealthBaselineWindow), cutover)
	if err != nil {
		return err
	}
	backlog, err := sqlc.New().CountProjectWorkerHealthPending(ctx, db, sqlc.CountProjectWorkerHealthPendingParams{AppID: mustPgUUID(safe.Worker.AppID), BindingID: mustPgUUID(safe.Worker.BindingID)})
	if err != nil {
		return err
	}
	health.PendingJobs, health.OldestPendingAgeMS = backlog.Pending, backlog.OldestAgeMs
	health.Status = projectWorkerHealthVerdict(health)
	return nil
}

type projectWorkerJobObservation struct {
	Invocation   Invocation
	BindingID    string
	StartedAt    time.Time
	FinishedAt   *time.Time
	Outcome      string
	QueueDelayMS int64
}

func workerEvidenceKey(inv Invocation) string {
	return fmt.Sprintf("%s/%d/%d", inv.ID, inv.ReplayGeneration, inv.Attempts)
}
func (m *MemStore) BeginProjectWorkerJobEvidence(_ context.Context, inv Invocation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.invocations[inv.ID]
	instance, found := m.instances[inv.InstanceID]
	binding, exists := m.queueBindings[stored.QueueBindingID]
	if !ok || !found || !exists || stored.AppID != inv.AppID || !validInvocationAttemptClaim(stored, InvocationClaim{Attempt: inv.Attempts, ReplayGeneration: inv.ReplayGeneration}) || stored.Source != InvocationQueue || stored.DeploymentScope != "production" || instance.AppID != inv.AppID || instance.DeploymentID != inv.ResolvedDeploymentID || instance.State != string(StateRunning) || instance.Mode != "worker" || binding.Mode != "push" || binding.WorkloadClass != WorkloadClassWorker || binding.DeploymentScope != "production" || !binding.Enabled || binding.RetiredAt != nil {
		return ErrConflict
	}
	if m.workerJobEvidence == nil {
		m.workerJobEvidence = map[string]projectWorkerJobObservation{}
	}
	key := workerEvidenceKey(inv)
	if _, exists := m.workerJobEvidence[key]; exists {
		return ErrConflict
	}
	now := time.Now().UTC()
	identity := Invocation{ID: inv.ID, AppID: inv.AppID, ResolvedDeploymentID: inv.ResolvedDeploymentID, Attempts: inv.Attempts, ReplayGeneration: inv.ReplayGeneration}
	m.workerJobEvidence[key] = projectWorkerJobObservation{Invocation: identity, BindingID: binding.ID, StartedAt: now, Outcome: "running", QueueDelayMS: max(0, now.Sub(stored.CreatedAt).Milliseconds())}
	return nil
}
func (m *MemStore) FinishProjectWorkerJobEvidence(_ context.Context, inv Invocation, outcome string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if outcome != "succeeded" && outcome != "retry" && outcome != "dead_letter" {
		return ErrInvalidArgument
	}
	key := workerEvidenceKey(inv)
	entry, ok := m.workerJobEvidence[key]
	if !ok || entry.Outcome != "running" || entry.Invocation.AppID != inv.AppID || entry.Invocation.ResolvedDeploymentID != inv.ResolvedDeploymentID || !validInvocationAttemptClaim(m.invocations[inv.ID], InvocationClaim{Attempt: inv.Attempts, ReplayGeneration: inv.ReplayGeneration}) {
		return ErrConflict
	}
	now := time.Now().UTC()
	entry.FinishedAt = &now
	entry.Outcome = outcome
	m.workerJobEvidence[key] = entry
	return nil
}
func workerHealthRoundEligible(safe *api.ProjectSafeRelease, round *ProjectReleaseVerificationRound, now time.Time) bool {
	v := safe.Verification
	if safe.Status != "verifying" || safe.Worker == nil || v == nil || v.WorkerHealth == nil || v.Deadline == nil {
		return false
	}
	// Refresh the baseline on deadline recovery, including after controller downtime.
	if !now.Before(*v.Deadline) {
		return true
	}
	return round != nil && sameVerificationTime(v.CheckedAt, round.PreviousCheckedAt) && (v.CheckedAt == nil || now.Sub(*v.CheckedAt) >= api.ProjectReleaseVerificationInterval)
}
func (m *MemStore) advanceWorkerHealthVerificationLocked(safe *api.ProjectSafeRelease, round *ProjectReleaseVerificationRound, now time.Time) bool {
	if workerHealthRoundEligible(safe, round, now) {
		m.readWorkerHealthLocked(safe, now)
	}
	return advanceVerification(safe, round, now)
}

func (m *MemStore) readWorkerHealthLocked(safe *api.ProjectSafeRelease, now time.Time) {
	start, cutover := workerHealthWindows(safe)
	health := safe.Verification.WorkerHealth
	read := func(deployment string, from, to time.Time) api.ProjectWorkerJobSample {
		var sample api.ProjectWorkerJobSample
		for _, e := range m.workerJobEvidence {
			if e.Invocation.AppID != safe.Worker.AppID || e.BindingID != safe.Worker.BindingID || e.Invocation.ResolvedDeploymentID != deployment || e.StartedAt.Before(from) || !e.StartedAt.Before(to) {
				continue
			}
			sample.Attempts++
			sample.QueueDelayMS += e.QueueDelayMS
			if e.Outcome == "retry" {
				sample.Retries++
			}
			if e.FinishedAt == nil {
				sample.Incomplete++
			} else {
				sample.DurationMS += e.FinishedAt.Sub(e.StartedAt).Milliseconds()
			}
			if e.Outcome == "retry" || e.Outcome == "dead_letter" {
				sample.Failures++
			}
		}
		return sample
	}
	health.Candidate = read(safe.Worker.DeploymentID, start, now)
	health.Baseline = read(safe.Worker.PreviousDeploymentID, cutover.Add(-api.ProjectWorkerHealthBaselineWindow), cutover)
	health.PendingJobs, health.OldestPendingAgeMS = m.workerHealthBacklogLocked(safe.Worker, now)
	health.Status = projectWorkerHealthVerdict(health)
}

func projectWorkerHealthVerdict(health *api.ProjectWorkerHealth) string {
	verdict := ProjectWorkerJobVerdict(health.Candidate, health.Baseline)
	if verdict == "healthy" && health.PendingJobs >= health.BaselinePendingJobs+api.ProjectWorkerHealthBacklogIncrease && health.OldestPendingAgeMS >= health.BaselineOldestPendingAgeMS+api.ProjectWorkerHealthBacklogAgeIncreaseMS {
		return "regressed"
	}
	return verdict
}
func (m *MemStore) workerHealthBacklogLocked(worker *api.ProjectReleaseWorker, now time.Time) (int64, int64) {
	var count, age int64
	for _, inv := range m.invocations {
		if inv.AppID == worker.AppID && inv.QueueBindingID == worker.BindingID && inv.DeploymentScope == "production" && inv.Source == InvocationQueue && inv.State == InvocationPending && !inv.DueAt.After(now) {
			count++
			age = max(age, now.Sub(inv.CreatedAt).Milliseconds())
		}
	}
	return count, age
}
func initializeWorkerHealthDB(ctx context.Context, db sqlc.DBTX, safe *api.ProjectSafeRelease) error {
	if safe.Worker == nil || safe.Verification == nil || safe.Verification.WorkerHealth == nil {
		return nil
	}
	backlog, err := sqlc.New().CountProjectWorkerHealthPending(ctx, db, sqlc.CountProjectWorkerHealthPendingParams{AppID: mustPgUUID(safe.Worker.AppID), BindingID: mustPgUUID(safe.Worker.BindingID)})
	if err != nil {
		return err
	}
	safe.Verification.WorkerHealth.BaselinePendingJobs = backlog.Pending
	safe.Verification.WorkerHealth.BaselineOldestPendingAgeMS = backlog.OldestAgeMs
	return nil
}
