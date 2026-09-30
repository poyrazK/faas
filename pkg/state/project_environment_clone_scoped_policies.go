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
	// producer identities or runtime lanes. Keep this guard until those durable
	// proofs exist, including for an explicitly empty captured collection.
	if record.snapshot.Policies.Work != nil {
		return fmt.Errorf("clone workload %q work policies: %w", record.WorkloadSlug, ErrProjectEnvironmentCloneWorkPolicyIsolationUnavailable)
	}
	actualHash, err := cloneScopedPoliciesHash(actual)
	if err != nil || !routePresent || actualHash != record.SourcePoliciesHash {
		return fmt.Errorf("clone workload %q scoped policies differ from its capture: %w", record.WorkloadSlug, ErrConflict)
	}
	return nil
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
