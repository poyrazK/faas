package state

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routelifecycle"
)

var ErrRouteLifecycleReviewChanged = errors.New("lifecycle review inputs changed")

type RouteLifecycleReviewBlockedError struct{ Reason string }

func (e *RouteLifecycleReviewBlockedError) Error() string {
	return "lifecycle review blocked: " + e.Reason
}

type RouteLifecycleCompatibilityValidator func(RoutePolicySnapshot, *RoutePolicyContract, *RoutePolicyContract, []api.RouteLifecycleMapping) error

// Callbacks are pure and run under the same policy/capture locks as persistence.
type RouteLifecycleApprovalStore interface {
	ApproveRouteLifecycle(context.Context, string, string, string, api.ApproveRouteLifecycleRequest, RouteCheckFingerprinter, RouteLifecycleCompatibilityValidator) (api.RouteLifecycleApproval, error)
	GetRouteLifecycleApproval(context.Context, string, string, string) (api.RouteLifecycleApproval, error)
}

func validLifecycleSHA(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}
func validateLifecycleApprovalRequest(r api.ApproveRouteLifecycleRequest) error {
	if r.ExpectedGateRevision == nil || *r.ExpectedGateRevision < 0 || r.ExpectedRequirementsRevision == nil || *r.ExpectedRequirementsRevision < 1 || r.ExpectedRemovalPolicyRevision == nil || *r.ExpectedRemovalPolicyRevision < 0 {
		return ErrInvalidArgument
	}
	for _, id := range []string{r.BaselineDeploymentID, r.CandidateDeploymentID} {
		v, err := uuid.Parse(id)
		if err != nil || v == uuid.Nil || v.String() != id {
			return ErrInvalidArgument
		}
	}
	if r.BaselineDeploymentID == r.CandidateDeploymentID {
		return ErrInvalidArgument
	}
	for _, h := range []string{r.ConfigurationSHA256, r.BaselineContractSHA256, r.CandidateContractSHA256} {
		if !validLifecycleSHA(h) {
			return ErrInvalidArgument
		}
	}
	_, _, err := normalizeLifecycleMappings(r.Mappings)
	return err
}
func normalizeLifecycleMappings(input []api.RouteLifecycleMapping) ([]api.RouteLifecycleMapping, string, error) {
	removal := make([]api.RouteRemovalMapping, len(input))
	byKey := map[string]api.RouteLifecycleMapping{}
	for i, m := range input {
		u, err := url.Parse(m.SuccessorURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.Path != m.SuccessorPath || strings.ContainsAny(m.SuccessorURL, "\r\n\x00") {
			return nil, "", ErrInvalidArgument
		}
		removal[i] = api.RouteRemovalMapping{Method: m.Method, Path: m.Path, SuccessorMethod: m.SuccessorMethod, SuccessorPath: m.SuccessorPath}
		if m.SuccessorAppID != "" || m.SuccessorDeploymentID != "" || m.SuccessorContractSHA256 != "" {
			for _, id := range []string{m.SuccessorAppID, m.SuccessorDeploymentID} {
				v, err := uuid.Parse(id)
				if err != nil || v == uuid.Nil || v.String() != id {
					return nil, "", ErrInvalidArgument
				}
			}
			if !validLifecycleSHA(m.SuccessorContractSHA256) {
				return nil, "", ErrInvalidArgument
			}
		}
		byKey[strings.ToUpper(strings.TrimSpace(m.Method))+" "+m.Path] = m
	}
	normalized, _, err := NormalizeRouteRemovalMappings(removal)
	if err != nil {
		return nil, "", err
	}
	out := make([]api.RouteLifecycleMapping, len(normalized))
	for i, m := range normalized {
		out[i] = api.RouteLifecycleMapping{Method: m.Method, Path: m.Path, SuccessorMethod: m.SuccessorMethod, SuccessorPath: m.SuccessorPath, SuccessorURL: byKey[m.Method+" "+m.Path].SuccessorURL, SuccessorAppID: byKey[m.Method+" "+m.Path].SuccessorAppID, SuccessorDeploymentID: byKey[m.Method+" "+m.Path].SuccessorDeploymentID, SuccessorContractSHA256: byKey[m.Method+" "+m.Path].SuccessorContractSHA256}
	}
	body, _ := json.Marshal(out)
	return out, fmt.Sprintf("%x", sha256.Sum256(body)), nil
}

func validateLifecycleMappings(before, after *RoutePolicyContract, mappings []api.RouteLifecycleMapping, at time.Time) error {
	if before == nil || after == nil || before.Truncated || after.Truncated || !validLifecycleSHA(before.SHA256) || !validLifecycleSHA(after.SHA256) {
		return &RouteLifecycleReviewBlockedError{"complete_captures_required"}
	}
	review := routelifecycle.Compare(before.Doc, after.Doc, at)
	changed := map[string]bool{}
	for _, f := range review.Findings {
		if f.Code != "successor_changed_requires_review" {
			return &RouteLifecycleReviewBlockedError{"other_lifecycle_findings_require_resolution"}
		}
		changed[f.Method+" "+f.Path] = true
	}
	if len(changed) == 0 || len(changed) != len(mappings) {
		return &RouteLifecycleReviewBlockedError{"mappings_must_cover_exact_successor_changes"}
	}
	var root struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if json.Unmarshal(after.Doc, &root) != nil {
		return &RouteLifecycleReviewBlockedError{"candidate_contract_unavailable"}
	}
	for _, m := range mappings {
		op, _ := root.Paths[m.Path][strings.ToLower(m.Method)].(map[string]any)
		if !changed[m.Method+" "+m.Path] || op["x-gregale-successor"] != m.SuccessorURL {
			return &RouteLifecycleReviewBlockedError{"mapping_does_not_match_declared_successor"}
		}
	}
	return nil
}

type lifecycleApprovalBinding struct {
	GateRevision, RequirementsRevision, RemovalPolicyRevision int64
	ConfigurationSHA256                                       string
}

func lifecycleBinding(snapshot RoutePolicySnapshot, gate api.CanaryRouteGate, saved api.SavedRouteRequirements, removal api.RouteRemovalPolicy, fingerprint RouteCheckFingerprinter) lifecycleApprovalBinding {
	b := lifecycleApprovalBinding{GateRevision: gate.Revision, RequirementsRevision: saved.Revision, RemovalPolicyRevision: removal.Revision}
	if fingerprint != nil {
		b.ConfigurationSHA256 = fingerprint(snapshot)
	}
	return b
}
func (b lifecycleApprovalBinding) matchesRequest(r api.ApproveRouteLifecycleRequest) bool {
	return b.GateRevision == *r.ExpectedGateRevision && b.RequirementsRevision == *r.ExpectedRequirementsRevision && b.RemovalPolicyRevision == *r.ExpectedRemovalPolicyRevision && b.ConfigurationSHA256 == r.ConfigurationSHA256
}
func lifecycleApproval(before, after *RoutePolicyContract, r api.ApproveRouteLifecycleRequest, b lifecycleApprovalBinding, actor, appID string, at time.Time, validator RouteLifecycleCompatibilityValidator, snapshot RoutePolicySnapshot) (api.RouteLifecycleApproval, error) {
	var receipt api.RouteLifecycleApproval
	if !b.matchesRequest(r) || before == nil || after == nil || before.SHA256 != r.BaselineContractSHA256 || after.SHA256 != r.CandidateContractSHA256 {
		return receipt, ErrRouteLifecycleReviewChanged
	}
	mappings, digest, _ := normalizeLifecycleMappings(r.Mappings)
	if err := validateLifecycleMappings(before, after, mappings, at); err != nil {
		return receipt, err
	}
	if err := validator(snapshot, before, after, mappings); err != nil {
		return receipt, err
	}
	receipt = api.RouteLifecycleApproval{ID: uuid.NewString(), AppID: appID, GateRevision: b.GateRevision, RequirementsRevision: b.RequirementsRevision, RemovalPolicyRevision: b.RemovalPolicyRevision, ConfigurationSHA256: b.ConfigurationSHA256, BaselineDeploymentID: r.BaselineDeploymentID, CandidateDeploymentID: r.CandidateDeploymentID, BaselineContractSHA256: before.SHA256, CandidateContractSHA256: after.SHA256, MappingSHA256: digest, Mappings: mappings, Compatibility: "no_supported_breaks", CheckerVersion: 1, ApprovedBy: actor, ApprovedAt: at, ValidUntil: at.Add(api.RouteLifecycleApprovalTTL)}
	return receipt, nil
}
func matchingLifecycleApproval(a api.RouteLifecycleApproval, b lifecycleApprovalBinding, before, after *RoutePolicyContract, at time.Time) bool {
	if before == nil || after == nil || a.InvalidatedAt != nil || a.ApprovedBy == "" || a.CheckerVersion != 1 || a.Compatibility != "no_supported_breaks" || !at.Before(a.ValidUntil) || at.Before(a.ApprovedAt) || a.GateRevision != b.GateRevision || a.RequirementsRevision != b.RequirementsRevision || a.RemovalPolicyRevision != b.RemovalPolicyRevision || !validLifecycleSHA(b.ConfigurationSHA256) || a.ConfigurationSHA256 != b.ConfigurationSHA256 || a.BaselineDeploymentID != before.DeploymentID || a.CandidateDeploymentID != after.DeploymentID || a.BaselineContractSHA256 != before.SHA256 || a.CandidateContractSHA256 != after.SHA256 {
		return false
	}
	mappings, digest, err := normalizeLifecycleMappings(a.Mappings)
	return err == nil && digest == a.MappingSHA256 && validateLifecycleMappings(before, after, mappings, at) == nil
}
func sameLifecycleScope(a, b string) bool {
	return normalizedDeploymentScope(a) == normalizedDeploymentScope(b) || routeRemovalProductionScope(a) && routeRemovalProductionScope(b)
}
