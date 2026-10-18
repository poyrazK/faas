package api

import "time"

// MCPAdmissionEvidence contains only policy digests and gateway replay metadata.
// Accepted means the traffic transaction committed, not future fleet health.
type MCPAdmissionEvidence struct {
	Version             int                   `json:"version"`
	ID                  string                `json:"id"`
	DeploymentID        string                `json:"deployment_id"`
	TrafficPercent      int                   `json:"traffic_percent"`
	CapturedAt          time.Time             `json:"captured_at"`
	Outcome             string                `json:"outcome"`
	Reason              string                `json:"reason,omitempty"`
	ExpectedFingerprint string                `json:"expected_fingerprint"`
	ObservedFingerprint string                `json:"observed_fingerprint,omitempty"`
	ExpectedRevision    *int64                `json:"expected_revision,omitempty"`
	ObservedRevision    *int64                `json:"observed_revision,omitempty"`
	Gateways            []MCPAdmissionGateway `json:"gateways"`
}

type MCPAdmissionGateway struct {
	NodeName   string     `json:"node_name"`
	BootID     *string    `json:"boot_id"`
	Revision   *int64     `json:"revision"`
	ObservedAt *time.Time `json:"observed_at"`
}
