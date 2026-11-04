package api

import (
	"context"
	"net/http"
	"net/url"
	"slices"

	"github.com/google/uuid"
)

const (
	CodeEnvBatchRevisionConflict = "env_batch_revision_conflict"
	CodeEnvBatchRequestConflict  = "env_batch_request_conflict"
)

// EnvBatchRequest changes one scope atomically. RequestID is immutable across
// retries, including after an uncertain response. Values are non-secret config.
type EnvBatchRequest struct {
	RequestID        string            `json:"request_id"`
	ExpectedRevision *int64            `json:"expected_revision"`
	Values           map[string]string `json:"values,omitempty"`
	Delete           []string          `json:"delete,omitempty"`
}

// EnvBatchResponse is the durable, value-free receipt of one native transaction.
type EnvBatchResponse struct {
	RequestID    string              `json:"request_id"`
	AppID        string              `json:"app_id"`
	Scope        string              `json:"scope"`
	UpdatedKeys  []string            `json:"updated_keys"`
	DeletedKeys  []string            `json:"deleted_keys"`
	ConfigChange ConfigChangeReceipt `json:"config_change"`
}

func (r *EnvBatchRequest) Validate(limits Limits) *Problem {
	id, err := uuid.Parse(r.RequestID)
	if err != nil || id == uuid.Nil || r.ExpectedRevision == nil || *r.ExpectedRevision < 0 {
		return ErrValidation("request_id must be a UUID and expected_revision must be non-negative")
	}
	r.RequestID = id.String()
	if len(r.Values)+len(r.Delete) == 0 || len(r.Values) > limits.EnvVarsMax || len(r.Delete) > limits.EnvVarsMax {
		return ErrValidation("provide a bounded, non-empty set of variable changes")
	}
	seen := map[string]bool{}
	for key, value := range r.Values {
		if p := ValidateEnvKey(key); p != nil {
			return p
		}
		if p := (PutAppEnvRequest{Value: value}).Validate(limits.EnvValueMaxBytes); p != nil {
			return p
		}
		seen[key] = true
	}
	for _, key := range r.Delete {
		if p := ValidateEnvKey(key); p != nil {
			return p
		}
		if seen[key] {
			return ErrValidation("a variable can appear only once in a batch")
		}
		seen[key] = true
	}
	r.Delete = slices.Clone(r.Delete)
	slices.Sort(r.Delete)
	return nil
}

func (c *Client) ApplyEnvBatch(ctx context.Context, app, scope string, req EnvBatchRequest) (EnvBatchResponse, error) {
	var out EnvBatchResponse
	err := c.do(ctx, http.MethodPost, c.configWriteQuery("/v1/apps/"+url.PathEscape(app)+"/env/batch", scope), req, &out)
	return out, err
}
