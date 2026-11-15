package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// TaskToolReleaseApp pins a stack role to a native default-environment review.
type TaskToolReleaseApp struct {
	Role   string           `json:"role"`
	App    string           `json:"app"`
	Review AppReleaseReview `json:"review"`
}

// TaskToolReleaseIntent contains review metadata, never local paths, executable
// arguments, database URLs, keys, or credential references.
type TaskToolReleaseIntent struct {
	ReleaseFingerprint string               `json:"release_fingerprint"`
	WorkerRuntime      string               `json:"worker_runtime"`
	Protocol           string               `json:"protocol"`
	ObserverMetricApp  string               `json:"observer_metric_app"`
	Endpoint           string               `json:"endpoint"`
	AccessMode         string               `json:"access_mode"`
	ToolScopes         map[string][]string  `json:"tool_scopes"`
	Apps               []TaskToolReleaseApp `json:"apps"`
	RequiredSetup      []string             `json:"required_setup"`
	Reason             string               `json:"reason"`
}

// TaskToolReleaseIntentRecord is an approval journal, not execution authority.
// BindingsVerified and ReadyToApply remain false in this milestone.
type TaskToolReleaseIntentRecord struct {
	Version          int                   `json:"version"`
	ID               string                `json:"id"`
	AccountID        string                `json:"account_id"`
	RequesterKeyID   string                `json:"requester_key_id"`
	IntentSHA256     string                `json:"intent_sha256"`
	Intent           TaskToolReleaseIntent `json:"intent"`
	Status           string                `json:"status"`
	CreatedAt        time.Time             `json:"created_at"`
	ExpiresAt        time.Time             `json:"expires_at"`
	DecidedAt        *time.Time            `json:"decided_at,omitempty"`
	BindingsVerified bool                  `json:"bindings_verified"`
	ReadyToApply     bool                  `json:"ready_to_apply"`
}

// TaskToolReleaseIntentCheck supplies a freshly recomputed local fingerprint.
type TaskToolReleaseIntentCheck struct {
	ReleaseFingerprint string `json:"release_fingerprint"`
}

// TaskToolReleaseIntentDecision binds an owner decision to the complete intent.
type TaskToolReleaseIntentDecision struct {
	IntentSHA256       string `json:"intent_sha256"`
	ReleaseFingerprint string `json:"release_fingerprint"`
	Decision           string `json:"decision"`
}

func (c *Client) PrepareTaskToolReleaseIntent(ctx context.Context, id string, in TaskToolReleaseIntent) (TaskToolReleaseIntentRecord, error) {
	var out TaskToolReleaseIntentRecord
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPut, "/v1/task-tool-release-intents/"+url.PathEscape(id), in, &out)
	return out, err
}

func (c *Client) GetTaskToolReleaseIntent(ctx context.Context, id string) (TaskToolReleaseIntentRecord, error) {
	var out TaskToolReleaseIntentRecord
	err := c.do(ctx, http.MethodGet, "/v1/task-tool-release-intents/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) CheckTaskToolReleaseIntent(ctx context.Context, id string, in TaskToolReleaseIntentCheck) (TaskToolReleaseIntentRecord, error) {
	var out TaskToolReleaseIntentRecord
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, "/v1/task-tool-release-intents/"+url.PathEscape(id)+"/check", in, &out)
	return out, err
}

func (c *Client) DecideTaskToolReleaseIntent(ctx context.Context, id string, in TaskToolReleaseIntentDecision) (TaskToolReleaseIntentRecord, error) {
	var out TaskToolReleaseIntentRecord
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPut, "/v1/task-tool-release-intents/"+url.PathEscape(id)+"/decision", in, &out)
	return out, err
}
