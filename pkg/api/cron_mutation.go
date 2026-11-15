package api

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

const CronMutationReceiptProofHeader = "X-Gregale-Cron-Receipt-Proof"

// CronMutationRequest binds a reviewed update or deletion to immutable target
// identities, the observed execution state and one private recovery proof.
// ExpiresAt is fixed at review time; retrying never extends write authority.
type CronMutationRequest struct {
	RequestID     string    `json:"request_id"`
	AppID         string    `json:"app_id"`
	CronID        string    `json:"cron_id"`
	ExpectedState string    `json:"expected_state"`
	ReceiptProof  string    `json:"receipt_proof"`
	ExpiresAt     time.Time `json:"expires_at"`
	Delete        bool      `json:"delete,omitempty"`
	Schedule      *string   `json:"schedule,omitempty"`
	Path          *string   `json:"path,omitempty"`
	Timezone      *string   `json:"timezone,omitempty"`
	Enabled       *bool     `json:"enabled,omitempty"`
}

func (r CronMutationRequest) Valid() bool {
	for _, value := range []string{r.RequestID, r.AppID, r.CronID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || value == r.RequestID && id.String() != value {
			return false
		}
	}
	changes := r.Schedule != nil || r.Path != nil || r.Timezone != nil || r.Enabled != nil
	return ValidEnvBatchReceiptProof(r.ExpectedState) && ValidEnvBatchReceiptProof(r.ReceiptProof) && !r.ExpiresAt.IsZero() && r.Delete != changes
}

// CronMutationReceipt contains no cron configuration or recovery credential.
// Completion describes the mutation, never completion of already running work.
type CronMutationReceipt struct {
	SchemaVersion       int       `json:"schema_version"`
	RequestID           string    `json:"request_id"`
	AppID               string    `json:"app_id"`
	CronID              string    `json:"cron_id"`
	Delete              bool      `json:"delete"`
	Status              string    `json:"status"`
	Phase               string    `json:"phase"`
	Cancelled           int64     `json:"cancelled"`
	DetachedInvocations int64     `json:"detached_invocations"`
	DetachedTasks       int64     `json:"detached_tasks"`
	AcceptedAt          time.Time `json:"accepted_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	ExpiresAt           time.Time `json:"expires_at"`
	RetainedUntil       time.Time `json:"retained_until"`
}

func (c *Client) ApplyCronMutation(ctx context.Context, app string, request CronMutationRequest) (CronMutationReceipt, error) {
	var out CronMutationReceipt
	endpoint := "/cron-mutations"
	if request.Delete {
		endpoint = "/cron-deletions"
	}
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(app)+endpoint, request, &out)
	return out, err
}

func (c *Client) GetCronMutation(ctx context.Context, app, requestID, proof string) (CronMutationReceipt, error) {
	var out CronMutationReceipt
	headers := http.Header{}
	headers.Set(CronMutationReceiptProofHeader, proof)
	err := c.doWithHeaders(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(app)+"/cron-mutations/"+url.PathEscape(requestID), nil, &out, headers)
	return out, err
}
