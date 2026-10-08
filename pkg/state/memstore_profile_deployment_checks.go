package state

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type memProfileDeploymentCheck struct {
	check            api.ProfileDeploymentCheck
	accountID, token string
	leaseUntil       time.Time
}

func (m *MemStore) GetProfileDeploymentPolicy(_ context.Context, accountID, appID string) (api.ProfileDeploymentPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.profileInvestigationOwnedLocked(accountID, appID) {
		return api.ProfileDeploymentPolicy{}, ErrNotFound
	}
	if p, ok := m.profileDeploymentPolicies[appID]; ok {
		p.Config = copyProfilePolicyConfig(p.Config)
		if p.UpdatedAt != nil {
			at := *p.UpdatedAt
			p.UpdatedAt = &at
		}
		return p, nil
	}
	return DefaultProfileDeploymentPolicy(appID, m.apps[appID].Runtime), nil
}

func (m *MemStore) SaveProfileDeploymentPolicy(_ context.Context, accountID, appID string, req api.SaveProfileDeploymentPolicyRequest) (api.ProfileDeploymentPolicy, error) {
	if err := ValidateProfileDeploymentPolicy(req); err != nil {
		return api.ProfileDeploymentPolicy{}, err
	}
	req.Config = copyProfilePolicyConfig(req.Config)
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.profileInvestigationOwnedLocked(accountID, appID) {
		return api.ProfileDeploymentPolicy{}, ErrNotFound
	}
	p := m.profileDeploymentPolicies[appID]
	if p.Revision != *req.ExpectedRevision {
		p.Config = copyProfilePolicyConfig(p.Config)
		return p, ErrProfileInvestigationRevision
	}
	if m.profileDeploymentPolicies == nil {
		m.profileDeploymentPolicies = map[string]api.ProfileDeploymentPolicy{}
	}
	now := time.Now().UTC()
	p = api.ProfileDeploymentPolicy{AppID: appID, Revision: p.Revision + 1, Config: req.Config, UpdatedAt: &now}
	m.profileDeploymentPolicies[appID] = p
	for _, j := range m.profileDeploymentChecks {
		if j.check.AppID == appID && profileCheckPending(j.check.Status) {
			cancelMemProfileCheck(j, now)
		}
	}
	for _, j := range m.profileCanaryChecks {
		if j.appID == appID && (j.check.Status == "queued" || j.check.Status == "running") {
			cancelMemProfileCanaryCheck(j, now)
		}
	}
	out := p
	out.Config = copyProfilePolicyConfig(p.Config)
	at := *p.UpdatedAt
	out.UpdatedAt = &at
	return out, nil
}

func profileCheckPending(status string) bool { return status == "queued" || status == "running" }

func cancelMemProfileCheck(j *memProfileDeploymentCheck, now time.Time) {
	j.check.Status, j.check.Reason = "cancelled", "Automatic profiling policy changed or was disabled."
	j.check.CompletedAt, j.check.NextAttemptAt, j.token, j.leaseUntil = &now, nil, "", time.Time{}
}

func (m *MemStore) ListProfileDeploymentChecks(_ context.Context, accountID, appID string) ([]api.ProfileDeploymentCheck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.profileInvestigationOwnedLocked(accountID, appID) {
		return nil, ErrNotFound
	}
	out := []api.ProfileDeploymentCheck{}
	for _, j := range m.profileDeploymentChecks {
		if j.check.AppID == appID {
			out = append(out, m.profileCheckCopyLocked(j))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].DeploymentID < out[j].DeploymentID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if len(out) > api.ProfileAutoMaxResults {
		out = out[:api.ProfileAutoMaxResults]
	}
	return out, nil
}

func (m *MemStore) profileCheckCopyLocked(j *memProfileDeploymentCheck) api.ProfileDeploymentCheck {
	out := copyProfileDeploymentCheck(j.check)
	if _, exists := m.profileInvestigations[out.InvestigationID]; !exists {
		out.InvestigationID = ""
	}
	return out
}

func (m *MemStore) GetProfileDeploymentCheck(_ context.Context, accountID, appID, deploymentID string) (api.ProfileDeploymentCheck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.profileDeploymentChecks[deploymentID]
	if !ok || j.check.AppID != appID || !m.profileInvestigationOwnedLocked(accountID, appID) {
		return api.ProfileDeploymentCheck{}, ErrNotFound
	}
	return m.profileCheckCopyLocked(j), nil
}

func (m *MemStore) DiscoverProfileDeploymentChecks(_ context.Context, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var candidates []Deployment
	for _, d := range m.deployments {
		p, ok := m.profileDeploymentPolicies[d.AppID]
		a := m.apps[d.AppID]
		if !ok || !p.Config.Enabled || !m.profileInvestigationOwnedLocked(a.AccountID, d.AppID) || !api.MustLimitsFor(m.accounts[a.AccountID].Plan).Profiling.Enabled || !profileCheckDeploymentSucceeded(d) || d.RolloutCompletedAt.Before(*p.UpdatedAt) || d.RolloutCompletedAt.Before(now.Add(-api.ProfileAutoDiscoveryLookback)) || d.RolloutCompletedAt.After(now) || m.profileDeploymentChecks[d.ID] != nil {
			continue
		}
		candidates = append(candidates, d)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].RolloutCompletedAt.Equal(*candidates[j].RolloutCompletedAt) {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].RolloutCompletedAt.Before(*candidates[j].RolloutCompletedAt)
	})
	if len(candidates) > api.ProfileAutoBatchSize {
		candidates = candidates[:api.ProfileAutoBatchSize]
	}
	if m.profileDeploymentChecks == nil {
		m.profileDeploymentChecks = map[string]*memProfileDeploymentCheck{}
	}
	for _, d := range candidates {
		var baseline Deployment
		for _, b := range m.deployments {
			if b.ID == d.ID || b.AppID != d.AppID || normalizedDeploymentScope(b.Scope) != normalizedDeploymentScope(d.Scope) || !profileCheckDeploymentSucceeded(b) || !b.RolloutCompletedAt.Before(d.CreatedAt) {
				continue
			}
			if baseline.ID == "" || b.RolloutCompletedAt.After(*baseline.RolloutCompletedAt) || b.RolloutCompletedAt.Equal(*baseline.RolloutCompletedAt) && b.ID > baseline.ID {
				baseline = b
			}
		}
		m.profileDeploymentChecks[d.ID] = &memProfileDeploymentCheck{check: newProfileDeploymentCheck(d, baseline.ID, m.profileDeploymentPolicies[d.AppID], now), accountID: m.apps[d.AppID].AccountID}
	}
	return len(candidates), nil
}

func profileCheckDeploymentSucceeded(d Deployment) bool {
	return (d.Status == DeployLive || d.Status == DeploySuperseded) && d.RolloutState == "complete" && d.RolloutCompletedAt != nil && d.DeletedAt == nil
}

func (m *MemStore) MaintainProfileDeploymentChecks(_ context.Context, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pruned := 0
	for id, j := range m.profileDeploymentChecks {
		if j.check.CompletedAt != nil && j.check.CompletedAt.Before(now.Add(-api.ProfileAutoReceiptRetention)) && pruned < api.ProfileAutoBatchSize {
			delete(m.profileDeploymentChecks, id)
			pruned++
			continue
		}
		if !profileCheckPending(j.check.Status) {
			continue
		}
		p := m.profileDeploymentPolicies[j.check.AppID]
		if !p.Config.Enabled || p.Revision != j.check.PolicyRevision || !m.profileInvestigationOwnedLocked(j.accountID, j.check.AppID) {
			cancelMemProfileCheck(j, now)
		} else if j.check.Attempts >= api.ProfileAutoMaxAttempts && !j.leaseUntil.After(now) {
			j.check.Status, j.check.Reason = "inconclusive", "The worker could not finish within its attempt limit."
			j.check.CompletedAt, j.check.NextAttemptAt, j.token, j.leaseUntil = &now, nil, "", time.Time{}
		}
	}
	return nil
}

func (m *MemStore) ClaimProfileDeploymentCheck(_ context.Context, now time.Time) (ProfileDeploymentCheckWork, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var selected *memProfileDeploymentCheck
	for _, j := range m.profileDeploymentChecks {
		p := m.profileDeploymentPolicies[j.check.AppID]
		if !profileCheckPending(j.check.Status) || j.check.NextAttemptAt.After(now) || j.leaseUntil.After(now) || j.check.Attempts >= api.ProfileAutoMaxAttempts || !p.Config.Enabled || p.Revision != j.check.PolicyRevision || !m.profileInvestigationOwnedLocked(j.accountID, j.check.AppID) {
			continue
		}
		if selected == nil || j.check.NextAttemptAt.Before(*selected.check.NextAttemptAt) || j.check.NextAttemptAt.Equal(*selected.check.NextAttemptAt) && j.check.DeploymentID < selected.check.DeploymentID {
			selected = j
		}
	}
	if selected == nil {
		return ProfileDeploymentCheckWork{}, ErrNotFound
	}
	selected.check.Status = "running"
	selected.check.Attempts++
	selected.token = uuid.NewString()
	selected.leaseUntil = now.Add(api.ProfileAutoLeaseDuration)
	return ProfileDeploymentCheckWork{Check: copyProfileDeploymentCheck(selected.check), AccountID: selected.accountID, Token: selected.token}, nil
}

func (m *MemStore) FinishProfileDeploymentCheck(_ context.Context, work ProfileDeploymentCheckWork, assessment api.ProfileRegressionAssessment, retry bool, now time.Time) (api.ProfileDeploymentCheck, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.profileDeploymentChecks[work.Check.DeploymentID]
	if !ok || j.accountID != work.AccountID || !m.profileInvestigationOwnedLocked(work.AccountID, work.Check.AppID) {
		return api.ProfileDeploymentCheck{}, ErrNotFound
	}
	if j.token != work.Token || j.check.Status != "running" || !j.leaseUntil.After(now) {
		return api.ProfileDeploymentCheck{}, ErrProfileCheckLease
	}
	if err := validateProfileCheckResult(assessment); err != nil {
		return api.ProfileDeploymentCheck{}, err
	}
	observation := profileAlertObservation{AppID: j.check.AppID, AccountID: work.AccountID, Scope: j.check.Scope, Source: "deployment", Revision: j.check.PolicyRevision, Assessment: assessment, EvidencePath: "/v1/apps/" + m.apps[j.check.AppID].Slug + "/profiles/deployment-checks/" + j.check.DeploymentID}
	var alertPlans []profileAlertPlan
	if !(retry && j.check.Attempts < api.ProfileAutoMaxAttempts) {
		var err error
		alertPlans, err = m.prepareProfileAlertsLocked(observation)
		if err != nil {
			return api.ProfileDeploymentCheck{}, err
		}
	}
	original := j
	j = &memProfileDeploymentCheck{check: copyProfileDeploymentCheck(original.check), accountID: original.accountID, token: original.token, leaseUntil: original.leaseUntil}
	if retry && j.check.Attempts < api.ProfileAutoMaxAttempts {
		due := now.Add(api.ProfileAutoRetryInterval)
		j.check.Status, j.check.Reason, j.check.NextAttemptAt = "queued", assessment.Reason, &due
	} else {
		j.check.Status, j.check.Reason = assessment.Status, assessment.Reason
		j.check.CompletedAt, j.check.NextAttemptAt = &now, nil
		if j.check.Baseline != nil {
			if err := m.saveAutoProfileInvestigationLocked(j, assessment, now); err != nil {
				return api.ProfileDeploymentCheck{}, err
			}
		}
	}
	profileAlertInvestigation(alertPlans, m.apps[j.check.AppID].Slug, j.check.InvestigationID)
	m.publishProfileAlertsLocked(observation, alertPlans)
	j.token, j.leaseUntil = "", time.Time{}
	m.profileDeploymentChecks[j.check.DeploymentID] = j
	return m.profileCheckCopyLocked(j), nil
}

func validateProfileCheckResult(a api.ProfileRegressionAssessment) error {
	if err := validateProfileAttribution(a.Attribution); err != nil {
		return err
	}
	if err := validateProfileRouteChecks(a.Options, a.RouteChecks); err != nil {
		return err
	}
	if a.Status != "regressed" && a.Status != "no_regression_detected" && a.Status != "inconclusive" {
		return errors.New("invalid automatic assessment status")
	}
	return nil
}

func (m *MemStore) saveAutoProfileInvestigationLocked(j *memProfileDeploymentCheck, a api.ProfileRegressionAssessment, now time.Time) error {
	count := 0
	for _, row := range m.profileInvestigations {
		if row.AppID == j.check.AppID {
			count++
		}
	}
	if count >= api.ProfileInvestigationMaxPerApp {
		j.check.Reason += " Investigation not saved: the app's saved-investigation limit is full."
		return nil
	}
	req, err := profileCheckInvestigation(j.check, a)
	if err != nil {
		return err
	}
	a.InvestigationRevision = 2
	row := api.ProfileInvestigation{ID: uuid.NewString(), AppID: j.check.AppID, Revision: 2, Investigation: req.Investigation, CreatedAt: now, UpdatedAt: now, Assessment: &a}
	if _, err := validateProfileAssessment(api.ProfileInvestigation{Revision: 1, Investigation: req.Investigation}, 1, a); err != nil {
		return err
	}
	if m.profileInvestigations == nil {
		m.profileInvestigations = map[string]api.ProfileInvestigation{}
	}
	m.profileInvestigations[row.ID] = copyProfileInvestigation(row)
	j.check.InvestigationID = row.ID
	return nil
}

func copyProfilePolicyConfig(c api.ProfileDeploymentPolicyConfig) api.ProfileDeploymentPolicyConfig {
	c.Options = api.NormalizeProfileRegressionOptions(c.Options)
	if c.CanaryGate != nil {
		g := *c.CanaryGate
		c.CanaryGate = &g
	}
	if c.Periodic != nil {
		p := *c.Periodic
		c.Periodic = &p
	}
	return c
}
