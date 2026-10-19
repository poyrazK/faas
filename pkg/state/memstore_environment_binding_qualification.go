package state

import (
	"context"
	"fmt"
	"time"
)

var _ EnvironmentQualificationServiceReceiptStore = (*MemStore)(nil)

func (m *MemStore) candidateBindingCatalogSupportedLocked(request EnvironmentWorkloadQualificationRequest) bool {
	appID, scope := request.AppID, request.FrozenInputs.Scope
	// Managed PostgreSQL and object storage are qualified by credential
	// receipts (ADR-20261019072523, ADR-20261019121546).
	for _, b := range m.outboundAppBindings {
		if b.AppID == appID {
			return false
		}
	}
	for _, binding := range m.queueBindings {
		if binding.AppID == appID && (normalizedDeploymentScope(binding.DeploymentScope) == scope || binding.DeploymentScope == "") && binding.RetiredAt == nil {
			found := false
			for _, b := range request.FrozenInputs.QueueBindings {
				found = found || stageCanonicalID(b.BindingID) == stageCanonicalID(binding.ID)
			}
			if !found {
				return false
			}
		}
	}
	return true
}

func (m *MemStore) RecordEnvironmentQualificationServiceExchange(ctx context.Context, request EnvironmentQualificationServiceRequest, original EnvironmentQualificationServiceRoute) error {
	if !qualificationServiceRequestValid(request) {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.resolveEnvironmentQualificationServiceLocked(request)
	if err != nil {
		return err
	}
	if !qualificationServiceExchangeMatches(current, original) {
		return ErrConflict
	}
	memory, caller, err := m.qualificationLocked(original.Caller.RequestID)
	if err != nil {
		return err
	}
	if caller.BindingContext == nil {
		return nil
	} // no enforced qualification requested
	revision, err := m.serviceBindingRevisionLocked(memory.source.AccountID)
	if err != nil {
		return err
	}
	if !candidateBindingContextCurrent(caller, m.deployments[caller.DeploymentID], m.bindingReleasePolicyLocked(caller.AppID, caller.FrozenInputs.Scope), revision) {
		return ErrBindingReleaseRequired
	}
	if memory.bindingServices == nil {
		memory.bindingServices = map[string]EnvironmentQualificationServiceReceipt{}
	}
	key := qualificationServiceReceiptKey(caller.ID, caller.Attempt, request.Binding)
	if _, exists := memory.bindingServices[key]; !exists {
		memory.bindingServices[key] = EnvironmentQualificationServiceReceipt{Binding: request.Binding, Route: original, RecordedAt: time.Now().UTC()}
	}
	return nil
}

// Called under the single publication lock, before any candidate is unheld.
func (m *MemStore) checkEnvironmentBindingGraphLocked(memory *environmentGitOpsMemory, graph EnvironmentWorkloadGraph, members []ProjectReleaseMember) error {
	_, err := m.environmentBindingGraphDeadlineLocked(memory, graph, members)
	return err
}
func (m *MemStore) environmentBindingGraphDeadlineLocked(memory *environmentGitOpsMemory, graph EnvironmentWorkloadGraph, members []ProjectReleaseMember) (time.Time, error) {
	if m.rejectUncheckedBindingReleaseGraphLocked(memory.source.ProjectID, memory.source.EnvironmentSlug) == nil {
		return time.Time{}, nil
	}
	if !candidateBindingMembersMatch(graph, members) {
		return time.Time{}, ErrBindingReleaseRequired
	}
	revision, err := m.serviceBindingRevisionLocked(memory.source.AccountID)
	if err != nil {
		return time.Time{}, err
	}
	requests := map[string]EnvironmentWorkloadQualificationRequest{}
	for _, r := range memory.qualifications {
		if r.GraphID == graph.ID {
			requests[r.AppID] = r
		}
	}
	if len(requests) != len(members) {
		return time.Time{}, ErrBindingReleaseRequired
	}
	var deadline time.Time
	for _, member := range members {
		r := requests[member.AppID]
		if r.DeploymentID != member.DeploymentID {
			return time.Time{}, fmt.Errorf("candidate binding member changed: %w", ErrBindingReleaseRequired)
		}
		if !m.candidateBindingCatalogSupportedLocked(r) {
			return time.Time{}, fmt.Errorf("candidate binding catalog is unsupported: %w", ErrBindingReleaseRequired)
		}
		if !candidateBindingContextCurrent(r, m.deployments[r.DeploymentID], m.bindingReleasePolicyLocked(r.AppID, r.FrozenInputs.Scope), revision) {
			return time.Time{}, fmt.Errorf("candidate binding artifact or policy changed: %w", ErrBindingReleaseRequired)
		}
		smoke := m.qualificationSmokeReceipts[qualificationSmokeReceiptKey(r.ID, r.Attempt)]
		end, err := candidateBindingEvidenceDeadline(r, smoke, memory.bindingServices, requests, time.Now())
		if err != nil {
			return time.Time{}, fmt.Errorf("candidate binding evidence is missing or expired: %w", err)
		}
		age, err := time.ParseDuration(r.BindingContext.Policy.MaxVerificationAge)
		if err != nil {
			return time.Time{}, ErrBindingReleaseRequired
		}
		restore := m.qualificationRestoreReceipts[qualificationRestoreReceiptKey(r.ID, r.Attempt)]
		postgresEnd, err := candidatePostgresEvidenceDeadline(r, m.candidatePostgresDeliveriesLocked(r.AppID, r.FrozenInputs.Scope),
			memory.bindingPostgres, m.qualificationSnapshots[r.ReservedInstanceID].Inputs, restore.Inputs, age, time.Now())
		if err != nil {
			return time.Time{}, fmt.Errorf("candidate PostgreSQL credential evidence is missing or changed: %w", err)
		}
		if !postgresEnd.IsZero() && postgresEnd.Before(end) {
			end = postgresEnd
		}
		storageEnd, err := candidateObjectStorageEvidenceDeadline(r, m.candidateObjectStorageDeliveriesLocked(r.AppID, r.FrozenInputs.Scope),
			r.BindingContext.Policy.ObjectStorageRequirements, memory.bindingObjectStorage, m.qualificationSnapshots[r.ReservedInstanceID].Inputs,
			restore.Inputs, age, time.Now())
		if err != nil {
			return time.Time{}, fmt.Errorf("candidate object storage credential evidence is missing or changed: %w", err)
		}
		if !storageEnd.IsZero() && storageEnd.Before(end) {
			end = storageEnd
		}
		if !time.Now().Before(end) {
			return time.Time{}, ErrBindingPromotionExpired
		}
		if deadline.IsZero() || end.Before(deadline) {
			deadline = end
		}
	}
	return deadline, nil
}
