package api

import (
	"encoding/json"
	"time"
)

const (
	APISandboxRunModeAutomated    = "automated"
	APISandboxRunModePartner      = "partner"
	APISandboxRunAwaitingPartner  = "awaiting_partner"
	APISandboxRunAwaitingOutcome  = "awaiting_partner_outcome"
	APISandboxRunCheckingState    = "checking_state"
	APISandboxRunConsumerRef      = "gregale-api-sandbox-run"
	APISandboxRunSharePrefix      = "sbx_run_"
	APISandboxRunQueued           = "queued"
	APISandboxRunRunning          = "running"
	APISandboxRunPassed           = "passed"
	APISandboxRunFailed           = "failed"
	APISandboxRunInterrupted      = "interrupted"
	APISandboxStepStripeWebhook   = "stripe_webhook"
	APISandboxStepWebhookReplay   = "webhook_duplicate"
	APISandboxStepWebhookProcess  = "webhook_processing"
	APISandboxStepOutboundJob     = "outbound_job"
	APISandboxStepOutboundQueued  = "outbound_webhook_queued"
	APISandboxStepOutboundAttempt = "outbound_webhook_attempt"
	APISandboxStepOutboundPayload = "outbound_webhook_payload"
	APISandboxStepPartnerOutcome  = "partner_webhook_outcome"
	APISandboxLostAcknowledgement = "sandbox: first callback acknowledgement intentionally dropped"

	// APISandboxWebhookReceiverPrefix is an internal, non-routable dispatcher target.
	APISandboxWebhookReceiverPrefix = "https://api-sandbox-webhook.invalid/"
)

// APISandboxJSONAssertion compares a field identified by an RFC 6901 JSON
// Pointer with the configured scalar value, including null.
type APISandboxJSONAssertion struct {
	Pointer string          `json:"pointer"`
	Equals  json.RawMessage `json:"equals"`
}

type APISandboxRunRequest struct {
	ScenarioRevision string `json:"scenario_revision"`
	Mode             string `json:"mode,omitempty"`
	PartnerWebhook   bool   `json:"partner_webhook,omitempty"`
}

// APISandboxRunStep keeps only redacted report metadata. It does not
// contain request or response bodies, URLs with query strings, or assertion values.
type APISandboxRunStep struct {
	Index                  int        `json:"index"`
	Title                  string     `json:"title"`
	Method                 string     `json:"method"`
	Path                   string     `json:"path,omitempty"`
	Kind                   string     `json:"kind,omitempty"`
	EventType              string     `json:"event_type,omitempty"`
	InvocationID           string     `json:"invocation_id,omitempty"`
	InvocationState        string     `json:"invocation_state,omitempty"`
	DeliveryID             string     `json:"delivery_id,omitempty"`
	AttemptNumber          int        `json:"attempt_number,omitempty"`
	ObservedAt             *time.Time `json:"observed_at,omitempty"`
	DeliveryAttempts       int        `json:"delivery_attempts,omitempty"`
	AcknowledgementDropped bool       `json:"acknowledgement_dropped,omitempty"`
	PartnerAppliedCount    *int       `json:"partner_applied_count,omitempty"`
	PartnerRequest         bool       `json:"partner_request,omitempty"`
	ExpectedStatus         int        `json:"expected_status,omitempty"`
	HTTPStatus             int        `json:"http_status,omitempty"`
	DurationMS             int64      `json:"duration_ms"`
	RequestID              string     `json:"request_id,omitempty"`
	FailureCode            string     `json:"failure_code,omitempty"`
	FailedAssertion        int        `json:"failed_assertion,omitempty"`
	FailedPointer          string     `json:"failed_pointer,omitempty"`
}

type APISandboxRunReport struct {
	ID                  string              `json:"id"`
	SourceDeploymentID  string              `json:"source_deployment_id"`
	SandboxDeploymentID string              `json:"sandbox_deployment_id,omitempty"`
	ScenarioRevision    string              `json:"scenario_revision"`
	ScenarioName        string              `json:"scenario_name"`
	Mode                string              `json:"mode"`
	PartnerWebhook      bool                `json:"partner_webhook,omitempty"`
	PartnerOutcomeUntil *time.Time          `json:"partner_outcome_until,omitempty"`
	PartnerStepCount    int                 `json:"partner_step_count,omitempty"`
	Status              string              `json:"status"`
	Steps               []APISandboxRunStep `json:"steps"`
	FailureCode         string              `json:"failure_code,omitempty"`
	CreatedAt           time.Time           `json:"created_at"`
	StartedAt           *time.Time          `json:"started_at,omitempty"`
	FinishedAt          *time.Time          `json:"finished_at,omitempty"`
}

// APISandboxPartnerStep is the internal permit for one in-flight guest request.
type APISandboxPartnerStep struct {
	RunID string
	Token string
	Step  APISandboxRunStep
}

func (r APISandboxRunReport) Terminal() bool {
	switch r.Status {
	case APISandboxRunPassed, APISandboxRunFailed, APISandboxRunInterrupted:
		return true
	default:
		return false
	}
}

type APISandboxRunsResponse struct {
	Runs            []APISandboxRunReport      `json:"runs"`
	ReceiverInvites []APISandboxReceiverInvite `json:"receiver_invites,omitempty"`
}

type APISandboxRunShareResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}
