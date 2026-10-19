package api

import "time"

type ProductPilotManifest struct {
	Version   int                    `json:"version"`
	App       string                 `json:"app"`
	AppID     string                 `json:"app_id"`
	Feature   string                 `json:"feature"`
	Cases     []ProductPilotCase     `json:"cases"`
	HelpCases []ProductPilotHelpCase `json:"help_cases,omitempty"`
	Journeys  []ProductPilotJourney  `json:"journeys,omitempty"`
}

type ProductPilotCase struct {
	Name                     string         `json:"name"`
	Baseline                 bool           `json:"baseline"`
	TenantID                 string         `json:"tenant_id"`
	Plan                     string         `json:"plan"`
	Revision                 string         `json:"revision"`
	OperationID              string         `json:"operation_id"`
	ExpectedState            OperationState `json:"expected_state"`
	ExpectedChargeMillicents *int64         `json:"expected_charge_millicents"`
	OutputFile               string         `json:"output_file,omitempty"`
	OutputSHA256             string         `json:"output_sha256,omitempty"`
}

// A repeatable read-only ledger comparison is not native qualification or
// evidence that browser login/download/retry/interruption scenarios were run.
type ProductPilotReport struct {
	Version            int                           `json:"version"`
	App                string                        `json:"app"`
	AppID              string                        `json:"app_id"`
	Feature            string                        `json:"feature"`
	ManifestSHA256     string                        `json:"manifest_sha256"`
	ObservedAt         time.Time                     `json:"observed_at"`
	LedgerState        string                        `json:"ledger_state"`
	QualificationState string                        `json:"qualification_state"`
	Checks             []ProductDoctorCheck          `json:"checks"`
	Operations         []ProductOperationExplanation `json:"operations"`
	Reviews            []ProductBillingReview        `json:"reviews"`
	Doctor             ProductDoctorReport           `json:"doctor"`
	Outputs            []ProductPilotOutput          `json:"outputs"`
	HelpState          string                        `json:"help_state"`
	Help               []ProductPilotHelpResult      `json:"help"`
	JourneyState       string                        `json:"journey_state"`
	Journeys           []ProductPilotJourneyResult   `json:"journeys"`
}

// Help expectations contain hashes, never customer messages or owner responses.
type ProductPilotHelpCase struct {
	Name            string `json:"name"`
	TenantID        string `json:"tenant_id"`
	RequestID       string `json:"request_id"`
	Kind            string `json:"kind"`
	Feature         string `json:"feature,omitempty"`
	OperationID     string `json:"operation_id,omitempty"`
	ExpectedStatus  string `json:"expected_status"`
	ExpectedVersion int64  `json:"expected_version"`
	MessageSHA256   string `json:"message_sha256"`
	ResponseSHA256  string `json:"response_sha256"`
	ContextSHA256   string `json:"context_sha256"`
}

type ProductPilotHelpResult struct {
	Name           string    `json:"name"`
	TenantID       string    `json:"tenant_id"`
	RequestID      string    `json:"request_id"`
	Kind           string    `json:"kind"`
	Status         string    `json:"status"`
	Version        int64     `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	MessageSHA256  string    `json:"message_sha256"`
	ResponseSHA256 string    `json:"response_sha256"`
	ContextSHA256  string    `json:"context_sha256"`
	Result         string    `json:"result"`
}

// Journey timings are operator-recorded browser observations, not qualification.
type ProductPilotJourney struct {
	Name          string                    `json:"name"`
	TenantID      string                    `json:"tenant_id"`
	OperationID   string                    `json:"operation_id"`
	HelpRequestID string                    `json:"help_request_id"`
	StartedAt     time.Time                 `json:"started_at"`
	Steps         []ProductPilotJourneyStep `json:"steps"`
}

type ProductPilotJourneyStep struct {
	Stage          string    `json:"stage"`
	Outcome        string    `json:"outcome"`
	ObservedAt     time.Time `json:"observed_at"`
	EvidenceFile   string    `json:"evidence_file"`
	EvidenceSHA256 string    `json:"evidence_sha256"`
}

type ProductPilotJourneyResult struct {
	Name             string                    `json:"name"`
	TenantID         string                    `json:"tenant_id"`
	OperationID      string                    `json:"operation_id"`
	HelpRequestID    string                    `json:"help_request_id"`
	State            string                    `json:"state"`
	FirstSuccessMS   *int64                    `json:"first_success_ms,omitempty"`
	HelpResolutionMS *int64                    `json:"help_resolution_ms,omitempty"`
	BlockedSteps     int                       `json:"blocked_steps"`
	Steps            []ProductPilotJourneyStep `json:"steps"`
	Evidence         []ProductPilotOutput      `json:"evidence"`
}

type ProductPilotOutput struct {
	Case   string `json:"case"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Source string `json:"source"`
}
