package api

// adr: 647
import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type QueueGlobalQuotaDeliveryRequest struct {
	Region    string `json:"region"`
	Purpose   string `json:"purpose"`
	ConsentID string `json:"runtime_consent_id,omitempty"`
}

// Accepted records a verified regional receipt. Runtime activation and drain
// confirmation remain separate operations.
type GlobalQuotaDeliveryResponse struct {
	ID            string                              `json:"id"`
	GlobalAppID   string                              `json:"global_app_id"`
	AllocationID  string                              `json:"allocation_id"`
	Generation    int64                               `json:"generation"`
	IssuerID      string                              `json:"issuer_id"`
	Purpose       string                              `json:"purpose"`
	ConsentID     string                              `json:"runtime_consent_id,omitempty"`
	Binding       GlobalRuntimeConsentBindingResponse `json:"binding"`
	RetirementID  string                              `json:"retirement_id,omitempty"`
	CreatedAt     time.Time                           `json:"created_at"`
	Status        string                              `json:"status"`
	Attempts      int32                               `json:"attempts"`
	NextAttemptAt time.Time                           `json:"next_attempt_at"`
	ClaimUntil    *time.Time                          `json:"claim_until,omitempty"`
	AcceptedAt    *time.Time                          `json:"accepted_at,omitempty"`
	LastCode      string                              `json:"last_code,omitempty"`
}

func globalQuotaDeliveryPath(slug, allocation, delivery string) (string, error) {
	p, err := globalRuntimeQuotaPath(slug, allocation)
	if err != nil {
		return "", err
	}
	id, err := uuid.Parse(delivery)
	if err != nil || id == uuid.Nil || id.String() != delivery {
		return "", fmt.Errorf("delivery_id must be a canonical nonzero UUID")
	}
	return p + "/deliveries/" + delivery, nil
}

func (c *Client) QueueGlobalQuotaDelivery(ctx context.Context, slug, allocation, delivery string, req QueueGlobalQuotaDeliveryRequest) (GlobalQuotaDeliveryResponse, error) {
	var out GlobalQuotaDeliveryResponse
	p, err := globalQuotaDeliveryPath(slug, allocation, delivery)
	if err != nil {
		return out, err
	}
	return out, c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "PUT", p, req, &out, delivery)
}

func (c *Client) GetGlobalQuotaDelivery(ctx context.Context, slug, allocation, delivery string) (GlobalQuotaDeliveryResponse, error) {
	var out GlobalQuotaDeliveryResponse
	p, err := globalQuotaDeliveryPath(slug, allocation, delivery)
	if err != nil {
		return out, err
	}
	return out, c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "GET", p, nil, &out, "")
}

func (c *Client) RetryGlobalQuotaDelivery(ctx context.Context, slug, allocation, delivery string) (GlobalQuotaDeliveryResponse, error) {
	var out GlobalQuotaDeliveryResponse
	p, err := globalQuotaDeliveryPath(slug, allocation, delivery)
	if err != nil {
		return out, err
	}
	return out, c.doWithClientAndIdempotencyKey(ctx, cellDeploymentHTTP(c.http), "POST", p+"/retry", nil, &out, "")
}
