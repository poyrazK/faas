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

type projectCanaryRecord struct {
	Policy                                       ProjectCanaryPolicyState
	AccountID, ProjectID, AttemptID, WorkerAppID string
}

type ProjectCanaryStore interface {
	ProjectCanaryCheckpoint(context.Context, string, string) (ProjectCanaryPolicyState, error)
	ResolveProjectCanary(context.Context, string, string, string) (string, string, error)
}

func sameReleaseMembers(a, b []ProjectReleaseMember) bool {
	if len(a) != len(b) {
		return false
	}
	byApp := map[string]string{}
	for _, m := range a {
		byApp[m.AppID] = m.DeploymentID
	}
	if len(byApp) != len(a) {
		return false
	}
	for _, m := range b {
		if byApp[m.AppID] != m.DeploymentID {
			return false
		}
		delete(byApp, m.AppID)
	}
	return len(byApp) == 0
}

func progressiveRelease(safe *api.ProjectSafeRelease) bool {
	return safe != nil && safe.Verification != nil && safe.Verification.Progressive
}
func validProgressiveRelease(safe *api.ProjectSafeRelease) bool {
	return !progressiveRelease(safe) || safe.Configuration != nil && (safe.Worker == nil || safe.Verification.WorkerHealth != nil) && (safe.Verification.Stabilization == nil || !safe.Verification.Stabilization.Enabled || time.Duration(safe.TTLSeconds)*time.Second >= api.ProjectReleaseStabilizationMinRetention)
}

func canaryRecord(accountID, attemptID, projectID string, safe *api.ProjectSafeRelease) (projectCanaryRecord, error) {
	policy, err := NewProjectCanaryPolicy(safe.ReleaseID, safe.PreviousReleaseID, safe.ReleaseID, safe.Worker != nil, time.Now().UTC())
	worker := ""
	if safe.Worker != nil {
		worker = safe.Worker.AppID
	}
	return projectCanaryRecord{policy, accountID, projectID, attemptID, worker}, err
}

func syncCanaryVerification(safe *api.ProjectSafeRelease, p ProjectCanaryPolicyState) {
	v := safe.Verification
	if p.Control != nil {
		copy := *p.Control
		v.Control = &copy
	}
	v.Percent, v.Generation = p.Percent, p.Generation
	v.Actions = api.ProjectReleaseControlActions(safe)
	deadline := p.Deadline
	v.Deadline = &deadline
	v.CheckedAt = p.CheckedAt
	v.HealthyRounds, v.RegressedRounds = p.HealthyRounds, p.RegressedRounds
}

func (m *MemStore) stageProjectCanaryLocked(accountID, attemptID, projectID string, safe *api.ProjectSafeRelease) error {
	c, err := canaryRecord(accountID, attemptID, projectID, safe)
	if err != nil {
		return err
	}
	graph := m.projectReleaseSets[safe.ReleaseID]
	graph.Active = false
	expires := c.Policy.Deadline.Add(time.Duration(safe.TTLSeconds) * time.Second)
	graph.ExpiresAt = &expires
	m.projectReleaseSets[graph.ID] = graph
	base := m.projectReleaseSets[safe.PreviousReleaseID]
	base.Active = true
	base.ExpiresAt = nil
	m.projectReleaseSets[base.ID] = base
	m.activeProjectReleaseSets[releaseKey(projectID, "production")] = base.ID
	if m.projectCanaries == nil {
		m.projectCanaries = map[string]projectCanaryRecord{}
	}
	m.projectCanaries[accountID+"\x00"+attemptID] = c
	syncCanaryVerification(safe, c.Policy)
	return nil
}

func stageProjectCanaryDB(ctx context.Context, tx pgx.Tx, accountID, attemptID, projectID string, safe *api.ProjectSafeRelease) error {
	c, err := canaryRecord(accountID, attemptID, projectID, safe)
	if err != nil {
		return err
	}
	q := sqlc.New()
	n, err := q.RetainStagedProjectRelease(ctx, tx, sqlc.RetainStagedProjectReleaseParams{ID: mustPgUUID(safe.ReleaseID), ExpiresAt: pgtype.Timestamptz{Time: c.Policy.Deadline.Add(time.Duration(safe.TTLSeconds) * time.Second), Valid: true}})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	n, err = q.RestoreCanaryBaselineDefault(ctx, tx, mustPgUUID(safe.PreviousReleaseID))
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	raw, err := json.Marshal(c.Policy)
	if err != nil {
		return err
	}
	if err = q.InsertProjectReleaseCanary(ctx, tx, sqlc.InsertProjectReleaseCanaryParams{AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), AttemptID: attemptID, Policy: raw, WorkerAppID: mustPgUUID(c.WorkerAppID)}); err != nil {
		return err
	}
	syncCanaryVerification(safe, c.Policy)
	return nil
}

func (m *MemStore) ProjectCanaryCheckpoint(_ context.Context, accountID, attemptID string) (ProjectCanaryPolicyState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.projectCanaries[accountID+"\x00"+attemptID]
	if !ok {
		return ProjectCanaryPolicyState{}, ErrNotFound
	}
	p := c.Policy
	if p.Control != nil {
		copy := *p.Control
		p.Control = &copy
	}
	if p.CheckedAt != nil {
		t := *p.CheckedAt
		p.CheckedAt = &t
	}
	return p, p.validate()
}

func (s *PgStore) ProjectCanaryCheckpoint(ctx context.Context, accountID, attemptID string) (ProjectCanaryPolicyState, error) {
	c, err := sqlc.New().ReadProjectReleaseCanary(ctx, s.pool, sqlc.ReadProjectReleaseCanaryParams{AccountID: mustPgUUID(accountID), AttemptID: attemptID})
	if err != nil {
		return ProjectCanaryPolicyState{}, mapErr(err)
	}
	var p ProjectCanaryPolicyState
	if err = json.Unmarshal(c.Policy, &p); err != nil {
		return p, err
	}
	return p, p.validate()
}

func canaryActiveID(p ProjectCanaryPolicyState) string {
	if p.Percent < api.ProjectCanaryFinalPercent {
		return p.BaselineReleaseID
	}
	return p.CandidateReleaseID
}

func canaryObservation(safe *api.ProjectSafeRelease, p ProjectCanaryPolicyState, round *ProjectReleaseVerificationRound, ready, baselineReady bool, now time.Time) ProjectCanaryObservation {
	o := ProjectCanaryObservation{Generation: p.Generation, PreviousCheckedAt: p.CheckedAt, HTTPVerdict: "unknown", BaselineCheckedAt: now, EvidenceStartedAt: p.StageStartedAt, EvidenceEndedAt: now}
	if p.CheckedAt != nil {
		o.EvidenceStartedAt = *p.CheckedAt
	}
	if round == nil {
		return o
	}
	o.Generation, o.PreviousCheckedAt = round.Generation, round.PreviousCheckedAt
	o.HTTPVerdict = ProjectReleaseProbeVerdict(safe.Verification.Probes, round.Evidence)
	o.BaselineHealthy = len(round.Evidence) == len(safe.Verification.Probes) && len(round.Evidence) > 0 && baselineReady
	for i, e := range round.Evidence {
		if i >= len(safe.Verification.Probes) || e.Probe != safe.Verification.Probes[i] || e.StableRequests < api.RouteHealthProbeRequestsPerMinute || e.StableErrors != 0 || e.Unauthenticated != 0 || e.UnexpectedResponses != 0 {
			o.BaselineHealthy = false
		}
	}
	if !ready {
		o.HTTPVerdict = "unknown"
	}
	if h := safe.Verification.WorkerHealth; h != nil {
		o.WorkerVerdict = h.Status
		b := h.Baseline
		o.BaselineHealthy = o.BaselineHealthy && b.Attempts >= api.ProjectWorkerHealthMinAttempts && b.Incomplete == 0 && b.Failures >= 0 && b.Failures*100 <= b.Attempts*api.ProjectWorkerHealthMaxBaselineErrorPercent
	}
	return o
}

type canaryEffects struct {
	activate func() error
	restore  func() error
	retain   func(time.Time) error
}

func advanceCanaryDecision(safe *api.ProjectSafeRelease, p ProjectCanaryPolicyState, round *ProjectReleaseVerificationRound, ready, baselineReady bool, now time.Time, e canaryEffects) (ProjectCanaryPolicyState, error) {
	if p.Control != nil {
		return advanceProjectCanaryControl(safe, p, round, ready, baselineReady, now, e)
	}
	if p.Stabilizing {
		return advanceProjectStabilization(safe, p, round, ready, baselineReady, now, e)
	}
	next, effect, err := DecideProjectCanary(p, canaryObservation(safe, p, round, ready, baselineReady, now), now)
	if err != nil {
		return p, err
	}
	switch effect {
	case ProjectCanaryActivate:
		if err = e.activate(); err != nil {
			return p, err
		}
	case ProjectCanaryRestore:
		if err = e.restore(); err != nil {
			return p, err
		}
		next.Status = "rolled_back"
	case ProjectCanaryAbort:
		safe.Status, safe.RestoredReleaseID = "rolled_back", safe.PreviousReleaseID
		if safe.Configuration != nil {
			safe.Configuration.RestoredVersion = safe.Configuration.PreviousVersion
		}
	case ProjectCanaryComplete:
		safe.Status = "succeeded"
		if v := safe.Verification.Stabilization; v != nil && v.Enabled {
			next.Stabilizing = true
			next.StageStartedAt, next.Deadline = now, now.Add(api.ProjectReleaseStabilizationWindow)
			next.CheckedAt, next.HealthyRounds, next.RegressedRounds = nil, 0, 0
			started, deadline := now, next.Deadline
			safe.Verification.Stabilization = &api.ProjectReleaseStabilization{Enabled: true, Status: "monitoring", Health: "unknown", StartedAt: &started, Deadline: &deadline}
			safe.Message = "Rollout succeeded; stabilization monitoring is active."
		}
	case ProjectCanaryNeedsOperator:
		safe.Status, safe.Message = "rollback_failed", "Progressive verification stopped; baseline health could not be confirmed. Operator action is required."
	}
	if effect == ProjectCanaryPromote {
		if err = e.retain(next.Deadline.Add(time.Duration(safe.TTLSeconds) * time.Second)); err != nil {
			return p, err
		}
	}
	if next.Generation != p.Generation {
		if round != nil {
			safe.Verification.Evidence = append([]api.ProjectReleaseProbeEvidence(nil), round.Evidence...)
		}
		syncCanaryVerification(safe, next)
		if safe.Status == "verifying" {
			safe.Message = fmt.Sprintf("Progressive canary at %d%%; waiting for confirmed health.", next.Percent)
		}
		if safe.Status == "rolled_back" && safe.Worker != nil {
			safe.Worker.Status = "restored"
			safe.Worker.DeploymentID, safe.Worker.InstanceID = safe.Worker.PreviousDeploymentID, ""
		}
	}
	return next, nil
}

func conflictCanary(safe *api.ProjectSafeRelease, p ProjectCanaryPolicyState) ProjectCanaryPolicyState {
	wasMonitoring := api.ProjectReleaseMonitoring(safe)
	if v := safe.Verification.Stabilization; v != nil && v.Enabled {
		copy := *v
		copy.Status = "conflicted"
		safe.Verification.Stabilization = &copy
	}
	safe.Status, safe.Message = "conflicted", "Progressive release ownership or configuration changed; this attempt will not overwrite newer production intent."
	if safe.Worker != nil {
		safe.Worker.Status = "conflicted"
	}
	p.Generation++
	if p.Control != nil {
		copy := *p.Control
		copy.Status = "blocked"
		p.Control = &copy
	}
	p.Status = "aborted"
	if p.Percent == api.ProjectCanaryFinalPercent {
		p.Status = "needs_operator"
	}
	if wasMonitoring {
		p.Status = "complete"
	}
	syncCanaryVerification(safe, p)
	return p
}

func (m *MemStore) advanceProjectCanaryLocked(key string, r api.ProjectSubmissionReceipt, round *ProjectReleaseVerificationRound) (*api.ProjectSafeRelease, error) {
	c, ok := m.projectCanaries[key]
	if !ok {
		return nil, ErrConflict
	}
	safe := r.Apply.SafeRelease
	prior := c.Policy.Generation
	if err := c.Policy.validate(); err != nil {
		return nil, err
	}
	active := m.activeProjectReleaseSets[releaseKey(c.ProjectID, "production")]
	configuration, err := m.guardReleaseConfigurationLocked(c.AccountID, c.AttemptID, c.ProjectID, safe, c.Policy.Percent == api.ProjectCanaryFinalPercent)
	if active != canaryActiveID(c.Policy) || err != nil || m.stabilizationSupersededLocked(c) {
		c.Policy = conflictCanary(safe, c.Policy)
	} else {
		ready, err := m.guardReleaseWorkerLocked(c.AccountID, r, true)
		if err != nil {
			c.Policy = conflictCanary(safe, c.Policy)
		} else {
			baselineReady := true
			if safe.Worker != nil {
				id, err := m.releaseWorkerReadyLocked(c.AccountID, safe.Worker.AppID, safe.Worker.PreviousDeploymentID)
				if err != nil {
					return nil, err
				}
				baselineReady = id != ""
				m.readWorkerHealthLocked(safe, time.Now().UTC())
			}
			c.Policy, err = advanceCanaryDecision(safe, c.Policy, round, ready, baselineReady, time.Now().UTC(), canaryEffects{
				activate: func() error {
					graph := m.projectReleaseSets[safe.ReleaseID]
					if _, err := m.publishProjectReleaseWithIDLocked(context.Background(), c.AccountID, c.ProjectID, "production", &safe.PreviousReleaseID, nil, safe.TTLSeconds, graph.Members, graph.ID); err != nil {
						return err
					}
					safe.Configuration.TargetVersion = configuration.Previous.Version
					if safe.Configuration.PreviousHash != safe.Configuration.TargetHash {
						safe.Configuration.TargetVersion = m.appendReleaseConfigurationLocked(c.AccountID, c.ProjectID, configuration.Target, safe.Configuration.TargetHash)
					}
					return nil
				},
				restore: func() error {
					base := m.projectReleaseSets[safe.PreviousReleaseID]
					if !releasePubliclyUsable(base, time.Now()) {
						return ErrConflict
					}
					graph, err := m.publishGuardedProjectReleaseSetLocked(context.Background(), c.AccountID, c.ProjectID, "production", &safe.ReleaseID, nil, safe.TTLSeconds, base.Members)
					if err != nil {
						return err
					}
					safe.Status, safe.RestoredReleaseID = "rolled_back", graph.ID
					safe.Configuration.RestoredVersion = safe.Configuration.TargetVersion
					if safe.Configuration.PreviousHash != safe.Configuration.TargetHash {
						safe.Configuration.RestoredVersion = m.appendReleaseConfigurationLocked(c.AccountID, c.ProjectID, configuration.Previous.Values, safe.Configuration.PreviousHash)
					}
					return nil
				},
				retain: func(expires time.Time) error {
					graph := m.projectReleaseSets[safe.ReleaseID]
					graph.ExpiresAt = &expires
					m.projectReleaseSets[graph.ID] = graph
					return nil
				},
			})
			if err != nil {
				return nil, err
			}
		}
	}
	if c.Policy.Generation == prior {
		return safe, nil
	}
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	entry := m.projectSubmissions[key]
	entry.Payload = data
	m.projectSubmissions[key] = entry
	m.projectCanaries[key] = c
	return safe, nil
}

func canaryBaselineWorkerReadyDB(ctx context.Context, db sqlc.DBTX, accountID string, w *api.ProjectReleaseWorker) (bool, error) {
	if w == nil {
		return true, nil
	}
	proofs, err := sqlc.New().ReadReleaseWorkerRunningProof(ctx, db, w.PreviousDeploymentID)
	if err != nil {
		return false, err
	}
	snapshot, err := runtimeAppValuesDB(ctx, db, accountID, w.AppID, w.PreviousDeploymentID)
	if err != nil {
		return false, err
	}
	fence, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil {
		return false, err
	}
	for _, proof := range proofs {
		if proof.Scope == fence.SecretFence.Scope && proof.SecretFingerprint == fence.SecretFence.Fingerprint && proof.ConfigFingerprint == fence.Fingerprint && proof.EnvironmentID == fence.SecretFence.EnvironmentID {
			return true, nil
		}
	}
	return false, nil
}

func advanceProjectCanaryDB(ctx context.Context, tx pgx.Tx, accountID, attemptID string, r api.ProjectSubmissionReceipt, round *ProjectReleaseVerificationRound) (*api.ProjectSafeRelease, error) {
	q := sqlc.New()
	safe := r.Apply.SafeRelease
	row, err := q.ReadProjectReleaseCanary(ctx, tx, sqlc.ReadProjectReleaseCanaryParams{AccountID: mustPgUUID(accountID), AttemptID: attemptID})
	if err != nil {
		return nil, mapErr(err)
	}
	var p ProjectCanaryPolicyState
	if err = json.Unmarshal(row.Policy, &p); err != nil {
		return nil, err
	}
	if err = p.validate(); err != nil {
		return nil, err
	}
	prior := p.Generation
	active, err := q.ActiveSafeProjectReleaseID(ctx, tx, r.Apply.ProjectID)
	if err != nil && !errors.Is(mapErr(err), ErrNotFound) {
		return nil, err
	}
	configuration, err := guardReleaseConfigurationDB(ctx, tx, accountID, attemptID, r.Apply.ProjectID, safe, p.Percent == api.ProjectCanaryFinalPercent)
	if err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	superseded := false
	if p.Stabilizing {
		var supersededErr error
		superseded, supersededErr = q.ProjectStabilizationSuperseded(ctx, tx, sqlc.ProjectStabilizationSupersededParams{AccountID: mustPgUUID(accountID), AttemptID: attemptID})
		if supersededErr != nil {
			return nil, supersededErr
		}
	}
	if active != canaryActiveID(p) || err != nil || superseded {
		p = conflictCanary(safe, p)
	} else {
		ready, err := guardReleaseWorkerDB(ctx, tx, accountID, r, true)
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) {
			p = conflictCanary(safe, p)
		} else if err != nil {
			return nil, err
		} else {
			baselineReady, err := canaryBaselineWorkerReadyDB(ctx, tx, accountID, safe.Worker)
			if err != nil {
				return nil, err
			}
			if safe.Worker != nil {
				if err = readWorkerHealthDB(ctx, tx, safe, time.Now().UTC()); err != nil {
					return nil, err
				}
			}
			p, err = advanceCanaryDecision(safe, p, round, ready, baselineReady, time.Now().UTC(), canaryEffects{
				activate: func() error {
					graph, err := readProjectReleaseSetTx(ctx, tx, accountID, r.Apply.ProjectID, "production", safe.ReleaseID)
					if err != nil {
						return err
					}
					if _, err = publishProjectReleaseWithIDTx(ctx, tx, accountID, r.Apply.ProjectID, "production", &safe.PreviousReleaseID, nil, safe.TTLSeconds, graph.Members, false, nil, graph.ID); err != nil {
						return err
					}
					safe.Configuration.TargetVersion = configuration.Previous.Version
					if safe.Configuration.PreviousHash != safe.Configuration.TargetHash {
						safe.Configuration.TargetVersion, err = appendReleaseConfigurationDB(ctx, tx, accountID, r.Apply.ProjectID, configuration.Previous.Version+1, configuration.Target, safe.Configuration.TargetHash)
					}
					return err
				},
				restore: func() error {
					base, err := readProjectReleaseSetTx(ctx, tx, accountID, r.Apply.ProjectID, "production", safe.PreviousReleaseID)
					if err != nil {
						return err
					}
					if base.ExpiresAt == nil || !base.ExpiresAt.After(time.Now()) {
						return ErrConflict
					}
					graph, err := publishProjectReleaseSetTx(ctx, tx, accountID, r.Apply.ProjectID, "production", &safe.ReleaseID, nil, safe.TTLSeconds, base.Members, false, nil)
					if err != nil {
						return err
					}
					safe.Status, safe.RestoredReleaseID = "rolled_back", graph.ID
					safe.Configuration.RestoredVersion = safe.Configuration.TargetVersion
					if safe.Configuration.PreviousHash != safe.Configuration.TargetHash {
						safe.Configuration.RestoredVersion, err = appendReleaseConfigurationDB(ctx, tx, accountID, r.Apply.ProjectID, safe.Configuration.TargetVersion+1, configuration.Previous.Values, safe.Configuration.PreviousHash)
					}
					return err
				},
				retain: func(expires time.Time) error {
					n, err := q.RetainStagedProjectRelease(ctx, tx, sqlc.RetainStagedProjectReleaseParams{ID: mustPgUUID(safe.ReleaseID), ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}})
					if err != nil {
						return err
					}
					if n != 1 {
						return ErrConflict
					}
					return nil
				},
			})
			if err != nil {
				return nil, err
			}
		}
	}
	if p.Generation == prior {
		return safe, nil
	}
	return saveProjectCanaryDB(ctx, tx, accountID, attemptID, safe, p, prior)
}

func saveProjectCanaryDB(ctx context.Context, tx pgx.Tx, accountID, attemptID string, safe *api.ProjectSafeRelease, p ProjectCanaryPolicyState, prior uint64) (*api.ProjectSafeRelease, error) {
	q := sqlc.New()
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	n, err := q.SaveProjectReleaseCanary(ctx, tx, sqlc.SaveProjectReleaseCanaryParams{AccountID: mustPgUUID(accountID), AttemptID: attemptID, Policy: raw, Status: p.Status, PreviousGeneration: int64(prior)})
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, ErrConflict
	}
	raw, err = json.Marshal(safe)
	if err != nil {
		return nil, err
	}
	if err = q.FinishSafeProjectSubmission(ctx, tx, sqlc.FinishSafeProjectSubmissionParams{AccountID: accountID, AttemptID: attemptID, SafeRelease: raw}); err != nil {
		return nil, err
	}
	if safe.Worker != nil {
		if err = q.EnqueueProjectWorkerReconcile(ctx, tx, sqlc.EnqueueProjectWorkerReconcileParams{AppID: safe.Worker.AppID, DeploymentID: safe.Worker.DeploymentID}); err != nil {
			return nil, err
		}
	}
	return safe, tx.Commit(ctx)
}
