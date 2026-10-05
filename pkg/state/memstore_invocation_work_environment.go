package state

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

func cloneInvocationWorkAdmission(owner InvocationWorkEnvironmentAdmission) InvocationWorkEnvironmentAdmission {
	owner.KeyDigest = append([]byte(nil), owner.KeyDigest...)
	owner.FairnessDigest = append([]byte(nil), owner.FairnessDigest...)
	return owner
}

func cloneInvocationWorkEnvelope(inv Invocation) Invocation {
	inv.Payload = append(json.RawMessage(nil), inv.Payload...)
	inv.Result = append(json.RawMessage(nil), inv.Result...)
	inv.RetryPolicyJSON = append(json.RawMessage(nil), inv.RetryPolicyJSON...)
	inv.Headers = append(json.RawMessage(nil), inv.Headers...)
	inv.WorkKeyDigest = append([]byte(nil), inv.WorkKeyDigest...)
	inv.WorkFairnessDigest = append([]byte(nil), inv.WorkFairnessDigest...)
	for _, field := range []**time.Time{&inv.WorkExpiresAt, &inv.ScheduledAt, &inv.LeaseExpiresAt, &inv.ReceivedAt, &inv.CompletedAt, &inv.DeadlineAt, &inv.ResultRetentionUntil, &inv.LastReplayedAt} {
		if *field != nil {
			value := **field
			*field = &value
		}
	}
	if inv.CronID != nil {
		value := *inv.CronID
		inv.CronID = &value
	}
	if inv.Outcome != nil {
		value := *inv.Outcome
		inv.Outcome = &value
	}
	return inv
}

func (m *MemStore) InvocationWorkEnvironmentAdmission(_ context.Context, id string) (InvocationWorkEnvironmentAdmission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owner, found := m.invocationWorkEnvironmentAdmissions[id]
	if !found {
		return owner, ErrNotFound
	}
	return cloneInvocationWorkAdmission(owner), nil
}

func (m *MemStore) validateWorkEnvironmentLocked(info invocationWorkEnvironment) error {
	if info.environment.ID == "" {
		return nil
	}
	env, found := m.projectEnvironments[info.environment.ID]
	spec, pinned := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadDeploymentSpecs[info.deployment]]
	app, owned := m.apps[info.app.ID]
	deployment, deployed := m.deployments[info.deployment]
	hash, err := WorkloadSettingsHash(spec.Settings)
	if !found || !pinned || !owned || app.Status == AppDeleted || app.AccountID != info.app.AccountID || app.ProjectID != info.app.ProjectID ||
		env.AccountID != app.AccountID || env.ProjectID != app.ProjectID ||
		spec.ID != info.spec.ID || spec.EnvironmentID != env.ID || spec.AppID != app.ID || spec.Hash != info.spec.Hash ||
		!deployed || deployment.AppID != app.ID || normalizedDeploymentScope(deployment.Scope) != env.Slug ||
		deployment.Status != DeployLive || err != nil || hash != spec.Hash {
		return ErrInvocationEnvironmentWorkIsolation
	}
	return nil
}

func (m *MemStore) validateWorkEnvironmentReplayLocked(info invocationWorkEnvironment, existing Invocation) error {
	owner, found := m.invocationWorkEnvironmentAdmissions[existing.ID]
	if info.environment.ID == "" {
		if found {
			return ErrInvocationEnvironmentWorkIsolation
		}
		return nil
	}
	if !found || owner.EnvironmentID != info.environment.ID || !admissionMatchesInvocation(owner, existing) {
		return ErrInvocationEnvironmentWorkIsolation
	}
	return nil
}

func (m *MemStore) registerWorkEnvironmentLocked(info invocationWorkEnvironment, inv Invocation) error {
	if info.environment.ID == "" {
		return nil
	}
	domains := map[string][]byte{"key": inv.WorkKeyDigest}
	if len(inv.WorkFairnessDigest) > 0 {
		domains["fairness"] = inv.WorkFairnessDigest
	}
	for kind, digest := range domains {
		key := workEnvironmentDomainKey(inv.AppID, inv.WorkPolicyName, kind, digest)
		if owner, exists := m.invocationWorkEnvironmentDomains[key]; exists && owner != info.environment.ID {
			return ErrInvocationEnvironmentWorkIsolation
		}
	}
	for kind, digest := range domains {
		m.invocationWorkEnvironmentDomains[workEnvironmentDomainKey(inv.AppID, inv.WorkPolicyName, kind, digest)] = info.environment.ID
	}
	return nil
}

func (m *MemStore) validateWorkEnvironmentClaimLocked(inv Invocation) error {
	ownerID, known := m.invocationWorkEnvironmentDomains[workEnvironmentDomainKey(inv.AppID, inv.WorkPolicyName, "key", inv.WorkKeyDigest)]
	owner, found := m.invocationWorkEnvironmentAdmissions[inv.ID]
	var headers map[string]string
	if len(inv.Headers) > 0 && json.Unmarshal(inv.Headers, &headers) != nil {
		return ErrInvocationEnvironmentWorkIsolation
	}
	revision, release, err := invocationPinHeaders(headers)
	if err != nil {
		return err
	}
	deployment, pinned := m.deployments[revision]
	set, releasePinned := m.projectReleaseSets[release]
	stagePin := (pinned && invocationStageScope(deployment.Scope)) || (releasePinned && invocationStageScope(set.EnvironmentSlug))
	if !known {
		if found || stagePin {
			return ErrInvocationEnvironmentWorkIsolation
		}
		return nil
	}
	if !found || !stagePin || owner.EnvironmentID != ownerID || !admissionMatchesInvocation(owner, inv) ||
		!stageKeyedInvocationSupported(inv) {
		return ErrInvocationEnvironmentWorkIsolation
	}
	if release != "" {
		app := m.apps[inv.AppID]
		if !releasePinned || set.AccountID != inv.AccountID || set.ProjectID != app.ProjectID ||
			(set.ExpiresAt != nil && !set.ExpiresAt.After(time.Now())) {
			return ErrInvocationEnvironmentWorkIsolation
		}
		pinned = false
		for _, member := range set.Members {
			if member.AppID == inv.AppID {
				deployment, pinned = m.deployments[member.DeploymentID]
				break
			}
		}
	}
	spec, specPinned := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadDeploymentSpecs[deployment.ID]]
	env := m.projectEnvironments[owner.EnvironmentID]
	info := invocationWorkEnvironment{app: m.apps[inv.AppID], environment: env, spec: spec, deployment: deployment.ID}
	if !pinned || !specPinned || inv.AccountID != info.app.AccountID || spec.ID != owner.WorkloadSpecID || spec.Hash != owner.SettingsHash ||
		m.validateWorkEnvironmentLocked(info) != nil {
		return ErrInvocationEnvironmentWorkIsolation
	}
	if owner.FairnessLimit > 0 && m.invocationWorkEnvironmentDomains[workEnvironmentDomainKey(inv.AppID, inv.WorkPolicyName, "fairness", inv.WorkFairnessDigest)] != ownerID {
		return ErrInvocationEnvironmentWorkIsolation
	}
	return nil
}

func (m *MemStore) deleteAppWorkOwnershipLocked(appID string) {
	for key := range m.invocationWorkEnvironmentDomains {
		if strings.HasPrefix(key, appID+"\x00") {
			delete(m.invocationWorkEnvironmentDomains, key)
		}
	}
	for id, owner := range m.invocationWorkEnvironmentAdmissions {
		if owner.AppID == appID {
			delete(m.invocationWorkEnvironmentAdmissions, id)
		}
	}
}
