package api

import "time"

// EnvironmentWorkloadRetirement reports retained scoped identities and durable
// withdrawal progress. Its historical receipts never authorize new execution.
type EnvironmentWorkloadRetirement struct {
	ID                   string     `json:"id"`
	AccountID            string     `json:"account_id"`
	ProjectID            string     `json:"project_id"`
	EnvironmentID        string     `json:"environment_id"`
	SourceID             string     `json:"source_id"`
	Resource             string     `json:"resource"`
	AppID                string     `json:"app_id"`
	RevisionID           string     `json:"revision_id"`
	PlanHash             string     `json:"plan_hash"`
	Generation           int64      `json:"generation"`
	Phase                string     `json:"phase"`
	DeploymentIDs        []string   `json:"deployment_ids,omitempty"`
	QueueBindingIDs      []string   `json:"queue_binding_ids,omitempty"`
	JobID                string     `json:"job_id"`
	RouteDeploymentID    string     `json:"route_deployment_id"`
	RouteGeneration      int64      `json:"route_generation"`
	ExpectedGateways     []string   `json:"expected_gateways,omitempty"`
	AcknowledgedGateways []string   `json:"acknowledged_gateways,omitempty"`
	RequestedAt          time.Time  `json:"requested_at"`
	RetiredAt            *time.Time `json:"retired_at"`
	RecoveredAt          *time.Time `json:"recovered_at"`
	RecoveryRevisionID   string     `json:"recovery_revision_id"`
	RecoveryGeneration   int64      `json:"recovery_generation"`
}

func (r EnvironmentWorkloadRetirement) BlocksAdmission() bool {
	return r.Phase == "retiring" || r.Phase == "retired" || r.Phase == "recovering"
}
