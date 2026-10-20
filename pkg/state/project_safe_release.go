package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type SafeProjectSubmission struct{ AccountID, AttemptID string }
type ProjectSafeReleaseStore interface {
	ListPendingSafeProjectSubmissions(context.Context) ([]SafeProjectSubmission, error)
	ReconcileSafeProjectSubmission(context.Context, string, string) (*api.ProjectSafeRelease, error)
}

type precheckedReleaseMembersKey struct{}

// ContextWithPrecheckedProjectRelease records the exact candidate members
// whose artifact preflight passed before reconciliation. A reconciler carrying
// it publishes only that set: a graph that became ready after the unlocked
// read waits for its own preflight on the next pass.
func ContextWithPrecheckedProjectRelease(ctx context.Context, members []ProjectReleaseMember) context.Context {
	return context.WithValue(ctx, precheckedReleaseMembersKey{}, append([]ProjectReleaseMember{}, members...))
}

func precheckedReleaseAllows(ctx context.Context, members []ProjectReleaseMember) bool {
	checked, ok := ctx.Value(precheckedReleaseMembersKey{}).([]ProjectReleaseMember)
	return !ok || sameReleaseMembers(checked, members)
}

func ProjectSafeReleaseCandidates(r api.ProjectSubmissionReceipt, deployments map[string]Deployment) ([]ProjectReleaseMember, string, string) {
	if r.Status != "complete" || !r.SelectionComplete || len(r.Apply.Builds) == 0 {
		return nil, "failed", "Submission did not complete; production was not switched."
	}
	if time.Now().After(r.Apply.SafeRelease.Deadline) {
		return nil, "failed", "Candidate readiness deadline expired; production was not switched."
	}
	var members []ProjectReleaseMember
	pending := false
	seen := map[string]bool{}
	for _, b := range r.Apply.Builds {
		d, found := deployments[b.DeploymentID]
		if b.Error != "" || b.DeploymentID == "" || !found || d.AppID != b.AppID || seen[b.AppID] {
			return nil, "failed", "Candidate admission was incomplete; production was not switched."
		}
		seen[b.AppID] = true
		if d.Status == DeployFailed || d.Status == DeployCancelled || d.Status == DeploySuperseded {
			return nil, "failed", "A candidate failed or was cancelled; production was not switched."
		}
		if !d.TrafficPercentExplicit || d.TrafficPercent != 0 {
			return nil, "conflicted", "Candidate traffic changed; inspect the project before publishing a release."
		}
		ready := false
		var stages StageState
		if json.Unmarshal(d.StageState, &stages) == nil {
			for _, stage := range stages.History {
				if stage.Name == StageReadiness && stage.Status == "completed" {
					ready = true
				}
			}
		}
		pending = pending || d.Status != DeployLive || !ready
		members = append(members, ProjectReleaseMember{AppID: b.AppID, DeploymentID: b.DeploymentID})
	}
	if pending {
		return nil, "building", ""
	}
	sort.Slice(members, func(i, j int) bool { return members[i].AppID < members[j].AppID })
	return members, "activated", ""
}

func (m *MemStore) ListPendingSafeProjectSubmissions(_ context.Context) ([]SafeProjectSubmission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SafeProjectSubmission
	for key, entry := range m.projectSubmissions {
		var r api.ProjectSubmissionReceipt
		if json.Unmarshal(entry.Payload, &r) != nil || r.Apply.SafeRelease == nil || (!pendingSafeProjectRelease(r.Apply.SafeRelease.Status) && !api.ProjectReleaseMonitoring(r.Apply.SafeRelease)) || entry.Status == "processing" && time.Since(entry.UpdatedAt) < time.Minute {
			continue
		}
		// Keys contain exactly account ID, NUL, attempt ID.
		for i, c := range key {
			if c == 0 {
				out = append(out, SafeProjectSubmission{key[:i], key[i+1:]})
				break
			}
		}
	}
	return out, nil
}
func (m *MemStore) ReconcileSafeProjectSubmission(ctx context.Context, accountID, attemptID string) (*api.ProjectSafeRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := accountID + "\x00" + attemptID
	entry, found := m.projectSubmissions[key]
	if !found {
		return nil, ErrNotFound
	}
	var r api.ProjectSubmissionReceipt
	if err := json.Unmarshal(entry.Payload, &r); err != nil {
		return nil, err
	}
	safe := r.Apply.SafeRelease
	if safe == nil || safe.Status != "building" {
		return safe, nil
	}
	if entry.Status == "processing" && time.Since(entry.UpdatedAt) < time.Minute {
		return safe, nil
	}
	r.Status = entry.Status
	members, status, message := ProjectSafeReleaseCandidates(r, m.deployments)
	if safe.ReleaseCommand != nil || status == "activated" {
		commandStatus, commandMessage := projectReleaseCommandOutcome(accountID, r, m.deployments, m.projectReleaseCommandTasksLocked(r))
		if commandStatus != "" && (status == "activated" || status == "building" || commandStatus == "failed" || commandStatus == "conflicted") {
			status, message = commandStatus, commandMessage
		}
	}
	if safe.ReleaseCommand != nil && safe.ReleaseCommand.VerifyBaseline && (status == "building" || status == "activated") && m.activeProjectReleaseSets[releaseKey(r.Apply.ProjectID, "production")] != safe.PreviousReleaseID {
		status, message = "conflicted", "Production changed while checking migration compatibility; this attempt did not switch production."
	}
	if safe.Worker != nil && (status == "building" || status == "activated") {
		ready, workerErr := m.guardReleaseWorkerLocked(accountID, r, false)
		if workerErr != nil {
			status, message = "conflicted", "Worker binding or baseline changed; production was not switched."
		} else if !ready {
			status = "building"
		}
	}
	if status == "building" && safe.ReleaseCommand == nil && safe.Worker == nil {
		return safe, nil
	}
	var configuration projectReleaseConfigCapture
	if status == "activated" && safe.Configuration != nil {
		var err error
		configuration, err = m.guardReleaseConfigurationLocked(accountID, attemptID, r.Apply.ProjectID, safe, false)
		if err != nil {
			status, message = "conflicted", "Release configuration changed; production was not switched."
		}
	}
	if status == "activated" {
		if !precheckedReleaseAllows(ctx, members) {
			return safe, nil
		}
		if !validProgressiveRelease(safe) {
			return nil, ErrConflict
		}
		if progressiveRelease(safe) {
			for _, c := range m.projectCanaries {
				if c.ProjectID == r.Apply.ProjectID && c.Policy.Status == "running" {
					return nil, ErrConflict
				}
			}
		}
		release, err := m.publishSafeProjectReleaseLocked(ctx, accountID, attemptID, r.Apply.ProjectID, safe, members, false)
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
			status, message = "conflicted", "The active release or project membership changed; production was not switched by this attempt."
		} else if err != nil {
			return nil, err
		} else {
			safe.ReleaseID = release.ID
			if progressiveRelease(safe) {
				if err := m.stageProjectCanaryLocked(accountID, attemptID, r.Apply.ProjectID, safe); err != nil {
					return nil, err
				}
			}
			if safe.Worker != nil {
				safe.Worker.Status = "active"
			}
			if safe.Configuration != nil {
				safe.Configuration.TargetVersion = configuration.Previous.Version
				if !progressiveRelease(safe) && safe.Configuration.PreviousHash != safe.Configuration.TargetHash {
					safe.Configuration.TargetVersion = m.appendReleaseConfigurationLocked(accountID, r.Apply.ProjectID, configuration.Target, safe.Configuration.TargetHash)
				}
			}
			if safe.Verification != nil {
				status = "verifying"
				if !progressiveRelease(safe) {
					deadline := time.Now().UTC().Add(api.ProjectReleaseVerificationTimeout)
					safe.Verification.Deadline = &deadline
				}
				if safe.Worker != nil && safe.Verification.WorkerHealth != nil {
					health := safe.Verification.WorkerHealth
					health.BaselinePendingJobs, health.BaselineOldestPendingAgeMS = m.workerHealthBacklogLocked(safe.Worker, time.Now().UTC())
				}
			}
		}
	}
	safe.Status, safe.Message = status, message
	if safe.Worker != nil && (status == "failed" || status == "conflicted") {
		safe.Worker.Status = status
	}
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	entry.Payload = data
	if entry.Status == "processing" {
		entry.Status = "interrupted"
	}
	m.projectSubmissions[key] = entry
	return safe, nil
}

func (s *PgStore) ListPendingSafeProjectSubmissions(ctx context.Context) ([]SafeProjectSubmission, error) {
	rows, err := sqlc.New().ListPendingSafeProjectSubmissions(ctx, s.pool)
	out := make([]SafeProjectSubmission, 0, len(rows))
	for _, row := range rows {
		out = append(out, SafeProjectSubmission{row.AccountID, row.AttemptID})
	}
	return out, err
}
func (s *PgStore) ReconcileSafeProjectSubmission(ctx context.Context, accountID, attemptID string) (*api.ProjectSafeRelease, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	entry, err := q.LockSafeProjectSubmission(ctx, tx, sqlc.LockSafeProjectSubmissionParams{AccountID: accountID, AttemptID: attemptID})
	if err != nil {
		return nil, mapErr(err)
	}
	var r api.ProjectSubmissionReceipt
	if err := json.Unmarshal(entry.Payload, &r); err != nil {
		return nil, err
	}
	safe := r.Apply.SafeRelease
	if safe == nil || safe.Status != "building" {
		return safe, nil
	}
	if entry.Status == "processing" && time.Since(entry.UpdatedAt.Time) < time.Minute {
		return safe, nil
	}
	r.Status = entry.Status
	if safe.Configuration != nil {
		if err := lockReleaseConfigurationEnvironmentDB(ctx, tx, accountID, r.Apply.ProjectID); err != nil {
			return nil, err
		}
	}
	if err := q.LockSafeProjectReleaseProject(ctx, tx, sqlc.LockSafeProjectReleaseProjectParams{ProjectID: r.Apply.ProjectID, AccountID: accountID}); err != nil {
		return nil, err
	}
	if err := q.LockSafeProjectReleaseApps(ctx, tx, r.Apply.ProjectID); err != nil {
		return nil, err
	}
	deployments := map[string]Deployment{}
	builds := append([]api.AppliedBuild(nil), r.Apply.Builds...)
	sort.Slice(builds, func(i, j int) bool { return builds[i].AppID < builds[j].AppID })
	for _, b := range builds {
		if b.DeploymentID == "" {
			continue
		}
		row, err := q.LockSafeProjectReleaseCandidate(ctx, tx, b.DeploymentID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			if errors.Is(mapErr(err), ErrNotFound) {
				continue
			}
			return nil, err
		}
		deployments[b.DeploymentID] = Deployment{AppID: row.AppID, Status: DeploymentStatus(row.Status), TrafficPercent: int(row.TrafficPercent), TrafficPercentExplicit: row.TrafficPercentExplicit, StageState: row.StageState, Scope: row.Scope, RootfsKey: row.RootfsKey, ImageDigest: row.ImageDigest, ReleaseCommand: row.ReleaseCommand, ReleaseCommandShell: row.ReleaseCommandShell}
	}
	members, status, message := ProjectSafeReleaseCandidates(r, deployments)
	if safe.ReleaseCommand != nil || status == "activated" {
		tasks, taskErr := readProjectReleaseCommandTasks(ctx, tx, r)
		if taskErr != nil {
			return nil, taskErr
		}
		commandStatus, commandMessage := projectReleaseCommandOutcome(accountID, r, deployments, tasks)
		if commandStatus != "" && (status == "activated" || status == "building" || commandStatus == "failed" || commandStatus == "conflicted") {
			status, message = commandStatus, commandMessage
		}
	}
	if safe.ReleaseCommand != nil && safe.ReleaseCommand.VerifyBaseline && (status == "building" || status == "activated") {
		active, readErr := q.ActiveSafeProjectReleaseID(ctx, tx, r.Apply.ProjectID)
		if readErr != nil {
			return nil, readErr
		}
		if active != safe.PreviousReleaseID {
			status, message = "conflicted", "Production changed while checking migration compatibility; this attempt did not switch production."
		}
	}
	if safe.Worker != nil && (status == "building" || status == "activated") {
		ready, workerErr := guardReleaseWorkerDB(ctx, tx, accountID, r, false)
		if errors.Is(workerErr, ErrConflict) || errors.Is(workerErr, ErrNotFound) {
			status, message = "conflicted", "Worker binding or baseline changed; production was not switched."
		} else if workerErr != nil {
			return nil, workerErr
		} else if !ready {
			status = "building"
		}
	}
	if status == "building" && safe.ReleaseCommand == nil && safe.Worker == nil {
		return safe, nil
	}
	var configuration projectReleaseConfigCapture
	if status == "activated" && safe.Configuration != nil {
		configuration, err = guardReleaseConfigurationDB(ctx, tx, accountID, attemptID, r.Apply.ProjectID, safe, false)
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
			status, message = "conflicted", "Release configuration changed; production was not switched."
		} else if err != nil {
			return nil, err
		}
	}
	if status == "activated" {
		if !precheckedReleaseAllows(ctx, members) {
			return safe, nil
		}
		// Publish and checkpoint share one transaction: a lost commit response or
		// APID restart can never publish a second graph for the same attempt.
		if !validProgressiveRelease(safe) {
			return nil, ErrConflict
		}
		release, err := publishProjectReleaseSetTx(withReleaseAdditionChange(ctx, attemptID, safe, false), tx, accountID, r.Apply.ProjectID, "production", &safe.PreviousReleaseID, nil, safe.TTLSeconds, members, false, nil)
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
			status, message = "conflicted", "The active release or project membership changed; production was not switched by this attempt."
		} else if err != nil {
			return nil, err
		} else {
			safe.ReleaseID = release.ID
			if progressiveRelease(safe) {
				if err := stageProjectCanaryDB(ctx, tx, accountID, attemptID, r.Apply.ProjectID, safe); err != nil {
					return nil, err
				}
			}
			if safe.Worker != nil {
				safe.Worker.Status = "active"
			}
			if safe.Worker != nil {
				if err := q.EnqueueProjectWorkerReconcile(ctx, tx, sqlc.EnqueueProjectWorkerReconcileParams{AppID: safe.Worker.AppID, DeploymentID: safe.Worker.DeploymentID}); err != nil {
					return nil, err
				}
			}
			if safe.Configuration != nil {
				safe.Configuration.TargetVersion = configuration.Previous.Version
				if !progressiveRelease(safe) && safe.Configuration.PreviousHash != safe.Configuration.TargetHash {
					safe.Configuration.TargetVersion, err = appendReleaseConfigurationDB(ctx, tx, accountID, r.Apply.ProjectID, configuration.Previous.Version+1, configuration.Target, safe.Configuration.TargetHash)
					if err != nil {
						return nil, err
					}
				}
			}
			if safe.Verification != nil {
				status = "verifying"
				if !progressiveRelease(safe) {
					deadline := time.Now().UTC().Add(api.ProjectReleaseVerificationTimeout)
					safe.Verification.Deadline = &deadline
				}
				if err := initializeWorkerHealthDB(ctx, tx, safe); err != nil {
					return nil, err
				}
			}
		}
	}
	safe.Status, safe.Message = status, message
	if safe.Worker != nil && (status == "failed" || status == "conflicted") {
		safe.Worker.Status = status
	}
	data, err := json.Marshal(safe)
	if err != nil {
		return nil, err
	}
	if err := q.FinishSafeProjectSubmission(ctx, tx, sqlc.FinishSafeProjectSubmissionParams{AccountID: accountID, AttemptID: attemptID, SafeRelease: data}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return safe, nil
}
