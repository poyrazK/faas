package api

// ExecutionEnvironmentEvent is a bounded build diagnostic, scoped like its environment.
// Sequence is durable; a retained-tail gap is explicit on the containing page.
type ExecutionEnvironmentEvent struct {
	Sequence  int64  `json:"sequence"`
	Stage     string `json:"stage"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message"`
	Attempt   int    `json:"attempt"`
	CreatedAt string `json:"created_at"`
}
type ExecutionEnvironmentEventsResponse struct {
	Events       []ExecutionEnvironmentEvent `json:"events"`
	NextSequence int64                       `json:"next_sequence"`
	Gap          bool                        `json:"gap,omitempty"`
	Status       string                      `json:"status"`
}

const ExecutionFinishedWebhookEvent = "execution.finished"

var AllowedAccountRunWebhookEvents = []string{ExecutionFinishedWebhookEvent, RunBudgetThresholdWebhookEvent, ExecutionPipelineFinishedWebhookEvent}

// ExecutionFinishedWebhookPayload contains receipt references, never customer output.
// EventID remains the execution ID across webhook retries and manual redrive.
type ExecutionFinishedWebhookPayload struct {
	EventID                string           `json:"event_id"`
	ExecutionID            string           `json:"execution_id"`
	Status                 ExecutionStatus  `json:"status"`
	Runtime                ExecutionRuntime `json:"runtime"`
	FinishedAt             string           `json:"finished_at"`
	ReceiptURL             string           `json:"receipt_url"`
	EnvironmentID          string           `json:"environment_id,omitempty"`
	WorkflowID             string           `json:"workflow_id,omitempty"`
	ResultValidationStatus string           `json:"result_validation_status,omitempty"`
}

// AllowedAccountWebhookEvents is the public account-receiver CLI vocabulary.
var AllowedAccountWebhookEvents = append(append([]string{}, AllowedAccountReleaseWebhookEvents...), AllowedAccountRunWebhookEvents...)
