package state

import (
	"encoding/json"

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

// RolloutAuditDataForTest exposes rolloutAuditData to the external state_test
// package so the encoding contract can be pinned against a real Postgres
// jsonb column. Follows the package's "ForTest" seam convention (see
// memstore_rebalance_helpers.go); production code MUST NOT call it.
func RolloutAuditDataForTest(action, reason string) []byte {
	return rolloutAuditData(action, reason)
}
