package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const MCPReadinessMonitorBatch = 4
const MCPReadinessMonitorLease = 40 * time.Second

type MCPReadinessMonitorJob struct {
	Subject    MCPReadinessSubject
	Role       api.MCPReadinessRole
	Status     api.MCPReadinessMonitorStatus
	LeaseID    string
	LeaseUntil time.Time
}
type MCPReadinessMonitorStore interface {
	ClaimMCPReadinessMonitors(context.Context) ([]MCPReadinessMonitorJob, error)
	CompleteMCPReadinessMonitor(context.Context, MCPReadinessMonitorJob, api.MCPReadinessEvidence, string) error
	ListMCPReadinessMonitoring(context.Context, string, string, string, ...string) ([]api.MCPReadinessMonitorStatus, error)
}

func monitoringEnabled(role api.MCPReadinessRole) bool {
	return role.Monitor != nil && role.Monitor.Enabled
}
func monitorStatus(subject MCPReadinessSubject, role api.MCPReadinessRole, prior api.MCPReadinessMonitorStatus) api.MCPReadinessMonitorStatus {
	if prior.PolicyRevision != subject.Policy.Revision || prior.ArtifactSHA256 != subject.ArtifactSHA256 {
		prior = api.MCPReadinessMonitorStatus{DeploymentID: subject.DeploymentID, Role: role.Name, PolicyRevision: subject.Policy.Revision, ArtifactSHA256: subject.ArtifactSHA256, NextProbeAt: subject.Policy.UpdatedAt, History: []api.MCPReadinessMonitorObservation{}}
	}
	prior.Scope = api.MCPConnectionScope(subject.Scope)
	prior.FailureThreshold = role.Monitor.FailureThreshold
	prior.OAuthConnection = role.Monitor.OAuthForScope(subject.Scope)
	prior.RecoveryAction = ""
	if role.Monitor.ManagedOAuth() {
		switch prior.Reason {
		case "oauth_reconnect_required":
			prior.RecoveryAction = "reconnect"
		case "oauth_connection_changed":
			prior.RecoveryAction = "review_binding"
		case "oauth_credential_unavailable":
			prior.RecoveryAction = "review_connection"
		}
	}
	return prior
}
func completeMonitorStatus(job MCPReadinessMonitorJob, e api.MCPReadinessEvidence, reason string, now time.Time) (api.MCPReadinessMonitorStatus, error) {
	switch reason {
	case "", "credential_unavailable", "preview_unavailable", "protocol_failure", "catalog_drift", "oauth_credential_unavailable", "oauth_reconnect_required", "oauth_connection_changed":
	default:
		return api.MCPReadinessMonitorStatus{}, ErrInvalidArgument
	}
	if e.Passed && reason != "" || !e.Passed && reason == "" {
		return api.MCPReadinessMonitorStatus{}, ErrInvalidArgument
	}
	out := job.Status
	out.NextProbeAt = now.Add(time.Duration(job.Role.Monitor.IntervalSeconds) * time.Second)
	out.Reason = reason
	if e.Passed {
		at := job.Subject.CapturedAt
		out.LastSuccessAt = &at
		out.ConsecutiveFailures = 0
	} else if out.ConsecutiveFailures < 1000000 {
		out.ConsecutiveFailures++
	}
	out.History = append(out.History, api.MCPReadinessMonitorObservation{ObservedAt: job.Subject.CapturedAt, Passed: e.Passed, Reason: reason})
	if len(out.History) > 20 {
		out.History = out.History[len(out.History)-20:]
	}
	out.State = api.ReadinessMonitorState(out, now)
	return out, nil
}
func (m *MemStore) readinessMonitorJobsLocked() map[string]MCPReadinessMonitorJob {
	r := m.readinessLocked()
	if r.monitors == nil {
		r.monitors = map[string]MCPReadinessMonitorJob{}
	}
	return r.monitors
}
func (m *MemStore) ClaimMCPReadinessMonitors(_ context.Context) ([]MCPReadinessMonitorJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	jobs := m.readinessMonitorJobsLocked()
	now := time.Now().UTC()
	out := []MCPReadinessMonitorJob{}
	ids := make([]string, 0, len(m.deployments))
	for id := range m.deployments {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		d := m.deployments[id]
		app := m.apps[d.AppID]
		if d.Status != DeployLive || d.TrafficPercent <= 0 || app.Status != AppActive || !app.AcceptsRequestInvocations() || app.PublicAuthMode == AppPublicAuthModeInternalOnly {
			continue
		}
		subject, err := m.readinessSubjectLocked(app.AccountID, id)
		if err != nil {
			continue
		}
		for _, role := range subject.Policy.Roles {
			if !monitoringEnabled(role) {
				continue
			}
			key := id + "/" + role.Name
			prior := jobs[key]
			if prior.LeaseUntil.After(now) {
				continue
			}
			status := monitorStatus(subject, role, prior.Status)
			if status.NextProbeAt.After(now) {
				continue
			}
			job := MCPReadinessMonitorJob{Subject: subject, Role: role, Status: status, LeaseID: uuid.NewString(), LeaseUntil: now.Add(MCPReadinessMonitorLease)}
			jobs[key] = cloneReadiness(job)
			out = append(out, job)
			if len(out) == MCPReadinessMonitorBatch {
				return out, nil
			}
		}
	}
	return out, nil
}
func (m *MemStore) CompleteMCPReadinessMonitor(_ context.Context, job MCPReadinessMonitorJob, e api.MCPReadinessEvidence, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	jobs := m.readinessMonitorJobsLocked()
	key := job.Subject.DeploymentID + "/" + job.Role.Name
	current, ok := jobs[key]
	if !ok || current.LeaseID != job.LeaseID || !current.LeaseUntil.After(now) {
		return ErrConflict
	}
	d := m.deployments[job.Subject.DeploymentID]
	if d.Status != DeployLive || d.TrafficPercent <= 0 {
		return ErrConflict
	}
	if !validateReadinessEvidence(current.Subject, e) || e.Role != current.Role.Name {
		return ErrInvalidArgument
	}
	status, err := completeMonitorStatus(current, e, reason, now)
	if err != nil {
		return err
	}
	if _, err = m.recordMCPReadinessLocked(current.Subject, e); err != nil {
		return err
	}
	current.Status = status
	current.LeaseID = ""
	current.LeaseUntil = time.Time{}
	jobs[key] = current
	return nil
}
func (m *MemStore) ListMCPReadinessMonitoring(_ context.Context, account, app, id string, scopes ...string) ([]api.MCPReadinessMonitorStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return nil, ErrNotFound
	}
	if len(scopes) > 0 && api.ValidateScope(readinessScope(scopes)) != nil {
		return nil, ErrInvalidArgument
	}
	out := []api.MCPReadinessMonitorStatus{}
	owner := m.apps[app]
	if owner.Status != AppActive || !owner.AcceptsRequestInvocations() || owner.PublicAuthMode == AppPublicAuthModeInternalOnly {
		return out, nil
	}
	jobs := m.readinessMonitorJobsLocked()
	now := time.Now().UTC()
	for _, d := range m.deployments {
		if len(scopes) > 0 && api.MCPConnectionScope(d.Scope) != readinessScope(scopes) {
			continue
		}
		if d.AppID != app || d.Status != DeployLive || d.TrafficPercent <= 0 || id != "" && !sameDeploymentID(d.ID, id) {
			continue
		}
		subject, err := m.readinessSubjectLocked(account, d.ID)
		if err != nil {
			return nil, err
		}
		for _, role := range subject.Policy.Roles {
			if !monitoringEnabled(role) {
				continue
			}
			status := monitorStatus(subject, role, jobs[d.ID+"/"+role.Name].Status)
			status.State = api.ReadinessMonitorState(status, now)
			out = append(out, cloneReadiness(status))
			if len(out) > 1000 {
				return nil, errors.New("too many readiness monitor subjects")
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DeploymentID+out[i].Role < out[j].DeploymentID+out[j].Role })
	return out, nil
}
