package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

var ErrProjectEnvironmentClonePolicyCaptureUnavailable = fmt.Errorf("clone scoped policy capture is unavailable: %w", ErrConflict)

type projectCloneScopedPolicies struct {
	OnlyAllowDeclaredRoutes bool                                          `json:"only_allow_declared_routes"`
	DeclaredRoutes          []DeclaredRoute                               `json:"declared_routes"`
	EdgePresent             bool                                          `json:"edge_present"`
	EdgeRules               []ProjectEnvironmentEdgeRule                  `json:"edge_rules"`
	Work                    *ProjectEnvironmentCloneWorkPolicyDefinitions `json:"work,omitempty"`
	Queues                  *ProjectEnvironmentCloneQueueDefinitions      `json:"queues,omitempty"`
}

func normalizeCloneScopedPolicies(policies projectCloneScopedPolicies) (projectCloneScopedPolicies, error) {
	if !validProjectEnvironmentEdgeRules(policies.EdgeRules) || (!policies.EdgePresent && len(policies.EdgeRules) != 0) {
		return policies, ErrConflict
	}
	policies.DeclaredRoutes = cloneDeclaredRoutes(policies.DeclaredRoutes)
	policies.EdgeRules = cloneProjectEnvironmentEdgeRules(policies.EdgeRules)
	if policies.Work != nil {
		work, err := normalizeCloneWorkPolicyDefinitions(*policies.Work)
		if err != nil {
			return policies, err
		}
		policies.Work = &work
	}
	if policies.Queues != nil {
		queues, err := normalizeCloneQueues(*policies.Queues)
		if err != nil {
			return policies, err
		}
		policies.Queues = &queues
	}
	return policies, nil
}

func cloneScopedPoliciesHash(policies projectCloneScopedPolicies) (string, error) {
	policies, err := normalizeCloneScopedPolicies(policies)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(policies)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func capturedCloneScopedPolicies(records []projectCloneWorkloadRecord) (map[string]projectCloneScopedPolicies, error) {
	policies := make(map[string]projectCloneScopedPolicies, len(records))
	for _, record := range records {
		if record.snapshot.Policies == nil || record.SourcePoliciesHash == "" {
			return nil, ErrProjectEnvironmentClonePolicyCaptureUnavailable
		}
		copy, err := normalizeCloneScopedPolicies(*record.snapshot.Policies)
		if err != nil {
			return nil, err
		}
		policies[record.AppID] = copy
	}
	return policies, nil
}

func validateCloneScopedPolicyPublication(record projectCloneWorkloadRecord, actual projectCloneScopedPolicies, routePresent bool) error {
	if record.snapshot.Policies == nil {
		return ErrProjectEnvironmentClonePolicyCaptureUnavailable
	}
	// A matching policy list alone cannot prove scoped admission, independent
	// producer identities or runtime lanes, so a captured policy, producer
	// binding or queue binding still blocks publication. An explicitly empty
	// catalogue publishes (ADR-20261024123122): production producers (events,
	// crons, triggers, queues, webhooks, workflows, replay and API invoke)
	// resolve only to the production lane, and stage-origin work never reads
	// application work policies, so a later production policy cannot reach the
	// stage.
	if !emptyCloneWorkPolicies(record.snapshot) {
		return fmt.Errorf("clone workload %q work policies: %w", record.WorkloadSlug, ErrProjectEnvironmentCloneWorkPolicyIsolationUnavailable)
	}
	if !emptyCloneQueues(record.snapshot) {
		return fmt.Errorf("clone workload %q queues: %w", record.WorkloadSlug, ErrProjectEnvironmentQueueActivationUnavailable)
	}
	// Empty catalogues leave nothing on the target to compare; routes and edge
	// rules carry the proof.
	actual.Work, actual.Queues = record.snapshot.Policies.Work, record.snapshot.Policies.Queues

	actualHash, err := cloneScopedPoliciesHash(actual)
	if err != nil || !routePresent || actualHash != record.SourcePoliciesHash {
		return fmt.Errorf("clone workload %q scoped policies differ from its capture: %w", record.WorkloadSlug, ErrConflict)
	}
	return nil
}

// A snapshot without a catalogue predates it; the PostgreSQL capture always
// records both, empty or not.
func emptyCloneWorkPolicies(snapshot projectCloneWorkloadSnapshot) bool {
	work, settings := snapshot.Policies.Work, snapshot.Settings.WorkPolicies
	return (work == nil || len(work.Policies) == 0 && len(work.EventBindings) == 0 && len(work.TriggerBindings) == 0) &&
		(settings == nil || len(settings.Policies) == 0)
}

func emptyCloneQueues(snapshot projectCloneWorkloadSnapshot) bool {
	queues, settings := snapshot.Policies.Queues, snapshot.Settings.QueueBindings
	return (queues == nil || len(queues.Bindings) == 0) && (settings == nil || len(settings.Bindings) == 0)
}

func (m *MemStore) capturedScopedPoliciesLocked(appID, environment string, settings ProjectEnvironmentWorkloadSettings) projectCloneScopedPolicies {
	policy, present := m.projectEnvironmentEdgePolicies[projectEnvironmentRoutePolicyKey(appID, environment)]
	return projectCloneScopedPolicies{OnlyAllowDeclaredRoutes: settings.OnlyAllowDeclaredRoutes, DeclaredRoutes: cloneDeclaredRoutes(settings.DeclaredRoutes),
		EdgePresent: present, EdgeRules: cloneProjectEnvironmentEdgeRules(policy.Rules)}
}

func (m *MemStore) copyCapturedScopedPoliciesLocked(clone ProjectEnvironmentClone, now time.Time, result *ProjectEnvironmentCloneResult) {
	for appID, policies := range clone.capturedPolicies {
		if !policies.OnlyAllowDeclaredRoutes || len(policies.DeclaredRoutes) > 0 {
			m.projectEnvironmentRoutePolicies[projectEnvironmentRoutePolicyKey(appID, clone.TargetSlug)] = ProjectEnvironmentRoutePolicy{AccountID: clone.AccountID, ProjectID: clone.ProjectID,
				AppID: appID, EnvironmentSlug: clone.TargetSlug, OnlyAllowDeclaredRoutes: policies.OnlyAllowDeclaredRoutes, DeclaredRoutes: cloneDeclaredRoutes(policies.DeclaredRoutes), CreatedAt: now, UpdatedAt: now}
			result.RoutesCopied++
		}
		if policies.EdgePresent {
			m.projectEnvironmentEdgePolicies[projectEnvironmentRoutePolicyKey(appID, clone.TargetSlug)] = ProjectEnvironmentEdgePolicy{AccountID: clone.AccountID, ProjectID: clone.ProjectID,
				AppID: appID, EnvironmentSlug: clone.TargetSlug, Rules: cloneProjectEnvironmentEdgeRules(policies.EdgeRules), CreatedAt: now, UpdatedAt: now}
			result.PoliciesCopied++
		}
	}
}

func (m *MemStore) verifyCloneScopedPolicyPublicationLocked(op ProjectEnvironmentCloneOperation, records []projectCloneWorkloadRecord) error {
	for _, record := range records {
		route, present := m.projectEnvironmentRoutePolicies[projectEnvironmentRoutePolicyKey(record.AppID, op.TargetEnvironment)]
		edge, edgePresent := m.projectEnvironmentEdgePolicies[projectEnvironmentRoutePolicyKey(record.AppID, op.TargetEnvironment)]
		actual := projectCloneScopedPolicies{OnlyAllowDeclaredRoutes: route.OnlyAllowDeclaredRoutes, DeclaredRoutes: route.DeclaredRoutes, EdgePresent: edgePresent, EdgeRules: edge.Rules}
		if err := validateCloneScopedPolicyPublication(record, actual, present); err != nil {
			return err
		}
	}
	return nil
}
