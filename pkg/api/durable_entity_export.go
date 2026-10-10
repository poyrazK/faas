package api

import "encoding/json"

type DurableEntityStateExport struct {
	Format   int                `json:"format"`
	Entity   DurableEntityScope `json:"entity"`
	Version  uint64             `json:"version"`
	Data     json.RawMessage    `json:"data"`
	Checksum string             `json:"checksum"`
}

type DurableEntityRestoreRequest struct {
	Namespace              string                   `json:"namespace"`
	Key                    string                   `json:"key"`
	Environment            string                   `json:"environment,omitempty"`
	PlatformTenantID       string                   `json:"platform_tenant_id,omitempty"`
	RequestID              string                   `json:"request_id"`
	ExpectedVersion        uint64                   `json:"expected_version"`
	ValidationBundleSHA256 string                   `json:"validation_bundle_sha256,omitempty"`
	ValidationDeploymentID string                   `json:"validation_deployment_id,omitempty"`
	Export                 DurableEntityStateExport `json:"export"`
}

type DurableEntityRestoreResponse struct {
	Version  uint64 `json:"version"`
	Replayed bool   `json:"replayed"`
}

// A verdict is diagnostic. Restore validates again under its private claim.
type DurableEntityRestoreValidationResponse struct {
	BundleSHA256    string `json:"bundle_sha256,omitempty"`
	Isolation       string `json:"isolation,omitempty"`
	Valid           bool   `json:"valid"`
	DeploymentID    string `json:"deployment_id"`
	ExpectedVersion uint64 `json:"expected_version"`
	SourceVersion   uint64 `json:"source_version"`
}
