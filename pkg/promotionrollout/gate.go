package promotionrollout

import (
	"crypto/ed25519"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func New(config Config) *Gate {
	config.PublicKey = append(ed25519.PublicKey(nil), config.PublicKey...)
	enabled := map[Capability]bool{}
	for capability, value := range config.Enabled {
		enabled[capability] = value
	}
	// This composition uses both existing boot flags, plus its own signed scope.
	enabled[WorkerSet] = enabled[Workers]
	enabled[Coordinated] = enabled[Workers] && enabled[Migrations]
	config.Enabled = enabled
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Gate{config: config}
}

func (g *Gate) snapshot() (Policy, Approval, string) {
	var policy Policy
	var approval Approval
	if g == nil {
		return policy, approval, "rollout_not_configured"
	}
	if g.bootstrapReason != "" {
		return policy, approval, g.bootstrapReason
	}
	if !validHex(g.config.SourceSHA, 20) {
		return policy, approval, "build_revision_unknown"
	}
	if !filepath.IsAbs(g.config.PolicyPath) {
		return policy, approval, "rollout_not_configured"
	}
	raw, err := ReadFile(g.config.PolicyPath, api.PromotionRolloutPolicyMaxBytes)
	if err != nil || Decode(raw, &policy) != nil || policy.SchemaVersion != Version || !validTargets(policy.Targets) || !validCapabilities(policy.Capabilities) || !filepath.IsAbs(policy.ApprovalPath) {
		return policy, approval, "policy_invalid"
	}
	if policy.Paused {
		return policy, approval, "admission_paused"
	}
	raw, err = ReadFile(policy.ApprovalPath, api.PromotionRolloutApprovalMaxBytes)
	if err != nil {
		return policy, approval, "approval_unavailable"
	}
	approval, err = verify(raw, g.config.PublicKey)
	if err != nil {
		return policy, approval, "approval_untrusted"
	}
	return policy, approval, validateApproval(approval, g.config.SourceSHA, g.config.Now().UTC())
}

// Readiness never treats qualification-only harness access as production ready.
func (g *Gate) Readiness(target Target) Readiness {
	// Store projections may use compact UUIDs. Normalize lookup identities;
	// signed approval and policy claims still require canonical UUID strings.
	canonical, validTarget := canonicalTarget(target)
	if validTarget {
		target = canonical
	}
	result := Readiness{SchemaVersion: Version, Mode: "production", Target: target}
	if g != nil {
		result.SourceSHA = g.config.SourceSHA
	}
	policy, approval, reason := g.snapshot()
	if g != nil && g.qualification {
		result.Mode, reason = "qualification", "qualification_only"
	}
	for _, capability := range Capabilities() {
		reasons := []string{}
		if reason != "" {
			reasons = append(reasons, reason)
		}
		if !validTarget {
			reasons = append(reasons, "target_invalid")
		}
		if g == nil || !g.config.Enabled[Health] || !g.config.Enabled[capability] {
			reasons = append(reasons, "feature_disabled")
		}
		if reason == "" {
			if !slices.Contains(policy.Targets, target) || !slices.Contains(approval.Targets, target) {
				reasons = append(reasons, "target_not_approved")
			}
			if !slices.Contains(policy.Capabilities, capability) || !slices.Contains(approval.Capabilities, capability) {
				reasons = append(reasons, "capability_not_approved")
			}
			reasons = append(reasons, missingGates(approval, capability)...)
		}
		result.Capabilities = append(result.Capabilities, CapabilityReadiness{Capability: capability, Ready: len(reasons) == 0, Reasons: reasons})
	}
	return result
}

// Check evaluates all capabilities from one file snapshot immediately before
// admission. Callers must never use it to re-authorize existing operations.
func (g *Gate) Check(target Target, required ...Capability) bool {
	if len(required) == 0 {
		return false
	}
	if g != nil && g.qualification {
		for _, capability := range append([]Capability{Health}, required...) {
			if !slices.Contains(Capabilities(), capability) || !g.config.Enabled[capability] {
				return false
			}
		}
		_, valid := canonicalTarget(target)
		return valid
	}
	return g.Readiness(target).Allows(required...)
}

func canonicalTarget(target Target) (Target, bool) {
	account, accountErr := uuid.Parse(target.AccountID)
	project, projectErr := uuid.Parse(target.ProjectID)
	if accountErr != nil || projectErr != nil || account == uuid.Nil || project == uuid.Nil {
		return target, false
	}
	valid := func(input, canonical string) bool {
		return input == canonical || input == strings.ReplaceAll(canonical, "-", "")
	}
	if !valid(target.AccountID, account.String()) || !valid(target.ProjectID, project.String()) {
		return target, false
	}
	return Target{AccountID: account.String(), ProjectID: project.String()}, true
}

// Allows uses this one observation, so CLI output and exit status cannot refer
// to different policy revisions when an operator updates the file concurrently.
func (r Readiness) Allows(required ...Capability) bool {
	if r.Mode != "production" || len(required) == 0 {
		return false
	}
	for _, capability := range append([]Capability{Health}, required...) {
		index := slices.Index(Capabilities(), capability)
		if index < 0 || index >= len(r.Capabilities) || r.Capabilities[index].Capability != capability || !r.Capabilities[index].Ready {
			return false
		}
	}
	return true
}
