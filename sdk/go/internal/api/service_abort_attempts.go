package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

const ServiceAbortAttemptHeader = "Service-Abort-Attempt-ID"

type serviceAbortAttemptKey struct{}

// ContextWithServiceAbortAttempt affects only reviewed exact service recovery.
func ContextWithServiceAbortAttempt(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, serviceAbortAttemptKey{}, id)
}

func serviceAbortAttemptHeaders(ctx context.Context) http.Header {
	id, _ := ctx.Value(serviceAbortAttemptKey{}).(string)
	if id == "" {
		return nil
	}
	h := make(http.Header)
	h.Set(ServiceAbortAttemptHeader, id)
	return h
}

// ServiceAbortReceipt is immutable acceptance evidence. RequestID identifies
// the native reverse handoff; acceptance never establishes ACK/drain completion.
type ServiceAbortReceipt struct {
	AttemptID               string    `json:"attempt_id"`
	RequestHash             string    `json:"request_hash"`
	AppID                   string    `json:"app_id"`
	DeploymentID            string    `json:"deployment_id"`
	PredecessorDeploymentID string    `json:"predecessor_deployment_id"`
	RequestID               string    `json:"request_id"`
	Scope                   string    `json:"scope"`
	ProjectID               string    `json:"project_id,omitempty"`
	EnvironmentID           string    `json:"environment_id,omitempty"`
	AcceptedAt              time.Time `json:"accepted_at"`
}

// GetServiceAbortReceipt reads retained historical acceptance without replaying
// recovery or consulting the mutable current handoff.
func (c *Client) GetServiceAbortReceipt(ctx context.Context, slug, attemptID string) (ServiceAbortReceipt, error) {
	var out ServiceAbortReceipt
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/service-aborts/"+url.PathEscape(attemptID), nil, &out)
	return out, err
}
