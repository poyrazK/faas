package api

import "encoding/json"

// DurableEntityInvokeRequest is an operator-authenticated preview surface.
// RequestID is durable replay identity, independent of HTTP Idempotency-Key.
type DurableEntityInvokeRequest struct {
	Namespace        string          `json:"namespace"`
	Key              string          `json:"key"`
	RequestID        string          `json:"request_id"`
	Payload          json.RawMessage `json:"payload"`
	Environment      string          `json:"environment,omitempty"`
	PlatformTenantID string          `json:"platform_tenant_id,omitempty"`
}

type DurableEntityInvokeResponse struct {
	Value    json.RawMessage `json:"value"`
	Version  uint64          `json:"version"`
	Replayed bool            `json:"replayed"`
}
