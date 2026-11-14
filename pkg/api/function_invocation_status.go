package api

import (
	"encoding/json"
	"time"
)

// FunctionInvocationStatus is what a function reads about an invocation it
// sent with send(), at GET http://api.svc.gregale:10081/v1/invocations/{id}
// (ADR-20261112160000).
type FunctionInvocationStatus struct {
	ID          string          `json:"id"`
	State       string          `json:"state"`
	Done        bool            `json:"done"`
	Outcome     string          `json:"outcome,omitempty"`
	Attempts    int             `json:"attempts"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}
