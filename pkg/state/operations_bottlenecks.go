package state

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type workflowBottleneckObservation struct {
	SLAWarningPercent int64                                  `json:"sla_warning_percent"`
	SLABudgetSeconds  int64                                  `json:"sla_budget_seconds"`
	History           api.OperationWorkflowStateHistoryEntry `json:"history"`
	Terminal          bool                                   `json:"terminal"`
}
type workflowStateDurationKey struct {
	Version int
	State   string
}
type workflowBlockerDurationKey struct {
	Version                int
	Operation, Code, Owner string
}
type workflowStateDurationBucket struct {
	api.OperationWorkflowStateDuration
	micros int64
}
type workflowBlockerDurationBucket struct {
	api.OperationWorkflowBlockerDuration
	micros int64
}

func workflowBottleneckHistoryLess(a, b workflowBottleneckObservation) bool {
	if a.History.Revision != b.History.Revision {
		return a.History.Revision < b.History.Revision
	}
	if a.History.OperationID != b.History.OperationID {
		return a.History.OperationID < b.History.OperationID
	}
	return a.History.ID < b.History.ID
}
func workflowBottleneckBlockerKey(h api.OperationWorkflowStateHistoryEntry, b api.OperationWorkflowBlocker) workflowBlockerDurationKey {
	return workflowBlockerDurationKey{Version: effectiveWorkflowContractVersion(h.ContractVersion), Operation: b.Operation, Code: b.Code, Owner: b.Owner}
}

// Durations cover only contiguous observed intervals. Unknown ranges are never filled.
func workflowBottlenecks(observations []workflowBottleneckObservation, current *api.OperationWorkflowState, verifications []api.OperationWorkflowResolutionVerification, at time.Time) *api.OperationWorkflowBottlenecks {
	return calculateWorkflowBottlenecks(observations, current, verifications, at, true)
}

func calculateWorkflowBottlenecks(observations []workflowBottleneckObservation, current *api.OperationWorkflowState, verifications []api.OperationWorkflowResolutionVerification, at time.Time, limitGroups bool) *api.OperationWorkflowBottlenecks {
	out := &api.OperationWorkflowBottlenecks{EvaluatedAt: at, IncompleteReasons: []string{}, States: []api.OperationWorkflowStateDuration{}, Blockers: []api.OperationWorkflowBlockerDuration{}, VerificationOwners: []api.OperationWorkflowVerificationDuration{}}
	reasons := map[string]bool{}
	sort.Slice(observations, func(i, j int) bool { return workflowBottleneckHistoryLess(observations[j], observations[i]) })
	if len(observations) > api.OperationWorkflowBottleneckHistoryMax {
		observations = observations[:api.OperationWorkflowBottleneckHistoryMax]
		out.HistoryTruncated = true
		reasons["history_window_truncated"] = true
	}
	sort.Slice(observations, func(i, j int) bool { return workflowBottleneckHistoryLess(observations[i], observations[j]) })
	out.ReportsInWindow = len(observations)
	if len(observations) == 0 || observations[0].History.Revision != 1 {
		reasons["missing_start"] = true
	}
	if current != nil {
		out.Ongoing = !current.Terminal
	}
	states := map[workflowStateDurationKey]*workflowStateDurationBucket{}
	blockers := map[workflowBlockerDurationKey]*workflowBlockerDurationBucket{}
	reports := map[string]api.OperationWorkflowStateHistoryEntry{}
	revisions := map[int64]int{}
	for _, o := range observations {
		revisions[o.History.Revision]++
	}
	validTimes := make([]bool, len(observations))
	var highWater time.Time
	for i, o := range observations {
		h := o.History
		reports[h.OperationID+"/"+h.ID] = h
		key := workflowStateDurationKey{Version: effectiveWorkflowContractVersion(h.ContractVersion), State: h.State}
		if states[key] == nil {
			states[key] = &workflowStateDurationBucket{OperationWorkflowStateDuration: api.OperationWorkflowStateDuration{ContractVersion: key.Version, State: key.State}}
		}
		states[key].ObservationCount++
		for _, b := range h.Blockers {
			key := workflowBottleneckBlockerKey(h, b)
			if blockers[key] == nil {
				blockers[key] = &workflowBlockerDurationBucket{OperationWorkflowBlockerDuration: api.OperationWorkflowBlockerDuration{ContractVersion: key.Version, Operation: key.Operation, Code: key.Code, Owner: key.Owner}}
			}
			blockers[key].ObservationCount++
		}
		if revisions[h.Revision] > 1 {
			reasons["duplicate_revision"] = true
		}
		if h.OccurredAt.IsZero() || h.OccurredAt.Before(highWater) {
			reasons["out_of_order_time"] = true
		} else if h.OccurredAt.After(at) {
			reasons["future_observation"] = true
		} else {
			validTimes[i] = true
			if out.ObservedFrom == nil {
				value := h.OccurredAt
				out.ObservedFrom = &value
			}
			value := h.OccurredAt
			out.ObservedThrough = &value
		}
		if h.OccurredAt.After(highWater) {
			highWater = h.OccurredAt
		}
	}
	var stateMicros, blockedMicros int64
	addInterval := func(o workflowBottleneckObservation, end time.Time, ongoing bool) {
		h := o.History
		if o.Terminal {
			return
		}
		micros := end.UnixMicro() - h.OccurredAt.UnixMicro()
		if micros < 0 {
			return
		}
		stateMicros += micros
		stateKey := workflowStateDurationKey{Version: effectiveWorkflowContractVersion(h.ContractVersion), State: h.State}
		states[stateKey].micros += micros
		states[stateKey].Ongoing = states[stateKey].Ongoing || ongoing
		if len(h.Blockers) > 0 {
			blockedMicros += micros
		}
		for _, b := range h.Blockers {
			bucket := blockers[workflowBottleneckBlockerKey(h, b)]
			bucket.micros += micros
			bucket.Ongoing = bucket.Ongoing || ongoing
		}
	}
	for i := 0; i+1 < len(observations); i++ {
		left, right := observations[i].History, observations[i+1].History
		contiguous := right.Revision == left.Revision+1 && revisions[left.Revision] == 1 && revisions[right.Revision] == 1
		if right.Revision > left.Revision+1 {
			reasons["revision_gap"] = true
		}
		if effectiveWorkflowContractVersion(left.ContractVersion) != effectiveWorkflowContractVersion(right.ContractVersion) {
			reasons["contract_changed"] = true
			contiguous = false
		}
		if right.FromState != "" && right.FromState != left.State {
			reasons["state_discontinuity"] = true
			contiguous = false
		}
		if contiguous && validTimes[i] && validTimes[i+1] {
			addInterval(observations[i], right.OccurredAt, false)
		}
	}
	latestMatches := false
	if len(observations) > 0 && current != nil {
		last := observations[len(observations)-1]
		h := last.History
		latestMatches = h.OperationID == current.OperationID && h.ID == current.ReportID && h.Revision == current.Revision && h.State == current.State && last.Terminal == current.Terminal && effectiveWorkflowContractVersion(h.ContractVersion) == effectiveWorkflowContractVersion(current.ContractVersion)
		if latestMatches && validTimes[len(observations)-1] && revisions[h.Revision] == 1 {
			addInterval(last, at, !current.Terminal)
			if !last.Terminal {
				value := at
				out.ObservedThrough = &value
			}
		}
	}
	if !latestMatches {
		reasons["missing_latest"] = true
	}
	out.StateSeconds = stateMicros / 1_000_000
	out.BlockedSeconds = blockedMicros / 1_000_000
	for _, bucket := range states {
		bucket.ObservedSeconds = bucket.micros / 1_000_000
		out.States = append(out.States, bucket.OperationWorkflowStateDuration)
	}
	for _, bucket := range blockers {
		bucket.ObservedSeconds = bucket.micros / 1_000_000
		out.Blockers = append(out.Blockers, bucket.OperationWorkflowBlockerDuration)
	}
	owners := map[string]*api.OperationWorkflowVerificationDuration{}
	for _, v := range verifications {
		owner := v.Resolution.VerificationOwner
		if owners[owner] == nil {
			owners[owner] = &api.OperationWorkflowVerificationDuration{Owner: owner}
		}
		group := owners[owner]
		group.ResolutionCount++
		if v.Status == "awaiting_verification" {
			group.PendingCount++
		}
		h, known := reports[v.ResolutionOperationID+"/"+v.ResolutionReportID]
		if !known || h.PublishedAt.IsZero() || h.PublishedAt.After(at) {
			group.UnknownStartCount++
			out.VerificationUnknownStartCount++
			reasons["verification_start_missing"] = true
			continue
		}
		end := at
		if v.VerifiedAt != nil && v.VerifiedAt.Before(end) {
			end = *v.VerifiedAt
		}
		var seconds int64
		if end.After(h.PublishedAt) {
			seconds = (end.UnixMicro() - h.PublishedAt.UnixMicro()) / 1_000_000
		}
		group.ObservedSeconds += seconds
		out.VerificationWaitSeconds += seconds
	}
	for _, group := range owners {
		out.VerificationOwners = append(out.VerificationOwners, *group)
	}
	sort.Slice(out.States, func(i, j int) bool {
		a, b := out.States[i], out.States[j]
		if a.ObservedSeconds != b.ObservedSeconds {
			return a.ObservedSeconds > b.ObservedSeconds
		}
		if a.ContractVersion != b.ContractVersion {
			return a.ContractVersion < b.ContractVersion
		}
		return a.State < b.State
	})
	sort.Slice(out.Blockers, func(i, j int) bool {
		a, b := out.Blockers[i], out.Blockers[j]
		if a.ObservedSeconds != b.ObservedSeconds {
			return a.ObservedSeconds > b.ObservedSeconds
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
		if a.ObservedSeconds != b.ObservedSeconds {
			return a.ObservedSeconds > b.ObservedSeconds
		}
		return a.Owner < b.Owner
	})
	if limitGroups && len(out.States) > api.OperationWorkflowBottleneckGroupsMax {
		out.States = out.States[:api.OperationWorkflowBottleneckGroupsMax]
		out.StatesTruncated = true
	}
	if limitGroups && len(out.Blockers) > api.OperationWorkflowBottleneckGroupsMax {
		out.Blockers = out.Blockers[:api.OperationWorkflowBottleneckGroupsMax]
		out.BlockersTruncated = true
	}
	if limitGroups && len(out.VerificationOwners) > api.OperationWorkflowBottleneckGroupsMax {
		out.VerificationOwners = out.VerificationOwners[:api.OperationWorkflowBottleneckGroupsMax]
		out.VerificationOwnersTruncated = true
	}
	for reason := range reasons {
		out.IncompleteReasons = append(out.IncompleteReasons, reason)
	}
	sort.Strings(out.IncompleteReasons)
	out.HistoryComplete = len(reasons) == 0
	return out
}

func (m *MemStore) workflowBottleneckHistoryLocked(account, tenant, app, scope string, subject api.OperationSubject, workflow, instance string, at time.Time) []workflowBottleneckObservation {
	return m.workflowBottleneckHistoryAtLocked(account, tenant, app, scope, subject, workflow, instance, at, at)
}
func (m *MemStore) workflowBottleneckHistoryAtLocked(account, tenant, app, scope string, subject api.OperationSubject, workflow, instance string, at, retentionAt time.Time) []workflowBottleneckObservation {
	data := m.operationMemoryLocked()
	var observations []workflowBottleneckObservation
	for _, receipt := range data.workflowStateReports {
		h := receipt.History
		op, ok := data.operations[h.OperationID]
		if !ok || !operationRetained(op, retentionAt) || !sameOperationHistoryIdentity(op.AccountID, account) || !sameOperationHistoryIdentity(op.AppID, app) || !sameOperationHistoryIdentity(op.PlatformTenantID, tenant) || op.Scope != scope || op.Subject == nil || *op.Subject != subject || h.Workflow != workflow || h.InstanceID != instance || h.PublishedAt.After(at) {
			continue
		}
		terminal := false
		var slaBudget, slaWarning int64
		for _, step := range data.definitions[op.DefinitionID].Spec.WorkflowSteps {
			if step.Workflow != workflow || effectiveWorkflowContractVersion(step.Version) != effectiveWorkflowContractVersion(h.ContractVersion) {
				continue
			}
			slaBudget = step.StateSLABudgetSeconds[h.State]
			slaWarning = step.StateSLAWarningPercent[h.State]
			for _, state := range step.TerminalStates {
				if state == h.State {
					terminal = true
				}
			}
		}
		observations = append(observations, workflowBottleneckObservation{History: h, Terminal: terminal, SLABudgetSeconds: slaBudget, SLAWarningPercent: slaWarning})
	}
	return observations
}
