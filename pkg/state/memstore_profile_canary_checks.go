package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type memProfileCanaryCheck struct {
	check            api.CanaryProfileSignal
	appID, accountID string
	token            string
	leaseUntil       time.Time
}

func profileCanaryCheckMapKey(key ProfileCanaryCheckKey) string {
	return fmt.Sprintf("%s/%d/%s/%d", key.DeploymentID, key.CanaryStep, key.CanaryStepStartedAt.UTC().Format(time.RFC3339Nano), key.PolicyRevision)
}

func profileCanarySignalKey(signal api.CanaryProfileSignal) ProfileCanaryCheckKey {
	deploymentID := ""
	if signal.Candidate != nil {
		deploymentID = signal.Candidate.DeploymentID
	}
	return ProfileCanaryCheckKey{DeploymentID: deploymentID, CanaryStep: signal.CanaryStep, CanaryStepStartedAt: signal.CanaryStepStartedAt, PolicyRevision: signal.PolicyRevision}
}

func copyProfileCanarySignal(in api.CanaryProfileSignal) api.CanaryProfileSignal {
	body, _ := json.Marshal(in)
	var out api.CanaryProfileSignal
	_ = json.Unmarshal(body, &out)
	return out
}

func (m *MemStore) GetProfileCanaryCheck(_ context.Context, accountID, appID string, key ProfileCanaryCheckKey) (api.CanaryProfileSignal, error) {
	if key.CanaryStep < 0 || key.CanaryStep > math.MaxInt32 {
		return api.CanaryProfileSignal{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.profileCanaryChecks[profileCanaryCheckMapKey(key)]
	if !ok || row.appID != appID || row.accountID != accountID || !m.profileInvestigationOwnedLocked(accountID, appID) {
		return api.CanaryProfileSignal{}, ErrNotFound
	}
	return copyProfileCanarySignal(row.check), nil
}

func (m *MemStore) GetLatestProfileCanaryCheck(_ context.Context, accountID, appID, deploymentID string, policyRevision int64) (api.CanaryProfileSignal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.profileInvestigationOwnedLocked(accountID, appID) {
		return api.CanaryProfileSignal{}, ErrNotFound
	}
	var latest *memProfileCanaryCheck
	for _, row := range m.profileCanaryChecks {
		if row.appID != appID || row.accountID != accountID || row.check.PolicyRevision != policyRevision || row.check.Candidate == nil || row.check.Candidate.DeploymentID != deploymentID {
			continue
		}
		if latest == nil || row.check.CanaryStepStartedAt.After(latest.check.CanaryStepStartedAt) ||
			(row.check.CanaryStepStartedAt.Equal(latest.check.CanaryStepStartedAt) && row.check.CanaryStep > latest.check.CanaryStep) {
			latest = row
		}
	}
	if latest == nil {
		return api.CanaryProfileSignal{}, ErrNotFound
	}
	return copyProfileCanarySignal(latest.check), nil
}

func (m *MemStore) DiscoverProfileCanaryChecks(_ context.Context, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.profileCanaryChecks == nil {
		m.profileCanaryChecks = map[string]*memProfileCanaryCheck{}
	}
	var candidates []Deployment
	for _, d := range m.deployments {
		if !profileCanaryDeploymentInFlight(d) || d.CanaryStepStartedAt == nil {
			continue
		}
		app, ok := m.apps[d.AppID]
		if !ok || app.Manifest.Profiling == nil || !app.Manifest.Profiling.Enabled {
			continue
		}
		policy, ok := m.profileDeploymentPolicies[d.AppID]
		if !ok || !policy.Config.Enabled || policy.Revision < 1 || !m.profileInvestigationOwnedLocked(app.AccountID, d.AppID) {
			continue
		}
		account, ok := m.accounts[app.AccountID]
		if !ok || !api.MustLimitsFor(account.Plan).Profiling.Enabled {
			continue
		}
		key := ProfileCanaryCheckKey{DeploymentID: d.ID, CanaryStep: d.CanaryStep, CanaryStepStartedAt: *d.CanaryStepStartedAt, PolicyRevision: policy.Revision}
		if _, exists := m.profileCanaryChecks[profileCanaryCheckMapKey(key)]; exists {
			continue
		}
		candidates = append(candidates, d)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].CanaryStepStartedAt.Equal(*candidates[j].CanaryStepStartedAt) {
			if candidates[i].ID == candidates[j].ID {
				return candidates[i].CanaryStep < candidates[j].CanaryStep
			}
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].CanaryStepStartedAt.Before(*candidates[j].CanaryStepStartedAt)
	})
	if len(candidates) > api.ProfileAutoBatchSize {
		candidates = candidates[:api.ProfileAutoBatchSize]
	}
	for _, d := range candidates {
		app := m.apps[d.AppID]
		policy := m.profileDeploymentPolicies[d.AppID]
		key := ProfileCanaryCheckKey{DeploymentID: d.ID, CanaryStep: d.CanaryStep, CanaryStepStartedAt: *d.CanaryStepStartedAt, PolicyRevision: policy.Revision}
		signal := newProfileCanarySignal(d, policy, now)
		stable, count := m.profileCanaryStableLocked(d)
		start := profileCanaryCaptureStart(d, policy)
		end := start.Add(time.Duration(policy.Config.WindowSeconds) * time.Second)
		candidate := api.ProfileQuery{DeploymentID: d.ID, Runtime: policy.Config.Runtime, Start: start, End: end}
		signal.Candidate = &candidate
		if count != 1 {
			signal.Status = "inconclusive"
			if count == 0 {
				signal.Reason = "A live stable deployment in the canary environment is not available."
			} else {
				signal.Reason = "A unique live stable deployment is not available for the profile comparison."
			}
			completed := now.UTC()
			signal.CompletedAt = &completed
		} else {
			baseline := api.ProfileQuery{DeploymentID: stable.ID, Runtime: policy.Config.Runtime, Start: start, End: end}
			signal.Baseline = &baseline
			due := end.Add(api.ProfileAutoIngestionGrace)
			signal.NextAttemptAt = &due
		}
		m.profileCanaryChecks[profileCanaryCheckMapKey(key)] = &memProfileCanaryCheck{check: signal, appID: app.ID, accountID: app.AccountID}
	}
	return len(candidates), nil
}

func profileCanaryDeploymentInFlight(d Deployment) bool {
	rolloutState := NormalizeRolloutState(d.RolloutState)
	return d.Status == DeployLive && d.TrafficPercent > 0 && d.CanaryTotalSteps > 0 && d.CanaryStep < d.CanaryTotalSteps && (rolloutState == "pending" || rolloutState == "rolling_out")
}

func (m *MemStore) profileCanaryStableLocked(candidate Deployment) (Deployment, int) {
	var stable Deployment
	count := 0
	for _, d := range m.deployments {
		if d.ID == candidate.ID || d.AppID != candidate.AppID || d.Status != DeployLive || d.TrafficPercent <= 0 || d.DeletedAt != nil || normalizedDeploymentScope(d.Scope) != normalizedDeploymentScope(candidate.Scope) {
			continue
		}
		if d.CanaryTotalSteps > 0 && d.CanaryStep < d.CanaryTotalSteps {
			continue
		}
		stable, count = d, count+1
	}
	return stable, count
}

func newProfileCanarySignal(d Deployment, policy api.ProfileDeploymentPolicy, now time.Time) api.CanaryProfileSignal {
	options := api.NormalizeProfileRegressionOptions(policy.Config.Options)
	signal := api.CanaryProfileSignal{
		Mode: "report_only", Status: "queued", Reason: "Waiting for the canary profile window and ingestion.",
		CanaryStep: d.CanaryStep, CanaryStepStartedAt: d.CanaryStepStartedAt.UTC(), CreatedAt: now.UTC(),
		PolicyRevision: policy.Revision, Metric: options.Metric, WindowSeconds: policy.Config.WindowSeconds,
		Options: options, Evidence: []api.ProfileRegressionEvidence{},
	}
	signal.Gate = newProfileGateState(d, policy)
	if signal.Gate != nil {
		signal.Mode = "gate"
	}
	return signal
}

func profileCanaryCaptureStart(d Deployment, policy api.ProfileDeploymentPolicy) time.Time {
	anchor := d.CanaryStepStartedAt.UTC()
	if policy.Config.CanaryGate != nil {
		anchor = profileGateAnchor(d, policy)
	}
	return anchor.Add(time.Duration(policy.Config.WarmupSeconds) * time.Second)
}

func (m *MemStore) MaintainProfileCanaryChecks(_ context.Context, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pruned := 0
	for key, row := range m.profileCanaryChecks {
		if row.check.CompletedAt != nil && row.check.CompletedAt.Before(now.Add(-api.ProfileAutoReceiptRetention)) && pruned < api.ProfileAutoBatchSize {
			delete(m.profileCanaryChecks, key)
			pruned++
			continue
		}
		if row.check.Status != "queued" && row.check.Status != "running" {
			continue
		}
		policy, ok := m.profileDeploymentPolicies[row.appID]
		if !ok || !policy.Config.Enabled || policy.Revision != row.check.PolicyRevision || !m.profileInvestigationOwnedLocked(row.accountID, row.appID) {
			cancelMemProfileCanaryCheck(row, now)
		} else if row.check.Attempts >= api.ProfileAutoMaxAttempts && !row.leaseUntil.After(now) {
			row.check.Status, row.check.Reason = "inconclusive", "The worker could not finish within its attempt limit."
			completed := now.UTC()
			row.check.CompletedAt, row.check.NextAttemptAt, row.token, row.leaseUntil = &completed, nil, "", time.Time{}
		}
	}
	return nil
}

func cancelMemProfileCanaryCheck(row *memProfileCanaryCheck, now time.Time) {
	row.check.Status, row.check.Reason = "cancelled", "Automatic profiling policy changed or was disabled."
	completed := now.UTC()
	row.check.CompletedAt, row.check.NextAttemptAt, row.token, row.leaseUntil = &completed, nil, "", time.Time{}
}

func (m *MemStore) ClaimProfileCanaryCheck(_ context.Context, now time.Time) (ProfileCanaryCheckWork, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var selected *memProfileCanaryCheck
	for _, row := range m.profileCanaryChecks {
		p, ok := m.profileDeploymentPolicies[row.appID]
		if !ok || row.check.Status != "queued" && row.check.Status != "running" || row.check.NextAttemptAt == nil || row.check.NextAttemptAt.After(now) || row.leaseUntil.After(now) || row.check.Attempts >= api.ProfileAutoMaxAttempts || !p.Config.Enabled || p.Revision != row.check.PolicyRevision || !m.profileInvestigationOwnedLocked(row.accountID, row.appID) {
			continue
		}
		if selected == nil || row.check.NextAttemptAt.Before(*selected.check.NextAttemptAt) || row.check.NextAttemptAt.Equal(*selected.check.NextAttemptAt) && profileCanaryCheckMapKey(profileCanarySignalKey(row.check)) < profileCanaryCheckMapKey(profileCanarySignalKey(selected.check)) {
			selected = row
		}
	}
	if selected == nil {
		return ProfileCanaryCheckWork{}, ErrNotFound
	}
	selected.check.Status = "running"
	selected.check.Attempts++
	selected.token = uuid.NewString()
	selected.leaseUntil = now.Add(api.ProfileAutoLeaseDuration)
	return ProfileCanaryCheckWork{Check: profileGateWork(copyProfileCanarySignal(selected.check)), AppID: selected.appID, AccountID: selected.accountID, Token: selected.token}, nil
}

func (m *MemStore) FinishProfileCanaryCheck(_ context.Context, work ProfileCanaryCheckWork, assessment api.ProfileRegressionAssessment, retry bool, now time.Time) (api.CanaryProfileSignal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := profileCanarySignalKey(work.Check)
	row, ok := m.profileCanaryChecks[profileCanaryCheckMapKey(key)]
	if !ok || row.appID != work.AppID || row.accountID != work.AccountID || !m.profileInvestigationOwnedLocked(work.AccountID, work.AppID) {
		return api.CanaryProfileSignal{}, ErrNotFound
	}
	if row.token != work.Token || row.check.Status != "running" || !row.leaseUntil.After(now) {
		return api.CanaryProfileSignal{}, ErrProfileCheckLease
	}
	if err := validateProfileCanaryAssessment(assessment); err != nil {
		return api.CanaryProfileSignal{}, err
	}
	if row.check.Gate != nil {
		d := m.deployments[key.DeploymentID]
		p := m.profileDeploymentPolicies[work.AppID]
		if !profileCanaryDeploymentInFlight(d) || d.CanaryStep != key.CanaryStep || d.CanaryStepStartedAt == nil || !d.CanaryStepStartedAt.Equal(key.CanaryStepStartedAt) || p.Revision != key.PolicyRevision || !p.Config.Enabled {
			return api.CanaryProfileSignal{}, ErrProfileCheckLease
		}
		if err := validateProfileGateAssessment(row.check, assessment); err != nil {
			return api.CanaryProfileSignal{}, err
		}
	}
	out := copyProfileCanarySignal(row.check)
	out.CheckedAt = &assessment.CheckedAt
	out.Reason = assessment.Reason
	out.Options, out.Metric = assessment.Options, assessment.Options.Metric
	out.BaselineRequests, out.CandidateRequests = assessment.BaselineRequests, assessment.CandidateRequests
	out.Attribution = assessment.Attribution
	out.RequestMix = assessment.RequestMix
	out.RouteChecks = assessment.RouteChecks
	out.BaselineCoverage, out.CandidateCoverage = assessment.BaselineCoverage, assessment.CandidateCoverage
	out.Total, out.Evidence, out.UncomparableEntries = assessment.Total, assessment.Evidence, assessment.UncomparableEntries
	if retry && out.Attempts < api.ProfileAutoMaxAttempts && (out.Gate == nil || now.Before(out.Gate.Deadline)) {
		out.Status = "queued"
		due := now.Add(api.ProfileAutoRetryInterval).UTC()
		out.NextAttemptAt, out.CompletedAt = &due, nil
	} else {
		out.Status = assessment.Status
		completed := now.UTC()
		out.NextAttemptAt, out.CompletedAt = nil, &completed
	}
	finishProfileGateWindow(&out, assessment, retry, now)
	if out.CompletedAt != nil {
		scope := m.deployments[assessment.Candidate.DeploymentID].Scope
		o := profileAlertObservation{AppID: work.AppID, AccountID: work.AccountID, Scope: scope, Source: "canary", Revision: out.PolicyRevision, Assessment: assessment, EvidencePath: "/v1/apps/" + m.apps[work.AppID].Slug + "/profiles/canary-checks/" + assessment.Candidate.DeploymentID}
		plans, err := m.prepareProfileAlertsLocked(o)
		if err != nil {
			return api.CanaryProfileSignal{}, err
		}
		if profileAlertHasEvent(plans) {
			saved := &memProfileDeploymentCheck{accountID: work.AccountID, check: api.ProfileDeploymentCheck{AppID: work.AppID, DeploymentID: assessment.Candidate.DeploymentID, Scope: scope, Baseline: &assessment.Baseline, Candidate: assessment.Candidate}}
			if err := m.saveAutoProfileInvestigationLocked(saved, assessment, now); err != nil {
				return api.CanaryProfileSignal{}, err
			}
			profileAlertInvestigation(plans, m.apps[work.AppID].Slug, saved.check.InvestigationID)
		}
		m.publishProfileAlertsLocked(o, plans)
	}
	row.check, row.token, row.leaseUntil = out, "", time.Time{}
	m.profileCanaryChecks[profileCanaryCheckMapKey(key)] = row
	return copyProfileCanarySignal(out), nil
}

func validateProfileCanaryAssessment(assessment api.ProfileRegressionAssessment) error {
	if err := validateProfileMixSnapshot(assessment.RequestMix, assessment.Baseline, assessment.Candidate); err != nil {
		return err
	}
	if err := validateProfileCheckResult(assessment); err != nil {
		return err
	}
	body, err := json.Marshal(assessment)
	if err != nil || len(body) > api.ProfileRegressionMaxAssessmentBytes {
		return errors.New("profile canary assessment exceeds its storage bound")
	}
	return nil
}
