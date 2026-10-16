package mcpmanagement

import (
	"errors"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

type storedPlan struct {
	EnvironmentRescheduleResult *api.EnvironmentCacheSchedule
	EnvironmentScheduleResult   *api.EnvironmentCacheSchedule
	EnvironmentPurgeResult      *api.EnvironmentCachePurgeReceipt
	EnvironmentCacheResult      *api.EnvironmentCachePolicy
	CacheResult                 *cacheRuleReceipt
	BudgetResult                *api.EnvironmentRouteBudgets
	Plan                        releasePlan
	AppHash                     [32]byte
	LatestID                    string
	Attempted                   bool
	Result                      *api.DeploymentResponse
	Promoted                    *api.DeploymentResponse
	PromotionAttempted          bool
	Review                      *api.AppReleaseReview
}

func (m *management) restorePlans() error {
	if m.opts.planStore == nil {
		return nil
	}
	var records map[string]storedPlan
	if err := m.opts.planStore.read("plans/"+m.opts.planNamespace, &records); err != nil {
		return err
	}
	if len(records) > maxReleasePlans {
		return errors.New("invalid stored plan capacity")
	}
	for id, record := range records {
		p := record.Plan
		if time.Now().After(p.ExpiresAt) {
			continue
		}
		if id != p.ID || len(id) != 64 || p.APIBase != m.opts.APIBase || !m.appIDAllowed(p.AppID) || (p.Scope != api.DefaultEnvScope && p.Scope != cacheRulePolicyScope && (!api.ValidProjectEnvironmentSlug(p.Scope) || p.EnvironmentID == "" || p.ProjectID == "" || p.Project == "")) {
			return errors.New("stored release plan binding mismatch")
		}
		if p.Scope == cacheRulePolicyScope && p.Action != "cache_rule" {
			return errors.New("stored shared policy scope requires a cache rule plan")
		}
		if p.Source != nil && ((p.Action != "deploy" && p.Action != "workload_rollout") || p.Image != "" || p.Source.AppID != p.AppID || !sourceCommit.MatchString(p.Source.CommitSHA) || !sourceRepository.MatchString(p.Source.Repo) || p.Source.RepositoryID <= 0 || p.Source.InstallationID <= 0 || record.Review == nil || !reflect.DeepEqual(p.Source.ReleaseReview, *record.Review)) {
			return errors.New("stored source plan binding mismatch")
		}
		if (p.Action == "workload_rollout" || p.Action == "service_abort") && (record.Review == nil || record.Review.Protected || !immutableImage.MatchString(p.Image) && p.Source == nil && p.Action == "workload_rollout" || p.RolloutMode != api.ExecutionModeService && p.RolloutMode != api.ExecutionModeWorker || p.Action == "service_abort" && p.RolloutMode != api.ExecutionModeService) {
			return errors.New("stored native rollout binding mismatch")
		}

		if p.Action == "route_budgets" {
			if p.Budgets == nil || routemonitor.ValidateEnvironmentBudgets(p.Budgets.Proposed) != nil || validateBudgetResponse(p.Budgets.Before, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID) != nil || p.Budgets.Proposed.AppID != p.AppID || p.Budgets.Proposed.ProjectID != p.ProjectID || p.Budgets.Proposed.EnvironmentID != p.EnvironmentID || *p.Budgets.Proposed.ExpectedRevision != p.Budgets.Before.Revision || *p.Budgets.Proposed.ExpectedAppRevision != p.Budgets.Before.AppRevision {
				return errors.New("stored route budget plan binding mismatch")
			}
			if record.BudgetResult != nil && validateBudgetResponse(*record.BudgetResult, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID) != nil {
				return errors.New("stored route budget receipt binding mismatch")
			}
		}
		if p.Action == "cache_rule" {
			if p.CacheRule == nil || p.Scope != cacheRulePolicyScope || p.Project != "" || p.ProjectID != "" || p.EnvironmentID != "" || m.cacheRuleAccess("", "") != nil || p.CacheRule.PolicyScope != cacheRulePolicyScope || p.CacheRule.Version < 1 || p.CacheRule.Before.ID == "" || p.CacheRule.Before.Host != api.EdgeRuleAppHosts || p.CacheRule.Proposed.Validate() != nil {
				return errors.New("stored cache rule plan binding mismatch")
			}
			if record.CacheResult != nil {
				expected := p.CacheRule.Before
				expected.Cache = p.CacheRule.Proposed
				if record.CacheResult.AppID != p.AppID || record.CacheResult.PolicyScope != cacheRulePolicyScope || !reflect.DeepEqual(record.CacheResult.Rule, expected) {
					return errors.New("stored cache rule receipt binding mismatch")
				}
			}
		}
		if p.Action == "environment_cache_policy" || p.Action == "environment_cache_inheritance" || p.Action == "environment_cache_copy" || p.Action == "environment_cache_rollback" {
			if validateEnvironmentCachePlan(&p) != nil {
				return errors.New("stored environment cache plan binding mismatch")
			}
			if record.EnvironmentCacheResult != nil && (validateEnvironmentCachePolicy(*record.EnvironmentCacheResult, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID) != nil || record.EnvironmentCacheResult.Inherited != p.EnvironmentCache.Proposed.Inherited || record.EnvironmentCacheResult.Revision != *p.EnvironmentCache.Proposed.ExpectedRevision+1 || !reflect.DeepEqual(record.EnvironmentCacheResult.Rules, p.EnvironmentCache.Proposed.Rules)) {
				return errors.New("stored environment cache receipt binding mismatch")
			}
		}
		if p.Action == "environment_cache_purge" {
			if validateEnvironmentPurgePlan(&p) != nil {
				return errors.New("stored purge review binding mismatch")
			}
			if out := record.EnvironmentPurgeResult; out != nil && (validatePurgeReceipt(*out, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID, p.EnvironmentPurge.Proposed.RequestID) != nil || out.Path != p.EnvironmentPurge.Proposed.Path) {
				return errors.New("stored purge receipt binding mismatch")
			}
		}
		if p.Action == "environment_cache_schedule" {
			if validateEnvironmentSchedulePlan(&p) != nil {
				return errors.New("stored schedule review binding mismatch")
			}
			if out := record.EnvironmentScheduleResult; out != nil && (validateScheduleResponse(*out, p.AppID, p.Scope, p.ProjectID, p.EnvironmentID, p.EnvironmentSchedule.Proposed.ScheduleID) != nil || !reflect.DeepEqual(out.CreateEnvironmentCacheScheduleRequest, p.EnvironmentSchedule.Proposed)) {
				return errors.New("stored schedule receipt binding mismatch")
			}
		}
		if p.Action == "environment_cache_reschedule" {
			if validateEnvironmentReschedulePlan(&p) != nil {
				return errors.New("stored reschedule review binding mismatch")
			}
			if record.EnvironmentRescheduleResult != nil && validateRescheduleReceipt(&p, *record.EnvironmentRescheduleResult) != nil {
				return errors.New("stored replacement receipt binding mismatch")
			}
		}
		p.environmentRescheduleResult = record.EnvironmentRescheduleResult
		p.environmentScheduleResult = record.EnvironmentScheduleResult
		p.environmentPurgeResult = record.EnvironmentPurgeResult
		p.environmentCacheResult = record.EnvironmentCacheResult
		p.cacheResult = record.CacheResult
		p.budgetResult = record.BudgetResult
		p.appHash, p.latestID, p.attempted = record.AppHash, record.LatestID, record.Attempted
		p.review = record.Review
		p.result, p.promoted, p.promotionAttempted = record.Result, record.Promoted, record.PromotionAttempted
		m.plans[id] = &p
	}
	return nil
}

// Called with the management lock held. Persist the attempt BEFORE the customer
// API write, so a process death after commit retries the original key and body.
func (m *management) persistPlans() error {
	if m.opts.planStore == nil {
		return nil
	}
	records := make(map[string]storedPlan, len(m.plans))
	for id, p := range m.plans {
		if time.Now().After(p.ExpiresAt) {
			continue
		}
		records[id] = storedPlan{EnvironmentRescheduleResult: p.environmentRescheduleResult, EnvironmentScheduleResult: p.environmentScheduleResult, EnvironmentPurgeResult: p.environmentPurgeResult, EnvironmentCacheResult: p.environmentCacheResult, CacheResult: p.cacheResult, BudgetResult: p.budgetResult, Plan: *p, AppHash: p.appHash, LatestID: p.latestID, Attempted: p.attempted, Result: p.result, Promoted: p.promoted, PromotionAttempted: p.promotionAttempted, Review: p.review}
	}
	return m.opts.planStore.update("plans/"+m.opts.planNamespace, records)
}
