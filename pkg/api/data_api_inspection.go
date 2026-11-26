package api

import "encoding/json"

// CreateDataAPIInspectionRequest selects one managed Data API binding. The
// server selects the deployment and generates the nonce.
type CreateDataAPIInspectionRequest struct {
	DatabaseID string `json:"database_id"`
	BindingID  string `json:"binding_id"`
	Table      string `json:"table"`
}

type DataAPIInspectionIdentity struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type DataAPIInspectionResponse struct {
	TaskID               string                    `json:"task_id"`
	AppID                string                    `json:"app_id"`
	DeploymentID         string                    `json:"deployment_id"`
	DatabaseID           string                    `json:"database_id"`
	BindingID            string                    `json:"binding_id"`
	CredentialGeneration int64                     `json:"credential_generation"`
	TaskStatus           AppTaskStatus             `json:"task_status"`
	BindingIdentity      DataAPIInspectionIdentity `json:"binding_identity"`
	Observation          json.RawMessage           `json:"observation,omitempty"`
}
