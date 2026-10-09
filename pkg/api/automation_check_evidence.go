package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// AutomationCheckEvidence contains client-reported checks, not server attestation.
// Only metadata is retained; sample inputs, outputs and failure text are omitted.
type AutomationCheckEvidence struct {
	ServerVerified    bool                       `json:"server_verified,omitempty"`
	DefinitionHash    string                     `json:"definition_hash"`
	CheckedVersion    int64                      `json:"checked_version"`
	CheckedAt         time.Time                  `json:"checked_at"`
	Scenarios         []AutomationCheckScenario  `json:"scenarios"`
	CoverageRequired  bool                       `json:"coverage_required"`
	CoveragePassed    bool                       `json:"coverage_passed"`
	CoverageRemaining int                        `json:"coverage_remaining"`
	Exclusions        []AutomationCheckExclusion `json:"exclusions"`
}
type AutomationCheckScenario struct {
	Name            string `json:"name"`
	Passed          bool   `json:"passed"`
	DefinitionValid bool   `json:"definition_valid"`
	Complete        bool   `json:"complete"`
}
type AutomationCheckExclusion struct {
	Step   string `json:"step"`
	Loop   string `json:"loop,omitempty"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// Validate binds evidence to the saved draft under the publication version lock.
func (e *AutomationCheckEvidence) Validate(definition WorkflowSpec, version int64, now time.Time) error {
	if e == nil {
		return nil
	}
	raw, err := json.Marshal(definition)
	if err != nil {
		return err
	}
	if e.DefinitionHash != fmt.Sprintf("%x", sha256.Sum256(raw)) || e.CheckedVersion != version {
		return fmt.Errorf("check evidence must match the saved draft hash and version")
	}
	if e.CheckedAt.IsZero() || e.CheckedAt.After(now.Add(5*time.Minute)) || e.CheckedAt.Before(now.Add(-24*time.Hour)) {
		return fmt.Errorf("check evidence timestamp must be within the past day")
	}
	if e.Exclusions == nil || len(e.Scenarios) == 0 || len(e.Scenarios) > 32 || len(e.Exclusions) > 256 || e.CoverageRemaining < 0 || e.CoverageRemaining > 4096 || e.CoveragePassed != (e.CoverageRemaining == 0) || (e.CoverageRequired && !e.CoveragePassed) {
		return fmt.Errorf("invalid check evidence summary")
	}
	safeText := func(s string, max int) bool {
		return strings.TrimSpace(s) != "" && len(s) <= max && strings.IndexFunc(s, unicode.IsControl) < 0
	}
	names := map[string]bool{}
	for _, s := range e.Scenarios {
		if !safeText(s.Name, 256) || names[s.Name] || !s.Passed || !s.DefinitionValid || !s.Complete {
			return fmt.Errorf("check evidence requires unique, passing, complete scenarios")
		}
		names[s.Name] = true
	}
	codes := map[string]bool{"guard_match_missing": true, "guard_skip_missing": true, "failure_route_missing": true, "wait_success_missing": true, "wait_timeout_missing": true, "retry_missing": true, "loop_empty_missing": true, "loop_multiple_items_missing": true}
	seen := map[[3]string]bool{}
	for _, x := range e.Exclusions {
		key := [3]string{x.Step, x.Loop, x.Code}
		if !safeText(x.Step, 512) || (x.Loop != "" && !safeText(x.Loop, 256)) || !codes[x.Code] || !safeText(x.Reason, 512) || seen[key] {
			return fmt.Errorf("invalid check evidence exclusion")
		}
		seen[key] = true
	}
	encoded, _ := json.Marshal(e)
	if len(encoded) > 128*1024 {
		return fmt.Errorf("check evidence exceeds maximum size")
	}
	return nil
}
