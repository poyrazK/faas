package api

import "time"

// Readiness subscriptions discover bounded current edges and compare saved observations.
// They produce advisory inbox notifications, never execution authority.
type OperationWorkflowReadinessSubscriptionRequest struct {
	AppID           string   `json:"app_id,omitempty"`
	TenantID        string   `json:"tenant_id,omitempty"`
	Scope           string   `json:"scope"`
	Name            string   `json:"name"`
	Workflow        string   `json:"workflow,omitempty"`
	TargetOperation string   `json:"target_operation,omitempty"`
	Owner           string   `json:"owner,omitempty"`
	Unassigned      bool     `json:"unassigned,omitempty"`
	Events          []string `json:"events,omitempty"`
}
type OperationWorkflowReadinessSubscription struct {
	ID                     string    `json:"id"`
	AppID                  string    `json:"app_id"`
	TenantID               string    `json:"tenant_id,omitempty"`
	Scope                  string    `json:"scope"`
	Name                   string    `json:"name"`
	Workflow               string    `json:"workflow,omitempty"`
	TargetOperation        string    `json:"target_operation,omitempty"`
	Owner                  string    `json:"owner,omitempty"`
	Unassigned             bool      `json:"unassigned,omitempty"`
	Events                 []string  `json:"events"`
	CreatedAt              time.Time `json:"created_at"`
	EvaluatedAt            time.Time `json:"evaluated_at"`
	Status                 string    `json:"status"`
	WatchedTransitionCount int       `json:"watched_transition_count"`
	CoverageIncomplete     bool      `json:"coverage_incomplete"`
	LatestSequence         int64     `json:"latest_sequence"`
}
type OperationWorkflowReadinessSubscriptionsResponse struct {
	Items []OperationWorkflowReadinessSubscription `json:"items"`
}
type OperationWorkflowReadinessNotification struct {
	Coordination *OperationWorkflowReadinessCoordination `json:"coordination,omitempty"`
	ID           string                                  `json:"id"`
	Sequence     int64                                   `json:"sequence"`
	Kind         string                                  `json:"kind"`
	CreatedAt    time.Time                               `json:"created_at"`
	Explanation  string                                  `json:"explanation"`
	Change       *OperationWorkflowReadinessChange       `json:"change,omitempty"`
}
type OperationWorkflowReadinessInboxResponse struct {
	Subscription   OperationWorkflowReadinessSubscription   `json:"subscription"`
	Items          []OperationWorkflowReadinessNotification `json:"items"`
	LatestSequence int64                                    `json:"latest_sequence"`
	NextSequence   int64                                    `json:"next_sequence"`
	ResyncRequired bool                                     `json:"resync_required"`
	HasMore        bool                                     `json:"has_more"`
}

// Coordination is shared inbox bookkeeping, never business workflow evidence.
type OperationWorkflowReadinessCoordination struct {
	Revision       int64      `json:"revision"`
	Status         string     `json:"status"`
	Owner          string     `json:"owner"`
	Note           string     `json:"note"`
	FollowUpAt     *time.Time `json:"follow_up_at,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at"`
	UpdatedBy      string     `json:"updated_by"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
	AcknowledgedBy string     `json:"acknowledged_by,omitempty"`
}
type OperationWorkflowReadinessCoordinationRequest struct {
	Revision   int64      `json:"revision"`
	Status     string     `json:"status"`
	Owner      string     `json:"owner"`
	Note       string     `json:"note"`
	FollowUpAt *time.Time `json:"follow_up_at,omitempty"`
}
type OperationWorkflowReadinessNotificationCheck struct {
	Notification OperationWorkflowReadinessNotification `json:"notification"`
	CheckedAt    time.Time                              `json:"checked_at"`
	Status       string                                 `json:"status"`
	Change       *OperationWorkflowReadinessChange      `json:"change,omitempty"`
}
