package api

import (
	"fmt"
	"strings"
)

// EventWorkflowRecoveryPlan pins explicit acceptance recipients; it never scans
// for new failures or follows a newer replay cycle.
type EventWorkflowRecoveryPlan struct {
	Name             string                        `json:"name"`
	PublishedVersion int64                         `json:"published_version"`
	Source           string                        `json:"source"`
	Events           []EventWorkflowRecoveryTarget `json:"events"`
}
type EventWorkflowRecoveryTarget struct {
	EventID        string `json:"event_id"`
	SubscriptionID string `json:"subscription_id"`
	ReplayCount    int64  `json:"replay_count"`
	Generation     *int64 `json:"generation,omitempty"`
}

func (p EventWorkflowRecoveryPlan) Expectation(t EventWorkflowRecoveryTarget) EventRoutingRecoveryExpectation {
	return EventRoutingRecoveryExpectation{WorkflowName: p.Name, PublishedVersion: p.PublishedVersion, ReplayCount: t.ReplayCount, Generation: t.Generation}
}
func (r EventRecoveryRequest) validateWorkflowRouting() error {
	p := r.WorkflowRouting
	if r.ParentJobID != "" || r.Mode != "" && r.Mode != "routing" || r.Outcome != "" || r.SubscriptionID != "" || r.EventSource != "" || r.EventType != "" || r.FailureCode != "" || r.MinAgeSeconds != 0 || r.IncludeNonRetryable {
		return fmt.Errorf("workflow_routing cannot be combined with parent, execution or backlog filters")
	}
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > EventBacklogFilterMaxBytes || p.PublishedVersion < 1 || strings.TrimSpace(p.Source) == "" || len(p.Source) > EventBacklogFilterMaxBytes || len(p.Events) < 1 || len(p.Events) > AutomationRecoveryBatchMaxRuns || r.RequestID == "" {
		return fmt.Errorf("workflow_routing requires name, published_version, source, 1..%d events and request_id", AutomationRecoveryBatchMaxRuns)
	}
	seen := map[string]bool{}
	for _, t := range p.Events {
		if strings.TrimSpace(t.EventID) == "" || len(t.EventID) > EventBacklogFilterMaxBytes || strings.TrimSpace(t.SubscriptionID) == "" || len(t.SubscriptionID) > EventBacklogFilterMaxBytes || seen[t.EventID] || !p.Expectation(t).Valid() {
			return fmt.Errorf("invalid or duplicate workflow recovery target")
		}
		seen[t.EventID] = true
	}
	return nil
}
