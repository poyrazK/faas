package api

import "encoding/json"

const (
	APISandboxScenarioMaxBytes        = 24 << 10
	APISandboxSeedMaxBytes            = 8 << 10
	APISandboxRequestMaxBytes         = 2 << 10
	APISandboxWebhookMaxBytes         = 8 << 10
	APISandboxScenarioMaxItems        = 8
	APISandboxScenarioMaxName         = 100
	APISandboxScenarioLibraryMaxItems = 50
)

type APISandboxExampleRequest struct {
	Title          string                    `json:"title"`
	Method         string                    `json:"method"`
	Path           string                    `json:"path"`
	Body           json.RawMessage           `json:"body,omitempty"`
	ExpectedStatus int                       `json:"expected_status"`
	Assertions     []APISandboxJSONAssertion `json:"assertions,omitempty"`
}

// APISandboxScenarioGuide is the part of the scenario shared with recipients.
type APISandboxScenarioGuide struct {
	Name      string                     `json:"name"`
	SeededIDs map[string]string          `json:"seeded_ids,omitempty"`
	Requests  []APISandboxExampleRequest `json:"requests"`
}

// APISandboxStripeWebhook is an owner-configured synthetic event fixture.
type APISandboxStripeWebhook struct {
	DeliveryPath string          `json:"delivery_path"`
	EventType    string          `json:"event_type"`
	DataObject   json.RawMessage `json:"data_object"`
}

// APISandboxOutboundWebhook checks a managed async job's completion callback.
type APISandboxOutboundWebhook struct {
	RetryOnce  bool                      `json:"retry_once,omitempty"`
	Assertions []APISandboxJSONAssertion `json:"assertions,omitempty"`
}

type APISandboxScenarioInput struct {
	APISandboxScenarioGuide
	Seed            json.RawMessage            `json:"seed"`
	StripeWebhook   *APISandboxStripeWebhook   `json:"stripe_webhook,omitempty"`
	OutboundWebhook *APISandboxOutboundWebhook `json:"outbound_webhook,omitempty"`
}

type APISandboxScenario struct {
	APISandboxScenarioInput
	Revision        string `json:"revision"`
	SourceRequestID string `json:"-"`
}

type APISandboxScenarioReceipt struct {
	APISandboxScenarioGuide
	Revision string `json:"revision"`
}

func (scenario *APISandboxScenario) Receipt() *APISandboxScenarioReceipt {
	if scenario == nil {
		return nil
	}
	return &APISandboxScenarioReceipt{
		APISandboxScenarioGuide: scenario.APISandboxScenarioGuide,
		Revision:                scenario.Revision,
	}
}

type APISandboxScenarioResponse struct {
	Scenario        *APISandboxScenario `json:"scenario"`
	SourceRequestID string              `json:"source_request_id,omitempty"`
}

type APISandboxScenarioSaveRequest struct {
	APISandboxScenarioInput
	SourceRequestID  string  `json:"source_request_id,omitempty"`
	ExpectedRevision *string `json:"expected_revision,omitempty"`
}

type APISandboxScenarioDraft struct {
	DeploymentID     string `json:"deployment_id"`
	SourceRequestID  string `json:"source_request_id"`
	ExpectedRevision string `json:"expected_revision"`
	Method           string `json:"method"`
	RouteTemplate    string `json:"route_template,omitempty"`
}

type APISandboxLibraryScenarioSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Revision string `json:"revision"`
}

type APISandboxLibraryScenario struct {
	ID              string             `json:"id"`
	Scenario        APISandboxScenario `json:"scenario"`
	SourceRequestID string             `json:"source_request_id,omitempty"`
}

type APISandboxScenarioLibraryResponse struct {
	Scenarios []APISandboxLibraryScenarioSummary `json:"scenarios"`
}
