package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/safetext"
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
	AcceptedAt              time.Time `json:"accepted_at"`
}

// ServiceAbortRequestHash includes the complete reviewed request, with UUID
// spellings canonicalized and the same bounded reason as native admission.
func ServiceAbortRequestHash(appID string, req RecoverRolloutRequest) string {
	canonical := func(id string) string {
		if u, err := uuid.Parse(id); err == nil {
			return u.String()
		}
		return id
	}
	req.DeploymentID = canonical(req.DeploymentID)
	req.ExpectedPredecessorDeploymentID = canonical(req.ExpectedPredecessorDeploymentID)
	req.Reason = safetext.Truncate(req.Reason, AuditReasonMaxBytes)
	if req.ExpectedReview != nil {
		r := *req.ExpectedReview
		r.AppID, r.ProjectID, r.EnvironmentID = canonical(r.AppID), canonical(r.ProjectID), canonical(r.EnvironmentID)
		r.LatestDeploymentID = canonical(r.LatestDeploymentID)
		r.ServingDeploymentIDs = make([]string, len(req.ExpectedReview.ServingDeploymentIDs))
		for i, id := range req.ExpectedReview.ServingDeploymentIDs {
			r.ServingDeploymentIDs[i] = canonical(id)
		}
		req.ExpectedReview = &r
	}
	body, _ := json.Marshal(struct {
		AppID   string
		Request RecoverRolloutRequest
	}{canonical(appID), req})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// GetServiceAbortReceipt reads retained historical acceptance without replaying
// recovery or consulting the mutable current handoff.
func (c *Client) GetServiceAbortReceipt(ctx context.Context, slug, attemptID string) (ServiceAbortReceipt, error) {
	var out ServiceAbortReceipt
	err := c.doWithoutIdempotencyKey(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/service-aborts/"+url.PathEscape(attemptID), nil, &out)
	return out, err
}
