package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// HostedTaskSourceEntry preserves authored source bytes and normalized modes.
// Only prepare accepts source; public receipts never contain these entries.
type HostedTaskSourceEntry struct {
	Path      string `json:"path"`
	Mode      uint32 `json:"mode"`
	Directory bool   `json:"directory,omitempty"`
	Content   []byte `json:"content,omitempty"`
}
type HostedTaskSourcePrepare struct {
	Review  json.RawMessage                    `json:"review"`
	Sources map[string][]HostedTaskSourceEntry `json:"sources"`
}
type HostedTaskSourceIntent struct {
	Version                    int             `json:"version"`
	ID                         string          `json:"id"`
	AccountID                  string          `json:"account_id"`
	RequesterKeyID             string          `json:"requester_key_id"`
	IntentSHA256               string          `json:"intent_sha256"`
	ReviewSHA256               string          `json:"review_sha256"`
	Review                     json.RawMessage `json:"review"`
	CreatedAt                  time.Time       `json:"created_at"`
	ExpiresAt                  time.Time       `json:"expires_at"`
	SourceState                string          `json:"source_state"`
	ReadyToApply               bool            `json:"ready_to_apply"`
	BindingsVerified           bool            `json:"bindings_verified"`
	OwnerAuthorizationRequired bool            `json:"owner_authorization_required"`
}

func (c *Client) PrepareHostedTaskSourceIntent(ctx context.Context, id string, in HostedTaskSourcePrepare) (HostedTaskSourceIntent, error) {
	var out HostedTaskSourceIntent
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPut, "/v1/task-tool-source-intents/"+url.PathEscape(id), in, &out)
	return out, err
}
func (c *Client) GetHostedTaskSourceIntent(ctx context.Context, id string) (HostedTaskSourceIntent, error) {
	var out HostedTaskSourceIntent
	err := c.do(ctx, http.MethodGet, "/v1/task-tool-source-intents/"+url.PathEscape(id), nil, &out)
	return out, err
}

// TaskReleaseReview is an offline source review, not an executable publication
// plan. It contains neither credential references nor credential values.
type TaskReleaseReview struct {
	Version            int                 `json:"version"`
	Status             string              `json:"status"`
	Fingerprint        string              `json:"release_fingerprint"`
	Protocol           string              `json:"protocol"`
	WebApp             string              `json:"web_app"`
	WorkerApp          string              `json:"worker_app"`
	WorkerRuntime      string              `json:"worker_runtime"`
	ObserverApp        string              `json:"observer_app"`
	ObserverMetricApp  string              `json:"observer_metric_app"`
	PreviousWorkerApps []string            `json:"previous_worker_apps"`
	Endpoint           string              `json:"endpoint"`
	AccessMode         string              `json:"access_mode"`
	ToolScopes         map[string][]string `json:"tool_scopes"`
	ReadinessRoles     []string            `json:"readiness_roles"`
	RequiredSetup      []string            `json:"required_setup"`
	TimeoutSeconds     int                 `json:"release_timeout_seconds"`
	WebLimits          TaskReviewLimits    `json:"web_configured_limits"`
	WorkerLimits       TaskReviewLimits    `json:"worker_configured_limits"`
	MigrationsRequired bool                `json:"migrations_required"`
	BindingsVerified   bool                `json:"bindings_verified"`
	ReadyToApply       bool                `json:"ready_to_apply"`
}

// TaskReviewLimits contains only explicitly configured overrides. Omitted
// values retain starter defaults; deployed quotas and bindings need live checks.
type TaskReviewLimits struct {
	TTLSeconds             int `json:"ttl_seconds,omitempty"`
	WorkerConcurrency      int `json:"worker_concurrency,omitempty"`
	MaxAttempts            int `json:"max_attempts,omitempty"`
	MaxRunning             int `json:"max_running,omitempty"`
	MaxRunningPerOwner     int `json:"max_running_per_owner,omitempty"`
	MaxOutstanding         int `json:"max_outstanding,omitempty"`
	MaxOutstandingPerOwner int `json:"max_outstanding_per_owner,omitempty"`
	ShutdownTimeoutMS      int `json:"shutdown_timeout_ms,omitempty"`
}

// HostedTaskSourceReview pins authored source and native app baselines without
// granting owner authorization, credential access or runtime readiness.
type HostedTaskSourceReview struct {
	TaskReleaseReview
	Kind      string               `json:"kind"`
	AccountID string               `json:"account_id"`
	Sources   map[string]string    `json:"source_sha256"`
	Apps      []TaskToolReleaseApp `json:"apps"`
}
