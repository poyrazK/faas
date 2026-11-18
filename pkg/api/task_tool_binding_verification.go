package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// HostedTaskBindingSelection contains native references, never credential values.
type HostedTaskBindingSelection struct {
	IntentSHA256         string `json:"intent_sha256"`
	ReviewSHA256         string `json:"review_sha256"`
	Namespace            string `json:"namespace"`
	WebDatabaseRef       string `json:"web_database_ref"`
	WorkerDatabaseRef    string `json:"worker_database_ref"`
	MigrationDatabaseRef string `json:"migration_database_ref"`
	ObserverDatabaseRef  string `json:"observer_database_ref"`
}

type HostedTaskBindingCheck struct {
	BindingSHA256 string `json:"binding_sha256"`
}
type HostedTaskBindingResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// HostedTaskBindingVerification is bounded historical evidence. Passing probes
// do not confer execution authority or complete application readiness.
type HostedTaskBindingVerification struct {
	Version                    int                       `json:"version"`
	AccountID                  string                    `json:"account_id"`
	IntentID                   string                    `json:"intent_id"`
	IntentSHA256               string                    `json:"intent_sha256"`
	ReviewSHA256               string                    `json:"review_sha256"`
	BindingSHA256              string                    `json:"binding_sha256"`
	Status                     string                    `json:"status"`
	Checks                     []HostedTaskBindingResult `json:"checks"`
	CreatedAt                  time.Time                 `json:"created_at"`
	ExpiresAt                  time.Time                 `json:"expires_at"`
	CheckedAt                  *time.Time                `json:"checked_at,omitempty"`
	ValidUntil                 *time.Time                `json:"valid_until,omitempty"`
	ReadyToApply               bool                      `json:"ready_to_apply"`
	BindingsVerified           bool                      `json:"bindings_verified"`
	OwnerAuthorizationRequired bool                      `json:"owner_authorization_required"`
}

func (c *Client) SelectHostedTaskBindings(ctx context.Context, id string, in HostedTaskBindingSelection) (HostedTaskBindingVerification, error) {
	var out HostedTaskBindingVerification
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPut, "/v1/task-tool-source-intents/"+url.PathEscape(id)+"/bindings", in, &out)
	return out, err
}
func (c *Client) GetHostedTaskBindings(ctx context.Context, id string) (HostedTaskBindingVerification, error) {
	var out HostedTaskBindingVerification
	err := c.do(ctx, http.MethodGet, "/v1/task-tool-source-intents/"+url.PathEscape(id)+"/bindings", nil, &out)
	return out, err
}
func (c *Client) CheckHostedTaskBindings(ctx context.Context, id string, in HostedTaskBindingCheck) (HostedTaskBindingVerification, error) {
	var out HostedTaskBindingVerification
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, "/v1/task-tool-source-intents/"+url.PathEscape(id)+"/bindings/check", in, &out)
	return out, err
}
func (c *Client) RevokeHostedTaskBindings(ctx context.Context, id string, in HostedTaskBindingCheck) (HostedTaskBindingVerification, error) {
	var out HostedTaskBindingVerification
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, "/v1/task-tool-source-intents/"+url.PathEscape(id)+"/bindings/revoke", in, &out)
	return out, err
}
