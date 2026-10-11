package api

const OperationWorkflowDispatchPath = "/v1/workflow-operations:dispatch"

// WorkflowOperationDispatchRequest is the private scheduler-to-gateway proof.
// It deliberately carries no caller-selected handler, body or deployment.
type WorkflowOperationDispatchRequest struct {
	OperationID        string `json:"operation_id"`
	AccountID          string `json:"account_id"`
	AppID              string `json:"app_id"`
	RunID              string `json:"run_id"`
	StepName           string `json:"step_name"`
	StepAttempt        int    `json:"step_attempt"`
	CoordinatorAttempt int    `json:"coordinator_attempt"`
	InstanceID         string `json:"instance_id"`
	NodeID             string `json:"node_id"`
	GuestCapability    string `json:"guest_capability"`
}

// Handler bytes and status remain separate from operation business completion.
type WorkflowOperationDispatchResponse struct {
	StatusCode int    `json:"status_code"`
	Body       []byte `json:"body"`
}
