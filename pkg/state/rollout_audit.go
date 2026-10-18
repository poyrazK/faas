package state

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
)

// normalizeRolloutReason bounds and sanitizes the free-text reason attached to
// a rollout recovery action.
//
// The reason arrives from POST /v1/apps/{slug}/rollout/recover and reaches two
// destinations with different rejection rules: deployments.rollout_aborted_reason
// (a text column, which rejects invalid UTF-8 and NUL) and events.data (a jsonb
// column, which rejects anything that is not valid JSON). Both writes ride the
// same transaction as the deployment stamp, so either rejection rolls back the
// recovery the operator was performing.
//
// Normalizing once, at the store boundary, means neither write can fail on
// encoding regardless of what any upstream caller did to the string.
func normalizeRolloutReason(reason string) string {
	return safetext.Truncate(reason, api.AuditReasonMaxBytes)
}

// rolloutAuditData encodes the audit payload for a rollout recovery action.
//
// encoding/json is load-bearing and not interchangeable with fmt: the %q verb
// is strconv.Quote, which emits \xNN escapes for control bytes and invalid
// UTF-8. JSON has no \x escape, so a %q-built payload is rejected by a jsonb
// column with SQLSTATE 22P02. encoding/json escapes the same bytes as \u00XX,
// which is valid.
func rolloutAuditData(action, reason string) []byte {
	payload, err := json.Marshal(struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}{Action: action, Reason: normalizeRolloutReason(reason)})
	if err != nil {
		// Unreachable: both fields are plain strings, and encoding/json
		// replaces invalid UTF-8 with U+FFFD rather than failing. The fallback
		// is a fixed literal rather than a reconstructed payload so this
		// branch cannot reintroduce the string-concatenation defect it exists
		// to guard against.
		return []byte(`{"action":"unknown","reason":""}`)
	}
	return payload
}

// Keep the predecessor and the checked policy/evidence fence in the audit
// transaction that restores traffic. Public clients cannot supply these fences.
func rolloutRecoveryAuditData(reason, predecessorID string, fences []BindingPromotionFence) []byte {
	payload, err := json.Marshal(struct {
		Action        string                  `json:"action"`
		Reason        string                  `json:"reason"`
		PredecessorID string                  `json:"predecessor_deployment_id"`
		BindingFences []BindingPromotionFence `json:"binding_fences,omitempty"`
	}{"abort", normalizeRolloutReason(reason), predecessorID, fences})
	if err != nil {
		return rolloutAuditData("abort", reason)
	}
	return payload
}

func addRolloutRecoveryAuditFences(data []byte, predecessorID string, fences []BindingPromotionFence) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("decode recovery audit: %w", err)
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	predecessor, err := json.Marshal(predecessorID)
	if err != nil {
		return nil, fmt.Errorf("encode recovery predecessor: %w", err)
	}
	checked, err := json.Marshal(fences)
	if err != nil {
		return nil, fmt.Errorf("encode recovery fences: %w", err)
	}
	fields["predecessor_deployment_id"], fields["binding_fences"] = predecessor, checked
	return json.Marshal(fields)
}

// rolloutAuditDataWithDecision is rolloutAuditData carrying the
// api.RolloutDecision behind the transition (ADR-1447).
func rolloutAuditDataWithDecision(action, reason string, decision json.RawMessage) []byte {
	return withAuditDecision(rolloutAuditData(action, reason), decision)
}

// withAuditDecision adds {"decision": ...} to an audit payload object. An
// empty decision, or a payload that is not an object, is returned unchanged:
// the decision is evidence, and losing it must never roll back the transition.
func withAuditDecision(data []byte, decision json.RawMessage) []byte {
	if len(decision) == 0 || !json.Valid(decision) {
		return data
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return data
	}
	fields["decision"] = decision
	merged, err := json.Marshal(fields)
	if err != nil {
		return data
	}
	return merged
}

// WithAuditDecision lets APID add the decision to the advance audit payload
// it builds before calling AdvanceCanary.
func WithAuditDecision(data []byte, decision json.RawMessage) []byte {
	return withAuditDecision(data, decision)
}

// leaseExpiredRolloutDecision is the record for the emergency abort taken when
// meterd's Safe Deploy lease expires during an active canary (ADR-1447).
func leaseExpiredRolloutDecision(reason string, dep Deployment, at time.Time) json.RawMessage {
	decision := api.ManualRolloutDecision(api.RolloutRuleAbortLeaseExpired,
		safetext.Truncate(reason, api.RolloutDecisionSummaryMaxBytes), dep.CanaryStep, dep.TrafficPercent, at)
	encoded, err := json.Marshal(decision)
	if err != nil {
		return nil
	}
	return encoded
}

// RolloutAuditDataForTest exposes rolloutAuditData to the external state_test
// package so the encoding contract can be pinned against a real Postgres
// jsonb column. Follows the package's "ForTest" seam convention (see
// memstore_rebalance_helpers.go); production code MUST NOT call it.
func RolloutAuditDataForTest(action, reason string) []byte {
	return rolloutAuditData(action, reason)
}
