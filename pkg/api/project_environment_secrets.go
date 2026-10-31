// adr: 1139
package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// CodePlanLimitProjectSecrets is returned when a new key would exceed the
// plan's ProjectSecretCountMax for one project environment.
const CodePlanLimitProjectSecrets = "plan_limit_project_secrets"

// ProjectSecretsRevisionKey is the qualification secret revision entry that
// covers an environment's shared secrets. Workload slugs cannot contain a
// colon, so it never collides with one. The server records it; clients never
// send it.
const ProjectSecretsRevisionKey = "project:secrets"

// ProjectEnvironmentSharedSecret describes one secret shared by every service
// of a project environment (ADR-1139). Values are write-only: no response
// carries plaintext or ciphertext. ValueHash is the HMAC fingerprint app
// secrets expose too, for comparing values without revealing them.
type ProjectEnvironmentSharedSecret struct {
	Key       string `json:"key"`
	Version   int64  `json:"version"`
	Kid       string `json:"kid"`
	ValueHash string `json:"value_hash"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// ProjectEnvironmentSharedSecretsResponse lists an environment's shared secrets.
type ProjectEnvironmentSharedSecretsResponse struct {
	ProjectSlug string                           `json:"project_slug"`
	Environment string                           `json:"environment"`
	Secrets     []ProjectEnvironmentSharedSecret `json:"secrets"`
	Limit       int                              `json:"limit"`
}

// SetProjectEnvironmentSharedSecretRequest sets or rotates one shared secret.
type SetProjectEnvironmentSharedSecretRequest struct {
	Value string `json:"value"`
}

// ProjectEnvironmentSharedSecretResponse is the stored secret after a write.
type ProjectEnvironmentSharedSecretResponse struct {
	ProjectSlug string                         `json:"project_slug"`
	Environment string                         `json:"environment"`
	Secret      ProjectEnvironmentSharedSecret `json:"secret"`
}

// ErrPlanLimitProjectSecrets is the 403 for a new key past the plan's
// per-environment shared secret limit.
func ErrPlanLimitProjectSecrets(l Limits, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanLimitProjectSecrets,
		"Project secret limit reached",
		fmt.Sprintf("%s plan allows %d shared secret(s) per project environment; this environment has %d.", l.Plan, l.ProjectSecretCountMax, observed)).
		WithLimit(int64(l.ProjectSecretCountMax), int64(observed)).
		WithDocs(docsBase + "/secrets#project-environment-secrets")
}

func projectEnvironmentSecretsPath(projectSlug, environmentSlug string) string {
	return "/v1/projects/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug) + "/secrets"
}

// ListProjectEnvironmentSecrets lists the secrets a project environment
// shares with its services (metadata only).
func (c *Client) ListProjectEnvironmentSecrets(ctx context.Context, projectSlug, environmentSlug string) (ProjectEnvironmentSharedSecretsResponse, error) {
	var out ProjectEnvironmentSharedSecretsResponse
	return out, c.do(ctx, http.MethodGet, projectEnvironmentSecretsPath(projectSlug, environmentSlug), nil, &out)
}

// SetProjectEnvironmentSecret creates a shared secret or replaces its value.
func (c *Client) SetProjectEnvironmentSecret(ctx context.Context, projectSlug, environmentSlug, key, value string) (ProjectEnvironmentSharedSecretResponse, error) {
	var out ProjectEnvironmentSharedSecretResponse
	path := projectEnvironmentSecretsPath(projectSlug, environmentSlug) + "/" + url.PathEscape(key)
	return out, c.do(ctx, http.MethodPut, path, SetProjectEnvironmentSharedSecretRequest{Value: value}, &out)
}

// RotateProjectEnvironmentSecret replaces the value of an existing shared
// secret; an unknown key is an error.
func (c *Client) RotateProjectEnvironmentSecret(ctx context.Context, projectSlug, environmentSlug, key, value string) (ProjectEnvironmentSharedSecretResponse, error) {
	var out ProjectEnvironmentSharedSecretResponse
	path := projectEnvironmentSecretsPath(projectSlug, environmentSlug) + "/" + url.PathEscape(key) + "/rotate"
	return out, c.do(ctx, http.MethodPost, path, SetProjectEnvironmentSharedSecretRequest{Value: value}, &out)
}

// DeleteProjectEnvironmentSecret removes a shared secret.
func (c *Client) DeleteProjectEnvironmentSecret(ctx context.Context, projectSlug, environmentSlug, key string) error {
	path := projectEnvironmentSecretsPath(projectSlug, environmentSlug) + "/" + url.PathEscape(key)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}
