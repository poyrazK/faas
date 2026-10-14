package release

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// WorkerDecision validates the evidence meterd attached to a Safe Deploy
// request and encodes it for storage (ADR-1237). A nil decision is allowed
// (an older meterd sends none) and encodes to nil. kind is the decision kind
// the route accepts.
func WorkerDecision(decision *api.RolloutDecision, kind string) (json.RawMessage, error) {
	if decision == nil {
		return nil, nil
	}
	if err := decision.Validate(); err != nil {
		return nil, err
	}
	if decision.Kind != kind {
		return nil, fmt.Errorf("this route records %s decisions, not %s", kind, decision.Kind)
	}
	return json.Marshal(decision)
}

// ManualDecision is the record APID writes for a transition a person asked
// for (ADR-1237). It names the rule only; no request text is copied.
func ManualDecision(rule string, d state.Deployment, at time.Time) json.RawMessage {
	summary := "requested through the API"
	if rule == api.RolloutRuleAbortManual {
		summary = "aborted through the API"
	}
	encoded, err := json.Marshal(api.ManualRolloutDecision(rule, summary, d.CanaryStep, d.TrafficPercent, at))
	if err != nil {
		return nil
	}
	return encoded
}

// RecoveryRule is the manual rule for a recover action.
func RecoveryRule(action string) string {
	if action == "abort" {
		return api.RolloutRuleAbortManual
	}
	return api.RolloutRuleAdvanceManual
}

// DecodeDecision returns the stored record, or nil when there is none or it
// cannot be read. A stored record was validated on write, so a decode failure
// means the column was edited by hand; the response simply omits it.
func DecodeDecision(raw json.RawMessage) *api.RolloutDecision {
	if len(raw) == 0 {
		return nil
	}
	var decision api.RolloutDecision
	if err := json.Unmarshal(raw, &decision); err != nil || decision.Rule == "" {
		return nil
	}
	return &decision
}
