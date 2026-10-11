package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// OperationWorkflowPerformanceStore summarizes retained business workflow durations.
type OperationWorkflowPerformanceStore interface {
	SummarizeAccountWorkflowPerformance(context.Context, string, api.OperationWorkflowPerformanceOptions) (api.OperationWorkflowPerformanceSummary, error)
	SummarizePlatformTenantWorkflowPerformance(context.Context, string, string, api.OperationWorkflowPerformanceOptions) (api.OperationWorkflowPerformanceSummary, error)
}

type workflowPerformanceInstance struct {
	SLAConfigured            bool                                          `json:"sla_configured"`
	CurrentSLAWarningPercent int64                                         `json:"current_sla_warning_percent"`
	CurrentSLABudgetSeconds  int64                                         `json:"current_sla_budget_seconds"`
	Current                  api.OperationWorkflowState                    `json:"current"`
	Tenant                   string                                        `json:"tenant"`
	Subject                  api.OperationSubject                          `json:"subject"`
	CohortTotal              int64                                         `json:"cohort_total"`
	Observations             []workflowBottleneckObservation               `json:"observations"`
	Verifications            []api.OperationWorkflowResolutionVerification `json:"verifications"`
	Bottlenecks              *api.OperationWorkflowBottlenecks             `json:"-"`
	Key                      string                                        `json:"-"`
}

func prepareWorkflowPerformance(account, tenant string, opts api.OperationWorkflowPerformanceOptions, operator bool) (api.OperationWorkflowPerformanceOptions, time.Time, error) {
	base, c, err := prepareOperationAttention(account, tenant, api.OperationWorkflowAttentionOptions{AppID: opts.AppID, Scope: opts.Scope, TenantID: opts.TenantID, Workflow: opts.Workflow}, operator, "workflow-performance")
	if err != nil || opts.Workflow == "" {
		return opts, c.EvaluatedAt, ErrInvalidArgument
	}
	opts.AppID, opts.TenantID = base.AppID, base.TenantID
	return opts, c.EvaluatedAt, nil
}

// Nearest-rank percentiles use one accumulated duration per eligible instance.
func workflowDurationDistribution(values []int64) api.OperationWorkflowDurationDistribution {
	out := api.OperationWorkflowDurationDistribution{WorkflowCount: int64(len(values))}
	if len(values) == 0 {
		return out
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	for _, n := range values {
		out.TotalSeconds += n
	}
	out.P50Seconds = values[(len(values)+1)/2-1]
	out.P95Seconds = values[(95*len(values)+99)/100-1]
	return out
}

func workflowPerformanceCohort(instances []workflowPerformanceInstance, at time.Time) api.OperationWorkflowPerformanceCohort {
	out := api.OperationWorkflowPerformanceCohort{Exclusions: []api.OperationWorkflowPerformanceCoverageReason{}, States: []api.OperationWorkflowStatePerformance{}, Blockers: []api.OperationWorkflowBlockerPerformance{}, VerificationOwners: []api.OperationWorkflowVerificationPerformance{}}
	if len(instances) > 0 {
		out.MatchingWorkflowCount = instances[0].CohortTotal
	}
	out.SampledWorkflowCount = int64(len(instances))
	out.CohortTruncated = out.MatchingWorkflowCount > out.SampledWorkflowCount
	reasons := map[string]int64{}
	slaVisits := map[workflowStateDurationKey]workflowSLAVisits{}
	states := map[workflowStateDurationKey][]int64{}
	blockers := map[workflowBlockerDurationKey][]int64{}
	owners := map[string][]int64{}
	pending := map[string]int64{}
	var stateTime, blockedTime, verificationWait []int64
	for _, instance := range instances {
		b := instance.Bottlenecks
		if b == nil {
			b = calculateWorkflowBottlenecks(instance.Observations, &instance.Current, instance.Verifications, at, false)
		}
		configured := instance.SLAConfigured
		for _, o := range instance.Observations {
			if o.SLABudgetSeconds > 0 {
				configured = true
				break
			}
		}
		if configured {
			out.SLAConfiguredWorkflowCount++
		}
		if !b.HistoryComplete {
			if configured {
				out.SLAUnknownWorkflowCount++
			}
			out.ExcludedIncompleteWorkflowCount++
			for _, reason := range b.IncompleteReasons {
				reasons[reason]++
			}
			continue
		}
		if configured {
			visits, known := workflowPerformanceSLAVisits(instance, at)
			if !known {
				out.SLAUnknownWorkflowCount++
			} else {
				out.SLAEvaluatedWorkflowCount++
				breached := false
				for key, count := range visits {
					total := slaVisits[key]
					total.Evaluated += count.Evaluated
					total.Breached += count.Breached
					slaVisits[key] = total
					if count.Breached > 0 {
						breached = true
					}
				}
				if breached {
					out.SLABreachedWorkflowCount++
				}
			}
		}
		out.CompleteHistoryWorkflowCount++
		stateTime = append(stateTime, b.StateSeconds)
		blockedTime = append(blockedTime, b.BlockedSeconds)
		verificationWait = append(verificationWait, b.VerificationWaitSeconds)
		for _, g := range b.States {
			key := workflowStateDurationKey{Version: g.ContractVersion, State: g.State}
			states[key] = append(states[key], g.ObservedSeconds)
		}
		for _, g := range b.Blockers {
			key := workflowBlockerDurationKey{Version: g.ContractVersion, Operation: g.Operation, Code: g.Code, Owner: g.Owner}
			blockers[key] = append(blockers[key], g.ObservedSeconds)
		}
		for _, g := range b.VerificationOwners {
			owners[g.Owner] = append(owners[g.Owner], g.ObservedSeconds)
			pending[g.Owner] += g.PendingCount
		}
	}
	out.StateTime = workflowDurationDistribution(stateTime)
	out.BlockedTime = workflowDurationDistribution(blockedTime)
	out.VerificationWait = workflowDurationDistribution(verificationWait)
	for reason, count := range reasons {
		out.Exclusions = append(out.Exclusions, api.OperationWorkflowPerformanceCoverageReason{Reason: reason, WorkflowCount: count})
	}
	sort.Slice(out.Exclusions, func(i, j int) bool { return out.Exclusions[i].Reason < out.Exclusions[j].Reason })
	for key, values := range states {
		out.States = append(out.States, api.OperationWorkflowStatePerformance{SLAEvaluatedVisitCount: slaVisits[key].Evaluated, SLABreachedVisitCount: slaVisits[key].Breached, ContractVersion: key.Version, State: key.State, Duration: workflowDurationDistribution(values)})
	}
	for key, values := range blockers {
		out.Blockers = append(out.Blockers, api.OperationWorkflowBlockerPerformance{ContractVersion: key.Version, Operation: key.Operation, Code: key.Code, Owner: key.Owner, Duration: workflowDurationDistribution(values)})
	}
	for owner, values := range owners {
		out.VerificationOwners = append(out.VerificationOwners, api.OperationWorkflowVerificationPerformance{Owner: owner, PendingResolutionCount: pending[owner], Duration: workflowDurationDistribution(values)})
	}
	sort.Slice(out.States, func(i, j int) bool {
		a, b := out.States[i], out.States[j]
		if a.Duration.TotalSeconds != b.Duration.TotalSeconds {
			return a.Duration.TotalSeconds > b.Duration.TotalSeconds
		}
		if a.ContractVersion != b.ContractVersion {
			return a.ContractVersion < b.ContractVersion
		}
		return a.State < b.State
	})
	sort.Slice(out.Blockers, func(i, j int) bool {
		a, b := out.Blockers[i], out.Blockers[j]
		if a.Duration.TotalSeconds != b.Duration.TotalSeconds {
			return a.Duration.TotalSeconds > b.Duration.TotalSeconds
		}
		if a.ContractVersion != b.ContractVersion {
			return a.ContractVersion < b.ContractVersion
		}
		if a.Operation != b.Operation {
			return a.Operation < b.Operation
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Owner < b.Owner
	})
	sort.Slice(out.VerificationOwners, func(i, j int) bool {
		a, b := out.VerificationOwners[i], out.VerificationOwners[j]
		if a.Duration.TotalSeconds != b.Duration.TotalSeconds {
			return a.Duration.TotalSeconds > b.Duration.TotalSeconds
		}
		return a.Owner < b.Owner
	})
	if len(out.States) > api.OperationWorkflowBottleneckGroupsMax {
		out.States = out.States[:api.OperationWorkflowBottleneckGroupsMax]
		out.StatesTruncated = true
	}
	if len(out.Blockers) > api.OperationWorkflowBottleneckGroupsMax {
		out.Blockers = out.Blockers[:api.OperationWorkflowBottleneckGroupsMax]
		out.BlockersTruncated = true
	}
	if len(out.VerificationOwners) > api.OperationWorkflowBottleneckGroupsMax {
		out.VerificationOwners = out.VerificationOwners[:api.OperationWorkflowBottleneckGroupsMax]
		out.VerificationOwnersTruncated = true
	}
	return out
}
func workflowPerformanceSummary(instances []workflowPerformanceInstance, opts api.OperationWorkflowPerformanceOptions, at time.Time) api.OperationWorkflowPerformanceSummary {
	completed, ongoing := []workflowPerformanceInstance{}, []workflowPerformanceInstance{}
	for _, instance := range instances {
		if instance.Current.Terminal {
			completed = append(completed, instance)
		} else {
			ongoing = append(ongoing, instance)
		}
	}
	return api.OperationWorkflowPerformanceSummary{EvaluatedAt: at, Workflow: opts.Workflow, CohortLimit: api.OperationWorkflowPerformanceCohortMax, Completed: workflowPerformanceCohort(completed, at), Ongoing: workflowPerformanceCohort(ongoing, at)}
}

func (m *MemStore) SummarizeAccountWorkflowPerformance(ctx context.Context, account string, opts api.OperationWorkflowPerformanceOptions) (api.OperationWorkflowPerformanceSummary, error) {
	return m.summarizeWorkflowPerformance(ctx, account, opts.TenantID, opts, true)
}
func (m *MemStore) SummarizePlatformTenantWorkflowPerformance(ctx context.Context, account, tenant string, opts api.OperationWorkflowPerformanceOptions) (api.OperationWorkflowPerformanceSummary, error) {
	return m.summarizeWorkflowPerformance(ctx, account, tenant, opts, false)
}
func (m *MemStore) summarizeWorkflowPerformance(ctx context.Context, account, tenant string, opts api.OperationWorkflowPerformanceOptions, operator bool) (api.OperationWorkflowPerformanceSummary, error) {
	opts, at, err := prepareWorkflowPerformance(account, tenant, opts, operator)
	if err != nil {
		return api.OperationWorkflowPerformanceSummary{}, err
	}
	instances, err := m.readWorkflowPerformance(ctx, account, tenant, opts, operator, at)
	if err != nil {
		return api.OperationWorkflowPerformanceSummary{}, err
	}
	out := workflowPerformanceSummary(instances, opts, at)
	out.CohortToken = workflowPerformanceToken(account, tenant, opts, operator, instances, at)
	return out, nil
}

func (m *MemStore) readWorkflowPerformance(ctx context.Context, account, tenant string, opts api.OperationWorkflowPerformanceOptions, operator bool, at time.Time) ([]workflowPerformanceInstance, error) {
	if operator {
		tenant = opts.TenantID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	retentionAt := time.Now().UTC()
	data := m.operationMemoryLocked()
	instances := []workflowPerformanceInstance{}
	for _, record := range data.workflowStates {
		if !sameOperationHistoryIdentity(record.AccountID, account) || !sameOperationHistoryIdentity(record.AppID, opts.AppID) || record.Scope != opts.Scope || tenant != "" && !sameOperationHistoryIdentity(record.TenantID, tenant) || record.State.Workflow != opts.Workflow || record.State.UpdatedAt.After(at) {
			continue
		}
		receipt, ok := data.workflowStateReports[record.OperationID+"/"+record.ReportID]
		if !ok || receipt.History.PublishedAt.After(at) {
			continue
		}
		op, ok := data.operations[record.OperationID]
		if !ok || !operationRetained(op, retentionAt) || op.Subject == nil {
			continue
		}
		current := record.State
		current.Stale = operationWorkflowStateIsStale(at, current)
		evaluateOperationWorkflowDeadline(at, &current)
		instances = append(instances, workflowPerformanceInstance{Current: current, Tenant: record.TenantID, Subject: *op.Subject, Key: operationAttentionKey(record.TenantID, *op.Subject, record.State.Workflow, record.State.InstanceID)})
	}
	sort.Slice(instances, func(i, j int) bool {
		if !instances[i].Current.UpdatedAt.Equal(instances[j].Current.UpdatedAt) {
			return instances[i].Current.UpdatedAt.After(instances[j].Current.UpdatedAt)
		}
		return instances[i].Key > instances[j].Key
	})
	counts := map[bool]int64{}
	selected := map[bool]int{}
	for _, i := range instances {
		counts[i.Current.Terminal]++
	}
	sampled := []workflowPerformanceInstance{}
	for _, i := range instances {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if selected[i.Current.Terminal] >= api.OperationWorkflowPerformanceCohortMax {
			continue
		}
		selected[i.Current.Terminal]++
		i.SLAConfigured = workflowSLAConfigured(data.definitions[data.operations[i.Current.OperationID].DefinitionID].Spec, i.Current)
		i.CurrentSLAWarningPercent = workflowStateSLAWarning(data.definitions[data.operations[i.Current.OperationID].DefinitionID].Spec, i.Current)
		i.CurrentSLABudgetSeconds = workflowStateSLABudget(data.definitions[data.operations[i.Current.OperationID].DefinitionID].Spec, i.Current)
		i.CohortTotal = counts[i.Current.Terminal]
		i.Observations = m.workflowBottleneckHistoryAtLocked(account, i.Tenant, opts.AppID, opts.Scope, i.Subject, opts.Workflow, i.Current.InstanceID, at, retentionAt)
		if len(i.Observations) <= api.OperationWorkflowBottleneckHistoryMax {
			i.Verifications = m.workflowVerificationsLocked(account, i.Tenant, opts.AppID, opts.Scope, i.Subject, opts.Workflow, i.Current.InstanceID, at, retentionAt)
		}
		i.Current.SLA = workflowStateSLA(i.Current, i.Observations, i.CurrentSLABudgetSeconds, i.CurrentSLAWarningPercent, at)
		sampled = append(sampled, i)
	}
	return sampled, nil
}
