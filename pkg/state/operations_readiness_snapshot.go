package state

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func readinessDigest(value any) string {
	raw, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

// Set digests ignore source ordering while retaining every finding, including beyond previews.
func readinessSetDigest[T any](values []T) string {
	encoded := make([]string, 0, len(values))
	for _, v := range values {
		raw, _ := json.Marshal(v)
		encoded = append(encoded, string(raw))
	}
	sort.Strings(encoded)
	return readinessDigest(encoded)
}
func newReadinessSnapshot(account, tenant string, opts api.OperationWorkflowAttentionOptions, operator bool) api.OperationWorkflowReadinessSnapshot {
	if operator {
		tenant = opts.TenantID
	} else {
		tenant = uuid.MustParse(tenant).String()
	}
	return api.OperationWorkflowReadinessSnapshot{Version: 1, Query: readinessDigest([]any{uuid.MustParse(account).String(), opts.AppID, opts.Scope, tenant, operator}), Items: []api.OperationWorkflowReadinessSnapshotEntry{}}
}
func readinessSnapshotKey(row api.OperationWorkflowReadinessSnapshotEntry) string {
	return readinessDigest([]any{row.PlatformTenantID, row.Subject, row.Workflow, row.InstanceID, row.Operation, row.FromState, row.ToState})
}
func readinessSnapshotEntry(row api.OperationWorkflowReadinessQueueEntry, instance *api.OperationWorkflowInstanceSnapshot, r api.OperationWorkflowTransitionReadiness) api.OperationWorkflowReadinessSnapshotEntry {
	codes := []string{}
	for _, b := range r.Blockers {
		if !slices.Contains(codes, b.Code) {
			codes = append(codes, b.Code)
		}
	}
	sort.Strings(codes)
	missingMilestones := append([]string{}, row.MissingMilestones...)
	sort.Strings(missingMilestones)
	missingDependencies := append([]string{}, row.MissingDependencyWorkflows...)
	sort.Strings(missingDependencies)
	verification := selectedVerifications(instance.ReadinessVerifications, api.OperationWorkflowAttentionOptions{TargetOperation: r.Transition.Operation})
	advisories := []string{}
	for _, a := range row.Advisories {
		if a != "verification_pending" {
			advisories = append(advisories, a)
		}
	}
	sort.Strings(advisories)
	reporting := []any{row.ReportingStatus, row.MissingReportCount, row.OverdueReportCount}
	if r.Reporting != nil {
		reporting = []any{r.Reporting.Status, r.Reporting.ExpectedConditionCount, r.Reporting.ReportedConditionCount, r.Reporting.MissingConditionCount, r.Reporting.OverdueConditionCount}
	}
	return api.OperationWorkflowReadinessSnapshotEntry{
		PlatformTenantID: row.PlatformTenantID, Subject: row.Subject, Workflow: row.Workflow, InstanceID: row.InstanceID, OperationID: row.OperationID, StateRevision: row.StateRevision, ContractVersion: row.ContractVersion, Operation: row.Transition.Operation, FromState: row.Transition.From, ToState: row.Transition.To, EvaluatedAt: row.EvaluatedAt, Ready: row.Ready, Reasons: append([]string{}, row.Reasons...), Advisories: append([]string{}, row.Advisories...), BlockerCodes: codes, MissingMilestones: missingMilestones, MissingDependencyWorkflows: missingDependencies, PendingVerificationCount: row.PendingVerificationCount,
		RequirementsFingerprint: readinessDigest([]any{row.ContractVersion, row.Transition}), BlockersFingerprint: readinessSetDigest(row.Blockers), PrerequisitesFingerprint: readinessPrerequisitesDigest(r), VerificationsFingerprint: readinessSetDigest(verification), ReportingFingerprint: readinessDigest([]any{reporting, advisories}),
	}
}
func readinessRetainedEvidence(r api.OperationWorkflowRetainedMilestone) []any {
	var freshness any
	if r.Freshness != nil {
		freshness = []any{r.Freshness.MaxAgeSeconds, r.Freshness.ExpiresAt}
	}
	// Evaluation clocks and elapsed ages do not constitute an evidence change.
	return []any{r.Requirement, r.Status, r.Evidence, r.Invalidation, freshness}
}
func readinessPrerequisitesDigest(r api.OperationWorkflowTransitionReadiness) string {
	facts := []any{r.MissingMilestones, r.MissingDependencyWorkflows, r.MissingPolicies, r.UnmetInvariants, r.UnmetEffects}
	for _, dependency := range r.UnmetDependencies {
		var state any
		if dependency.State != nil {
			s := dependency.State
			state = []any{s.OperationID, s.ReportID, s.OutcomeCode, s.Terminal}
		}
		facts = append(facts, []any{"dependency", dependency.Dependency, dependency.Status, state})
	}
	for _, fact := range r.RetainedMilestones {
		facts = append(facts, readinessRetainedEvidence(fact))
	}
	for _, group := range r.RetainedMilestoneGroups {
		facts = append(facts, []any{"group", group.ID, group.Satisfied, group.SelectedPathID})
		for _, fact := range group.Alternatives {
			facts = append(facts, []any{group.ID, readinessRetainedEvidence(fact)})
		}
		for _, path := range group.Paths {
			facts = append(facts, []any{group.ID, path.ID, path.Satisfied, path.Decision})
			for _, fact := range path.Requirements {
				facts = append(facts, []any{group.ID, path.ID, readinessRetainedEvidence(fact)})
			}
		}
	}
	return readinessSetDigest(facts)
}
func finalizeReadinessQueueSnapshot(out *api.OperationWorkflowReadinessQueueResponse) {
	byKey := map[string]api.OperationWorkflowReadinessSnapshotEntry{}
	for _, entry := range out.Snapshot.Items {
		byKey[readinessSnapshotKey(entry)] = entry
	}
	out.Snapshot.Items = []api.OperationWorkflowReadinessSnapshotEntry{}
	for _, row := range out.Items {
		key := readinessSnapshotKey(api.OperationWorkflowReadinessSnapshotEntry{PlatformTenantID: row.PlatformTenantID, Subject: row.Subject, Workflow: row.Workflow, InstanceID: row.InstanceID, Operation: row.Transition.Operation, FromState: row.Transition.From, ToState: row.Transition.To})
		out.Snapshot.Items = append(out.Snapshot.Items, byKey[key])
	}
	out.Snapshot.CapturedAt = out.EvaluatedAt
}
