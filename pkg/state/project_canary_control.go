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

var ErrProjectReleaseBaselineExpired = errors.New("project rollback baseline retention expired")

type ProjectCanaryControlStore interface {
	ControlProjectCanary(context.Context, string, string, string, api.ProjectReleaseControlRequest) (*api.ProjectSafeRelease, error)
}

func prepareCanaryControl(safe *api.ProjectSafeRelease, p ProjectCanaryPolicyState, action string, req api.ProjectReleaseControlRequest) (ProjectCanaryPolicyState, bool, error) {
	if !validProgressiveRelease(safe) || !progressiveRelease(safe) || !api.ValidDeploymentAttemptID(req.RequestID) || req.ExpectedGeneration == 0 || (action != "abort" && action != "recover") {
		return p, false, ErrInvalidArgument
	}
	if err := p.validate(); err != nil {
		return p, false, err
	}
	if c := p.Control; c != nil && c.RequestID == req.RequestID {
		if c.Action != action || c.ExpectedGeneration != req.ExpectedGeneration {
			return p, false, ErrConflict
		}
		return p, false, nil
	}
	if p.Generation != req.ExpectedGeneration || p.Control != nil && p.Control.Status == "pending" {
		return p, false, ErrConflict
	}
	if action == "abort" && ((p.Status != "running" || safe.Status != "verifying") && !api.ProjectReleaseMonitoring(safe)) || action == "recover" && (p.Status != "needs_operator" || safe.Status != "rollback_failed") {
		return p, false, ErrConflict
	}
	now := time.Now().UTC()
	p.Generation++
	p.Control = &api.ProjectReleaseControl{Action: action, RequestID: req.RequestID, ExpectedGeneration: req.ExpectedGeneration, RequestedAt: now, Status: "pending"}
	p.CheckedAt, p.HealthyRounds, p.RegressedRounds = nil, 0, 0
	if p.Percent < api.ProjectCanaryFinalPercent {
		p.Status, p.Control.Status = "aborted", "restored"
		safe.Status, safe.RestoredReleaseID = "rolled_back", p.BaselineReleaseID
		safe.Configuration.RestoredVersion = safe.Configuration.PreviousVersion
		safe.Message = "Operator aborted progressive deployment; new work stays on the unchanged baseline."
		if safe.Worker != nil {
			safe.Worker.Status, safe.Worker.DeploymentID, safe.Worker.InstanceID = "restored", safe.Worker.PreviousDeploymentID, ""
		}
	} else {
		// A separate bounded decision window does not extend graph retention.
		p.Status, p.StageStartedAt, p.Deadline = "running", now, now.Add(api.ProjectReleaseVerificationTimeout)
		safe.Status = "verifying"
		safe.Message = "Operator requested baseline recovery; new unassigned work is blocked until fresh baseline health permits restoration."
	}
	if v := safe.Verification.Stabilization; v != nil && v.Enabled {
		copy := *v
		copy.Status = "needs_operator"
		safe.Verification.Stabilization = &copy
	}
	syncCanaryVerification(safe, p)
	return p, true, p.validate()
}

func advanceProjectCanaryControl(safe *api.ProjectSafeRelease, p ProjectCanaryPolicyState, round *ProjectReleaseVerificationRound, ready, baselineReady bool, now time.Time, effects canaryEffects) (ProjectCanaryPolicyState, error) {
	if p.Control.Status != "pending" || p.Status != "running" {
		return p, nil
	}
	if now.Before(p.StageStartedAt) || round == nil && now.Before(p.Deadline) || round != nil && (round.Generation != p.Generation || !sameCanaryCheckpoint(p.CheckedAt, round.PreviousCheckedAt)) {
		return p, nil
	}
	if now.Before(p.Deadline) && p.CheckedAt != nil && now.Sub(*p.CheckedAt) < api.ProjectReleaseVerificationInterval {
		return p, nil
	}
	o := canaryObservation(safe, p, round, ready, baselineReady, now)
	copy := *p.Control
	p.Control = &copy
	if now.Before(p.Deadline) && o.BaselineHealthy {
		if err := effects.restore(); err != nil {
			return p, err
		}
		p.Status, p.Control.Status = "rolled_back", "restored"
		if v := safe.Verification.Stabilization; v != nil {
			copy := *v
			copy.Status = "rolled_back"
			safe.Verification.Stabilization = &copy
		}
		safe.Message = "Operator recovery restored the retained baseline graph and configuration after fresh health verification."
		if safe.Worker != nil {
			safe.Worker.Status, safe.Worker.DeploymentID, safe.Worker.InstanceID = "restored", safe.Worker.PreviousDeploymentID, ""
		}
	} else if !now.Before(p.Deadline) {
		p.Status, p.Control.Status = "needs_operator", "blocked"
		safe.Status, safe.Message = "rollback_failed", "Operator recovery could not confirm baseline health before its deadline; new unassigned work remains blocked."
	} else {
		p.CheckedAt = &now
		safe.Message = "Operator recovery is waiting for healthy baseline probes and qualified worker delivery evidence; new unassigned work remains blocked."
	}
	p.Generation++
	if round != nil {
		safe.Verification.Evidence = append([]api.ProjectReleaseProbeEvidence(nil), round.Evidence...)
	}
	syncCanaryVerification(safe, p)
	return p, p.validate()
}

func (m *MemStore) ControlProjectCanary(_ context.Context, accountID, attemptID, action string, req api.ProjectReleaseControlRequest) (*api.ProjectSafeRelease, error) {
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
	c, ok := m.projectCanaries[key]
	if !ok {
		return nil, ErrConflict
	}
	safe, err := copyCanaryControlSafe(r.Apply.SafeRelease)
	if err != nil {
		return nil, err
	}
	p, changed, err := prepareCanaryControl(safe, c.Policy, action, req)
	if err != nil || !changed {
		return safe, err
	}
	if m.activeProjectReleaseSets[releaseKey(c.ProjectID, "production")] != canaryActiveID(c.Policy) || m.stabilizationSupersededLocked(c) {
		return nil, ErrConflict
	}
	if _, err = m.guardReleaseConfigurationLocked(accountID, attemptID, c.ProjectID, r.Apply.SafeRelease, c.Policy.Percent == api.ProjectCanaryFinalPercent); err != nil {
		return nil, err
	}
	if _, err = m.guardReleaseWorkerLocked(accountID, r, true); err != nil {
		return nil, err
	}
	if c.Policy.Percent == api.ProjectCanaryFinalPercent {
		baseline, ok := m.projectReleaseSets[c.Policy.BaselineReleaseID]
		if !ok || baseline.ExpiresAt == nil || !baseline.ExpiresAt.After(time.Now()) {
			return nil, ErrProjectReleaseBaselineExpired
		}
	}
	c.Policy = p
	r.Apply.SafeRelease = safe
	entry.Payload, err = json.Marshal(r)
	if err != nil {
		return nil, err
	}
	m.projectSubmissions[key], m.projectCanaries[key] = entry, c
	return safe, nil
}

func (s *PgStore) ControlProjectCanary(ctx context.Context, accountID, attemptID, action string, req api.ProjectReleaseControlRequest) (*api.ProjectSafeRelease, error) {
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
	if err = json.Unmarshal(entry.Payload, &r); err != nil {
		return nil, err
	}
	row, err := q.ReadProjectReleaseCanary(ctx, tx, sqlc.ReadProjectReleaseCanaryParams{AccountID: mustPgUUID(accountID), AttemptID: attemptID})
	if err != nil {
		return nil, mapErr(err)
	}
	var p ProjectCanaryPolicyState
	if err = json.Unmarshal(row.Policy, &p); err != nil {
		return nil, err
	}
	safe, err := copyCanaryControlSafe(r.Apply.SafeRelease)
	if err != nil {
		return nil, err
	}
	next, changed, err := prepareCanaryControl(safe, p, action, req)
	if err != nil || !changed {
		return safe, err
	}
	if err = lockReleaseConfigurationEnvironmentDB(ctx, tx, accountID, r.Apply.ProjectID); err != nil {
		return nil, err
	}
	if err = q.LockSafeProjectReleaseProject(ctx, tx, sqlc.LockSafeProjectReleaseProjectParams{AccountID: accountID, ProjectID: r.Apply.ProjectID}); err != nil {
		return nil, err
	}
	active, err := q.ActiveSafeProjectReleaseID(ctx, tx, r.Apply.ProjectID)
	if err != nil {
		return nil, mapErr(err)
	}
	if active != canaryActiveID(p) {
		return nil, ErrConflict
	}
	if _, err = guardReleaseConfigurationDB(ctx, tx, accountID, attemptID, r.Apply.ProjectID, r.Apply.SafeRelease, p.Percent == api.ProjectCanaryFinalPercent); err != nil {
		return nil, err
	}
	if _, err = guardReleaseWorkerDB(ctx, tx, accountID, r, true); err != nil {
		return nil, err
	}
	if p.Stabilizing {
		superseded, err := q.ProjectStabilizationSuperseded(ctx, tx, sqlc.ProjectStabilizationSupersededParams{AccountID: mustPgUUID(accountID), AttemptID: attemptID})
		if err != nil {
			return nil, err
		}
		if superseded {
			return nil, ErrConflict
		}
	}
	if p.Percent == api.ProjectCanaryFinalPercent {
		raw, err := q.ReadProjectReleaseSet(ctx, tx, sqlc.ReadProjectReleaseSetParams{AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(r.Apply.ProjectID), Environment: "production", ReleaseID: mustPgUUID(p.BaselineReleaseID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProjectReleaseBaselineExpired
		}
		if err != nil {
			return nil, err
		}
		var baseline ProjectReleaseSet
		if err := json.Unmarshal(raw, &baseline); err != nil {
			return nil, err
		}
		if baseline.ExpiresAt == nil || !baseline.ExpiresAt.After(time.Now()) {
			return nil, ErrProjectReleaseBaselineExpired
		}
	}
	return saveProjectCanaryDB(ctx, tx, accountID, attemptID, safe, next, p.Generation)
}

var _ ProjectCanaryControlStore = (*MemStore)(nil)
var _ ProjectCanaryControlStore = (*PgStore)(nil)

func copyCanaryControlSafe(safe *api.ProjectSafeRelease) (*api.ProjectSafeRelease, error) {
	raw, err := json.Marshal(safe)
	if err != nil {
		return nil, err
	}
	var copy *api.ProjectSafeRelease
	err = json.Unmarshal(raw, &copy)
	return copy, err
}
