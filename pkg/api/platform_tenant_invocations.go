package api

import (
	"encoding/json"
	"time"
)

// PlatformTenantInvocationResponse exposes customer work without the original
// request headers, payload, deployment or account metadata.
type PlatformTenantInvocationResponse struct {
	ID          string          `json:"id"`
	State       string          `json:"state"`
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	Attempts    int             `json:"attempts"`
	Result      json.RawMessage `json:"result,omitempty"`
	LastError   string          `json:"last_error,omitempty"`
	Outcome     *string         `json:"outcome,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}
