package state

import (
	"encoding/json"
	"errors"
)

// ErrRetryIntentUnavailable means an older failure lost its explicit traffic
// choice. Re-deploying is required; guessing could turn a dark release public.
var ErrRetryIntentUnavailable = errors.New("deployment retry intent is unavailable; submit a fresh deployment with the intended traffic settings")

// DeploymentRetryIntent retains admission intent independently of routing and
// failure state. Stage writers preserve it across rolling binary upgrades.
type DeploymentRetryIntent struct {
	Version                string `json:"version"`
	TrafficPercent         int    `json:"traffic_percent"`
	TrafficPercentExplicit bool   `json:"traffic_percent_explicit"`
	ServiceRollout         bool   `json:"service_rollout"`
}

// RetryTrafficIntent is shared by retry admission and execution. A failed
// serving weight of zero must not masquerade as an intentionally dark release.
func (d Deployment) RetryTrafficIntent() (int, bool, error) {
	intent, err := readDeploymentRetryIntent(d)
	if err != nil {
		return 0, false, err
	}
	if intent != nil {
		return intent.TrafficPercent, intent.TrafficPercentExplicit, nil
	}
	if d.CanaryTotalSteps == 0 && d.TrafficPercentExplicit && d.TrafficPercent == 0 {
		return 0, false, ErrRetryIntentUnavailable
	}
	return d.TrafficPercent, d.TrafficPercentExplicit, nil
}

func newDeploymentRetryIntent(d Deployment) DeploymentRetryIntent {
	return DeploymentRetryIntent{Version: "v1", TrafficPercent: d.TrafficPercent,
		TrafficPercentExplicit: d.TrafficPercentExplicit, ServiceRollout: IsServiceRollout(d)}
}

func readDeploymentRetryIntent(d Deployment) (*DeploymentRetryIntent, error) {
	if len(d.StageState) == 0 {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(d.StageState, &fields); err != nil {
		return nil, ErrRetryIntentUnavailable
	}
	raw, ok := fields["retry_intent"]
	if !ok {
		return nil, nil
	}
	var intent DeploymentRetryIntent
	var required map[string]json.RawMessage
	if json.Unmarshal(raw, &intent) != nil || json.Unmarshal(raw, &required) != nil ||
		intent.Version != "v1" || intent.TrafficPercent < 0 || intent.TrafficPercent > 100 ||
		len(required["traffic_percent"]) == 0 || len(required["traffic_percent_explicit"]) == 0 || len(required["service_rollout"]) == 0 ||
		string(required["traffic_percent"]) == "null" || string(required["traffic_percent_explicit"]) == "null" || string(required["service_rollout"]) == "null" {
		return nil, ErrRetryIntentUnavailable
	}
	return &intent, nil
}

// Mirror the database trigger: insertion captures the accepted inputs; updates
// restore the original intent even if an older stage writer drops the field.
// Legacy active rows capture their pre-update values before failure zeroes them.
func preserveDeploymentRetryIntent(previous, next Deployment, existed bool) json.RawMessage {
	var fields map[string]json.RawMessage
	if len(next.StageState) != 0 && json.Unmarshal(next.StageState, &fields) != nil {
		return next.StageState
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	var intent json.RawMessage
	if existed {
		var oldFields map[string]json.RawMessage
		_ = json.Unmarshal(previous.StageState, &oldFields)
		intent = oldFields["retry_intent"]
		if len(intent) == 0 && !previous.Status.IsTerminal() {
			intent, _ = json.Marshal(newDeploymentRetryIntent(previous))
		}
	} else {
		intent, _ = json.Marshal(newDeploymentRetryIntent(next))
	}
	if len(intent) == 0 {
		delete(fields, "retry_intent")
	} else {
		fields["retry_intent"] = intent
	}
	raw, _ := json.Marshal(fields)
	return raw
}
