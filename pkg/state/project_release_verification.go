package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectReleaseVerificationRound is an internal, attributed probe result. The
// previous checkpoint is a CAS token so competing workers cannot count one round twice.
type ProjectReleaseVerificationRound struct {
	Generation        uint64
	PreviousCheckedAt *time.Time
	Evidence          []api.ProjectReleaseProbeEvidence
}
type ProjectReleaseVerificationStore interface {
	AdvanceProjectReleaseVerification(context.Context, string, string, *ProjectReleaseVerificationRound) (*api.ProjectSafeRelease, error)
}

func pendingSafeProjectRelease(status string) bool {
	return status == "building" || status == "verifying" || status == "rolling_back"
}

// ProjectReleaseProbeVerdict requires complete attributed samples on every
// snapshotted route. 401/403 and missing evidence are unknown, never healthy.
// A candidate 5xx is a regression only while the retained baseline is healthy.
func ProjectReleaseProbeVerdict(probes []api.ProjectReleaseProbe, evidence []api.ProjectReleaseProbeEvidence) string {
	if len(probes) == 0 || len(probes) != len(evidence) {
		return "unknown"
	}
	regressed, baselineFailed := false, false
	for i, p := range probes {
		e := evidence[i]
		if e.Probe != p || e.CandidateRequests < api.RouteHealthProbeRequestsPerMinute || e.StableRequests < api.RouteHealthProbeRequestsPerMinute || e.Unauthenticated != 0 || e.UnexpectedResponses != 0 || e.StableErrors < 0 || e.StableErrors > e.StableRequests || e.CandidateErrors < 0 || e.CandidateErrors > e.CandidateRequests {
			return "unknown"
		}
		regressed = regressed || e.CandidateErrors > 0
		baselineFailed = baselineFailed || e.StableErrors > 0
	}
	if regressed && baselineFailed {
		return "unknown"
	}
	if regressed {
		return "regressed"
	}
	return "healthy"
}
func advanceVerification(safe *api.ProjectSafeRelease, round *ProjectReleaseVerificationRound, now time.Time) bool {
	v := safe.Verification
	if safe.Status != "verifying" || v == nil || v.Deadline == nil {
		return false
	}
	if !now.Before(*v.Deadline) {
		safe.Status, safe.Message = "rolling_back", "Verification deadline expired without confirmed health; restoring the previous project release."
		if v.WorkerHealth != nil && v.WorkerHealth.Baseline.Attempts >= api.ProjectWorkerHealthMinAttempts && v.WorkerHealth.Baseline.Failures*100 > v.WorkerHealth.Baseline.Attempts*api.ProjectWorkerHealthMaxBaselineErrorPercent {
			safe.Status, safe.Message = "rollback_failed", "The retained worker baseline failed jobs; operator action is required instead of restoring a known failing release."
		}
		for _, e := range v.Evidence {
			if e.StableErrors > 0 {
				safe.Status, safe.Message = "rollback_failed", "The retained baseline also failed critical probes; operator action is required instead of restoring a known failing release."
				break
			}
		}
		return true
	}
	if round == nil || !sameVerificationTime(v.CheckedAt, round.PreviousCheckedAt) || v.CheckedAt != nil && now.Sub(*v.CheckedAt) < api.ProjectReleaseVerificationInterval {
		return false
	}
	v.CheckedAt = &now
	v.Evidence = append([]api.ProjectReleaseProbeEvidence(nil), round.Evidence...)
	verdict := ProjectReleaseProbeVerdict(v.Probes, round.Evidence)
	if v.WorkerHealth != nil {
		safe.Message = ""
		if v.WorkerHealth.Status == "insufficient_evidence" {
			safe.Message = "Worker job verification has insufficient evidence; waiting for real production deliveries."
		}
		switch v.WorkerHealth.Status {
		case "regressed":
			if verdict == "healthy" {
				verdict = "regressed"
			}
			for _, probe := range round.Evidence {
				if probe.StableErrors > 0 {
					verdict = "unknown"
					break
				}
			}
		case "healthy":
		default:
			if verdict == "healthy" {
				verdict = "unknown"
			}
		}
	}
	if v.WorkerHealth != nil && v.WorkerHealth.Baseline.Attempts >= api.ProjectWorkerHealthMinAttempts && v.WorkerHealth.Baseline.Failures*100 > v.WorkerHealth.Baseline.Attempts*api.ProjectWorkerHealthMaxBaselineErrorPercent {
		verdict = "unknown"
	}

	switch verdict {
	case "healthy":
		v.HealthyRounds++
		v.RegressedRounds = 0
	case "regressed":
		v.RegressedRounds++
		v.HealthyRounds = 0
	default:
		v.HealthyRounds, v.RegressedRounds = 0, 0
	}
	if v.HealthyRounds >= api.ProjectReleaseVerificationRounds {
		safe.Status = "succeeded"
	}
	if v.RegressedRounds >= api.ProjectReleaseVerificationRounds {
		safe.Status, safe.Message = "rolling_back", "Repeated candidate regressions with a healthy retained baseline; restoring the previous project release."
	}
	return true
}
func sameVerificationTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
func failVerificationGuard(safe *api.ProjectSafeRelease) {
	safe.Status, safe.Message = "conflicted", "The active project release changed; this attempt will not overwrite a newer release."
}
func rollbackVerificationResult(safe *api.ProjectSafeRelease, restored ProjectReleaseSet, err error) error {
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
		safe.Status, safe.Message = "rollback_failed", "The retained baseline or project membership is no longer eligible for rollback; operator action is required."
		return nil
	}
	if err != nil {
		return err
	}
	safe.Status, safe.RestoredReleaseID = "rolled_back", restored.ID
	return nil
}
func (m *MemStore) AdvanceProjectReleaseVerification(ctx context.Context, accountID, attemptID string, round *ProjectReleaseVerificationRound) (*api.ProjectSafeRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := accountID + "\x00" + attemptID
	entry, ok := m.projectSubmissions[key]
	if !ok {
		return nil, ErrNotFound
	}
	var r api.ProjectSubmissionReceipt
	if err := json.Unmarshal(entry.Payload, &r); err != nil {
		return nil, err
	}
	safe := r.Apply.SafeRelease
	if safe == nil || safe.Status != "verifying" && safe.Status != "rolling_back" && !api.ProjectReleaseMonitoring(safe) {
		return safe, nil
	}
	if progressiveRelease(safe) {
		if !validProgressiveRelease(safe) {
			return nil, ErrConflict
		}
		return m.advanceProjectCanaryLocked(ctx, key, r, round)
	}
	var configuration projectReleaseConfigCapture
	configurationConflict := false
	if safe.Configuration != nil {
		var err error
		configuration, err = m.guardReleaseConfigurationLocked(accountID, attemptID, r.Apply.ProjectID, safe, true)
		configurationConflict = err != nil
	}
	if safe.Worker != nil && m.activeProjectReleaseSets[releaseKey(r.Apply.ProjectID, "production")] == safe.ReleaseID {
		ready, workerErr := m.guardReleaseWorkerLocked(accountID, r, true)
		if workerErr != nil {
			configurationConflict = true
		} else if !ready && round != nil {
			copy := *round
			copy.Evidence = nil
			round = &copy
		}
	}
	if configurationConflict {
		safe.Status, safe.Message = "conflicted", "Release configuration changed; this attempt will not overwrite newer configuration."
	} else if m.activeProjectReleaseSets[releaseKey(r.Apply.ProjectID, "production")] != safe.ReleaseID {
		failVerificationGuard(safe)
	} else if safe.Status == "rolling_back" {
		baseline, found := m.projectReleaseSets[safe.PreviousReleaseID]
		if !found || baseline.ExpiresAt == nil || !baseline.ExpiresAt.After(time.Now()) {
			safe.Status, safe.Message = "rollback_failed", "The retained baseline expired; operator action is required."
		} else {
			restored, err := m.publishSafeProjectReleaseLocked(ctx, accountID, attemptID, r.Apply.ProjectID, safe, baseline.Members, true)
			if err := rollbackVerificationResult(safe, restored, err); err != nil {
				return nil, err
			}
			if safe.Status == "rolled_back" && safe.Worker != nil {
				safe.Worker.Status = "restored"
				safe.Worker.DeploymentID, safe.Worker.InstanceID = safe.Worker.PreviousDeploymentID, ""
			}
			if safe.Status == "rolled_back" && safe.Configuration != nil {
				safe.Configuration.RestoredVersion = safe.Configuration.TargetVersion
				if safe.Configuration.PreviousHash != safe.Configuration.TargetHash {
					safe.Configuration.RestoredVersion = m.appendReleaseConfigurationLocked(accountID, r.Apply.ProjectID, configuration.Previous.Values, safe.Configuration.PreviousHash)
				}
			}

		}
	} else if !m.advanceWorkerHealthVerificationLocked(safe, round, time.Now().UTC()) {
		return safe, nil
	}
	if safe.Worker != nil && safe.Status == "conflicted" {
		safe.Worker.Status = "conflicted"
	}
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	entry.Payload = data
	m.projectSubmissions[key] = entry
	return safe, nil
}
func (s *PgStore) AdvanceProjectReleaseVerification(ctx context.Context, accountID, attemptID string, round *ProjectReleaseVerificationRound) (*api.ProjectSafeRelease, error) {
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
	if safe == nil || safe.Status != "verifying" && safe.Status != "rolling_back" && !api.ProjectReleaseMonitoring(safe) {
		return safe, nil
	}
	if safe.Configuration != nil {
		if err := lockReleaseConfigurationEnvironmentDB(ctx, tx, accountID, r.Apply.ProjectID); err != nil {
			return nil, err
		}
	}
	if err := q.LockSafeProjectReleaseProject(ctx, tx, sqlc.LockSafeProjectReleaseProjectParams{AccountID: accountID, ProjectID: r.Apply.ProjectID}); err != nil {
		return nil, err
	}
	if progressiveRelease(safe) {
		if !validProgressiveRelease(safe) {
			return nil, ErrConflict
		}
		return advanceProjectCanaryDB(ctx, tx, accountID, attemptID, r, round)
	}
	active, err := q.ActiveSafeProjectReleaseID(ctx, tx, r.Apply.ProjectID)
	if err != nil {
		return nil, err
	}
	var configuration projectReleaseConfigCapture
	configurationConflict := false
	if safe.Configuration != nil && active == safe.ReleaseID {
		configuration, err = guardReleaseConfigurationDB(ctx, tx, accountID, attemptID, r.Apply.ProjectID, safe, true)
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
			configurationConflict = true
		} else if err != nil {
			return nil, err
		}
	}
	if safe.Worker != nil && active == safe.ReleaseID {
		ready, workerErr := guardReleaseWorkerDB(ctx, tx, accountID, r, true)
		if errors.Is(workerErr, ErrConflict) || errors.Is(workerErr, ErrNotFound) {
			configurationConflict = true
		} else if workerErr != nil {
			return nil, workerErr
		} else if !ready && round != nil {
			copy := *round
			copy.Evidence = nil
			round = &copy
		}
	}
	if configurationConflict {
		safe.Status, safe.Message = "conflicted", "Release configuration changed; this attempt will not overwrite newer configuration."
	} else if active != safe.ReleaseID {
		failVerificationGuard(safe)
	} else if safe.Status == "rolling_back" {
		baseline, err := readProjectReleaseSetTx(ctx, tx, accountID, r.Apply.ProjectID, "production", safe.PreviousReleaseID)
		if errors.Is(err, ErrNotFound) || err == nil && (baseline.ExpiresAt == nil || !baseline.ExpiresAt.After(time.Now())) {
			safe.Status, safe.Message = "rollback_failed", "The retained baseline expired; operator action is required."
		} else if err != nil {
			return nil, err
		} else {
			restored, err := publishProjectReleaseSetTx(withReleaseAdditionChange(ctx, attemptID, safe, true), tx, accountID, r.Apply.ProjectID, "production", &safe.ReleaseID, nil, safe.TTLSeconds, baseline.Members, false, nil)
			if err := rollbackVerificationResult(safe, restored, err); err != nil {
				return nil, err
			}
			if safe.Status == "rolled_back" && safe.Worker != nil {
				safe.Worker.Status = "restored"
				safe.Worker.DeploymentID, safe.Worker.InstanceID = safe.Worker.PreviousDeploymentID, ""
			}
			if safe.Status == "rolled_back" && safe.Configuration != nil {
				safe.Configuration.RestoredVersion = safe.Configuration.TargetVersion
				if safe.Configuration.PreviousHash != safe.Configuration.TargetHash {
					safe.Configuration.RestoredVersion, err = appendReleaseConfigurationDB(ctx, tx, accountID, r.Apply.ProjectID, safe.Configuration.TargetVersion+1, configuration.Previous.Values, safe.Configuration.PreviousHash)
					if err != nil {
						return nil, err
					}
				}
			}

		}
	} else {
		now := time.Now().UTC()
		if workerHealthRoundEligible(safe, round, now) {
			if err := readWorkerHealthDB(ctx, tx, safe, now); err != nil {
				return nil, err
			}
		}
		if !advanceVerification(safe, round, now) {
			return safe, nil
		}
	}
	if safe.Worker != nil && safe.Status == "conflicted" {
		safe.Worker.Status = "conflicted"
	}
	if safe.Worker != nil && safe.Status == "rolled_back" {
		if err := q.EnqueueProjectWorkerReconcile(ctx, tx, sqlc.EnqueueProjectWorkerReconcileParams{AppID: safe.Worker.AppID, DeploymentID: safe.Worker.PreviousDeploymentID}); err != nil {
			return nil, err
		}
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
