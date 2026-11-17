package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const workflowScheduleBatch = api.WorkflowScheduleBatch

// workflowFailurePolicyInterval spaces complete failure-policy passes. Each
// policy costs a locked transaction of several statements, so evaluating every
// policy on every one-second tick grew database load with the number of
// policies rather than with failures. ADR-905 detection is eventual and the
// shortest observation window is 60 s, so a pass every 10 s keeps a latch well
// inside it. A pass with more pages keeps paging on consecutive ticks.
const workflowFailurePolicyInterval = 10 * time.Second

const (
	workflowFailurePolicyPassTimeout = 5 * time.Second
	workflowFailurePolicyReadTimeout = time.Second
	workflowFailurePolicyCacheTTL    = time.Second
)

type workflowSchedulePolicyCheck struct {
	allowed bool
	err     error
	checked time.Time
}

type workflowSchedulePolicyKey struct{ appID, name string }

// The keyed workflow worker serializes this scan within one process. Database
// admission serializes it across scheduler replicas and manual run producers.
func (l *Loop) runWorkflowSchedulesTick(ctx context.Context) error {
	if !l.workflowsDispatched {
		return nil
	}
	// Stop admitting new scheduled work while the platform-wide workflow kill
	// switch holds dispatch. Leave schedule cursors untouched so each schedule's
	// normal missed-fire policy applies when the capability is restored.
	if l.engine.capabilities != nil && !l.engine.capabilities.Released("workflows") {
		return nil
	}
	policyErr := l.runWorkflowFailurePoliciesTick(ctx)
	// If the bulk pass fails, check each scheduled target independently. A
	// target whose policy cannot be checked or is over its limit stays stopped,
	// while a failure in another app no longer blocks its schedules.
	checks := make(map[workflowSchedulePolicyKey]workflowSchedulePolicyCheck)
	return errors.Join(policyErr, l.runApplicationWorkflowSchedulesTick(ctx, l.workflowFailurePolicyUncertain, checks), l.runTenantWorkflowSchedulesTick(ctx, l.workflowFailurePolicyUncertain, checks))
}

func (l *Loop) workflowSchedulePolicyAllowed(ctx context.Context, appID, name string, checks map[workflowSchedulePolicyKey]workflowSchedulePolicyCheck) (bool, error) {
	key := workflowSchedulePolicyKey{appID, name}
	if prior, ok := checks[key]; ok && time.Since(prior.checked) < workflowFailurePolicyCacheTTL {
		return prior.allowed, prior.err
	}
	store, ok := l.engine.Store().(state.StoredWorkflowFailurePolicyStore)
	if !ok {
		return false, errors.New("workflow failure policy unavailable")
	}
	readCtx, cancel := context.WithTimeout(ctx, workflowFailurePolicyReadTimeout)
	defer cancel()
	policy, err := store.GetStoredWorkflowFailurePolicy(readCtx, appID, name)
	if err != nil {
		checks[key] = workflowSchedulePolicyCheck{err: err, checked: time.Now()}
		return false, err
	}
	thresholdReached := policy.Policy.Enabled && policy.ObservedFailures >= int64(policy.Policy.FailureThreshold) && policy.ObservedCompletedRuns >= int64(policy.Policy.MinCompletedRuns)
	allowed := !policy.Paused && !thresholdReached
	checks[key] = workflowSchedulePolicyCheck{allowed: allowed, checked: time.Now()}
	return allowed, nil
}

func (l *Loop) runWorkflowFailurePoliciesTick(ctx context.Context) error {
	store, ok := l.engine.Store().(state.StoredWorkflowFailurePolicyStore)
	if !ok {
		return nil
	}
	now := l.now()
	if l.workflowFailurePolicyAfter == "" && now.Before(l.workflowFailurePolicyNextPass) {
		return nil
	}
	passCtx, cancel := context.WithTimeout(ctx, workflowFailurePolicyPassTimeout)
	defer cancel()
	next, err := store.EvaluateStoredWorkflowFailurePolicies(passCtx, l.engine.OwnerNodeID(), l.workflowFailurePolicyAfter, api.StoredWorkflowFailurePolicyBatch, now)
	if err != nil {
		l.workflowFailurePolicyUncertain = true
		l.workflowFailurePolicyPassFailed = true
		var batchErr *state.StoredWorkflowFailurePolicyBatchError
		if !errors.As(err, &batchErr) {
			return fmt.Errorf("workflow failure policies: %w", err)
		}
	}
	l.workflowFailurePolicyAfter = next
	if next == "" {
		l.workflowFailurePolicyUncertain = l.workflowFailurePolicyPassFailed
		l.workflowFailurePolicyPassFailed = false
		l.workflowFailurePolicyNextPass = now.Add(workflowFailurePolicyInterval)
	}
	if err != nil {
		return fmt.Errorf("workflow failure policies: %w", err)
	}
	return nil
}

func (l *Loop) runApplicationWorkflowSchedulesTick(ctx context.Context, checkPolicy bool, checks map[workflowSchedulePolicyKey]workflowSchedulePolicyCheck) error {
	store, ok := l.engine.Store().(state.WorkflowScheduleStore)
	if !ok {
		return nil
	}
	now := l.now()
	minute := now.Unix() / 60
	if minute != l.workflowScheduleMinute {
		l.workflowScheduleMinute = minute
		// Finish an incomplete pass before restarting at the beginning. A slow
		// pass may span minutes; resetting its cursor starves later apps.
		if l.workflowScheduleComplete {
			l.workflowScheduleAfter, l.workflowScheduleFailed = "", false
		}
		l.workflowScheduleComplete = false
	}
	if l.workflowScheduleComplete {
		return nil
	}
	candidates, err := store.ListWorkflowScheduleCandidates(ctx, l.engine.OwnerNodeID(), l.workflowScheduleAfter, workflowScheduleBatch)
	if err != nil {
		return err
	}
	var failures []error
	for _, candidate := range candidates {
		// A denied account must not consume its quota with runs that dispatch
		// cannot execute. The app cursor still advances past this candidate;
		// the schedule cursor is left intact for its missed-fire policy.
		if l.engine.capabilities != nil && !l.engine.capabilities.EveryAccount("workflows") && !l.workflowCapabilityAllowed(ctx, candidate.AppID) {
			continue
		}
		var definitions []api.WorkflowSpec
		if err := json.Unmarshal(candidate.Workflows, &definitions); err != nil {
			failures = append(failures, fmt.Errorf("workflow schedules: decode app %s: %w", candidate.AppID, err))
			continue
		}
		for _, definition := range definitions {
			if definition.Trigger == nil || definition.Trigger.Type != "schedule" {
				continue
			}
			if checkPolicy {
				allowed, err := l.workflowSchedulePolicyAllowed(ctx, candidate.AppID, definition.Name, checks)
				if err != nil {
					failures = append(failures, fmt.Errorf("workflow schedules: check policy %s/%s: %w", candidate.AppID, definition.Name, err))
					continue
				}
				if !allowed {
					continue
				}
			}
			cursor, changed, err := store.AdmitScheduledWorkflow(ctx, candidate.AppID, candidate.DeploymentID, definition.Name, now)
			if err != nil {
				failures = append(failures, fmt.Errorf("workflow schedules: admit %s/%s: %w", candidate.AppID, definition.Name, err))
				continue
			}
			if changed && l.log != nil {
				l.log.Info("workflow schedule evaluated", "app_id", candidate.AppID, "workflow", definition.Name,
					"status", cursor.Status, "run_id", cursor.LastRunID, "scheduled_for", cursor.ScheduledFor)
			}
		}
	}
	l.workflowScheduleFailed = l.workflowScheduleFailed || len(failures) > 0
	if len(candidates) < workflowScheduleBatch {
		l.workflowScheduleComplete = !l.workflowScheduleFailed
		l.workflowScheduleAfter, l.workflowScheduleFailed = "", false
	} else {
		l.workflowScheduleAfter = candidates[len(candidates)-1].AppID
	}
	return errors.Join(failures...)
}

func (l *Loop) runTenantWorkflowSchedulesTick(ctx context.Context, checkPolicy bool, checks map[workflowSchedulePolicyKey]workflowSchedulePolicyCheck) error {
	store, ok := l.engine.Store().(state.TenantWorkflowScheduleStore)
	if !ok {
		return nil
	}
	now := l.now()
	minute := now.Unix() / 60
	if minute != l.tenantWorkflowScheduleMinute {
		l.tenantWorkflowScheduleMinute = minute
		// Keep the keyset cursor across minute boundaries until this pass has
		// completed; otherwise early, non-due tenants can monopolize every pass.
		if l.tenantWorkflowScheduleComplete {
			l.tenantWorkflowScheduleAfterApp, l.tenantWorkflowScheduleAfterTenant = "", ""
			l.tenantWorkflowScheduleFairAfter = nil
			l.tenantWorkflowScheduleFailed = false
		}
		l.tenantWorkflowScheduleComplete = false
	}
	if l.tenantWorkflowScheduleComplete {
		return nil
	}
	var candidates []state.WorkflowScheduleCandidate
	var err error
	fair, fairScan := store.(state.FairTenantWorkflowScheduleStore)
	paged, pagedScan := store.(state.FairTenantWorkflowSchedulePageStore)
	if pagedScan {
		candidates, err = paged.ListFairTenantWorkflowScheduleCandidatesAfter(ctx, l.engine.OwnerNodeID(), now.UTC().Truncate(time.Minute), workflowScheduleBatch, l.tenantWorkflowScheduleFairAfter)
	} else if fairScan {
		candidates, err = fair.ListFairTenantWorkflowScheduleCandidates(ctx, l.engine.OwnerNodeID(), now.UTC().Truncate(time.Minute), workflowScheduleBatch)
	} else {
		candidates, err = store.ListTenantWorkflowScheduleCandidates(ctx, l.engine.OwnerNodeID(),
			l.tenantWorkflowScheduleAfterApp, l.tenantWorkflowScheduleAfterTenant, workflowScheduleBatch)
	}
	if err != nil {
		return err
	}
	var failures []error
	allowedByApp := make(map[string]bool)
	for _, candidate := range candidates {
		// Only the cursor-based scan can skip a denied candidate without
		// repeatedly selecting it ahead of other accounts.
		if pagedScan && l.engine.capabilities != nil && !l.engine.capabilities.EveryAccount("workflows") {
			allowed, seen := allowedByApp[candidate.AppID]
			if !seen {
				allowed = l.workflowCapabilityAllowed(ctx, candidate.AppID)
				allowedByApp[candidate.AppID] = allowed
			}
			if !allowed {
				continue
			}
		}
		var definitions []api.WorkflowSpec
		if err := json.Unmarshal(candidate.Workflows, &definitions); err != nil {
			failures = append(failures, fmt.Errorf("tenant workflow schedules: decode app %s: %w", candidate.AppID, err))
			continue
		}
		for _, definition := range definitions {
			if definition.Trigger == nil || definition.Trigger.Type != "schedule" {
				continue
			}
			if checkPolicy {
				allowed, err := l.workflowSchedulePolicyAllowed(ctx, candidate.AppID, definition.Name, checks)
				if err != nil {
					failures = append(failures, fmt.Errorf("tenant workflow schedules: check policy %s/%s: %w", candidate.AppID, definition.Name, err))
					continue
				}
				if !allowed {
					continue
				}
			}
			cursor, changed, err := store.AdmitTenantScheduledWorkflow(ctx, candidate.AppID, candidate.PlatformTenantID,
				candidate.DeploymentID, definition.Name, now)
			if err != nil {
				failures = append(failures, fmt.Errorf("tenant workflow schedules: admit %s/%s/%s: %w",
					candidate.AppID, candidate.PlatformTenantID, definition.Name, err))
				continue
			}
			if changed && l.log != nil {
				l.log.Info("tenant workflow schedule evaluated", "app_id", candidate.AppID,
					"platform_tenant_id", candidate.PlatformTenantID, "workflow", definition.Name,
					"status", cursor.Status, "run_id", cursor.LastRunID, "scheduled_for", cursor.ScheduledFor)
			}
		}
	}
	l.tenantWorkflowScheduleFailed = l.tenantWorkflowScheduleFailed || len(failures) > 0
	if len(candidates) < workflowScheduleBatch {
		l.tenantWorkflowScheduleComplete = !l.tenantWorkflowScheduleFailed
		l.tenantWorkflowScheduleFairAfter = nil
		if l.tenantWorkflowScheduleComplete {
			l.tenantWorkflowScheduleAfterApp, l.tenantWorkflowScheduleAfterTenant = "", ""
		} else {
			l.tenantWorkflowScheduleAfterApp, l.tenantWorkflowScheduleAfterTenant = "", ""
			l.tenantWorkflowScheduleFailed = false
		}
	} else if pagedScan {
		last := candidates[len(candidates)-1]
		l.tenantWorkflowScheduleFairAfter = &state.FairTenantScheduleCursor{AppID: last.AppID, TenantID: last.PlatformTenantID, WorkflowName: last.FairWorkflowName, LastAdmittedAt: last.FairLastAdmittedAt}
	} else if !fairScan {
		last := candidates[len(candidates)-1]
		l.tenantWorkflowScheduleAfterApp, l.tenantWorkflowScheduleAfterTenant = last.AppID, last.PlatformTenantID
	}
	return errors.Join(failures...)
}
