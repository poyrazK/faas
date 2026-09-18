package provider

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type client struct {
	baseURL string
	token   string
	http    *http.Client
}

type apiError struct {
	status int
	code   string
	title  string
	detail string
}

func (e *apiError) Error() string {
	parts := make([]string, 0, 3)
	if e.code != "" {
		parts = append(parts, e.code)
	}
	if e.title != "" {
		parts = append(parts, e.title)
	}
	if e.detail != "" {
		parts = append(parts, e.detail)
	}
	if len(parts) == 0 {
		return fmt.Sprintf("Gregale API request failed with HTTP %d", e.status)
	}
	return strings.Join(parts, ": ")
}

type problemResponse struct {
	Code   string `json:"code"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

type appRequest struct {
	Slug            string `json:"slug"`
	Visibility      string `json:"visibility,omitempty"`
	Type            string `json:"type,omitempty"`
	Runtime         string `json:"runtime,omitempty"`
	ResourceProfile string `json:"resource_profile,omitempty"`
	RAMMB           *int   `json:"ram_mb,omitempty"`
	MaxConcurrency  *int   `json:"max_concurrency,omitempty"`
	IdleTimeoutS    *int   `json:"idle_timeout_s,omitempty"`
	HealthPath      string `json:"health_path,omitempty"`
	HealthPathWakes *bool  `json:"health_path_wakes,omitempty"`
}

type appPatch struct {
	Visibility      *string `json:"visibility,omitempty"`
	ResourceProfile *string `json:"resource_profile,omitempty"`
	RAMMB           *int    `json:"ram_mb,omitempty"`
	MaxConcurrency  *int    `json:"max_concurrency,omitempty"`
	IdleTimeoutS    *int    `json:"idle_timeout_s,omitempty"`
	HealthPath      *string `json:"health_path,omitempty"`
	HealthPathWakes *bool   `json:"health_path_wakes,omitempty"`
}

type appResponse struct {
	ID              string `json:"id"`
	Slug            string `json:"slug"`
	Visibility      string `json:"visibility,omitempty"`
	Type            string `json:"type,omitempty"`
	Runtime         string `json:"runtime,omitempty"`
	Status          string `json:"status,omitempty"`
	URL             string `json:"url,omitempty"`
	ResourceProfile string `json:"resource_profile,omitempty"`
	RAMMB           *int   `json:"ram_mb,omitempty"`
	MaxConcurrency  *int   `json:"max_concurrency,omitempty"`
	IdleTimeoutS    *int   `json:"idle_timeout_s,omitempty"`
	HealthPath      string `json:"health_path,omitempty"`
	HealthPathWakes *bool  `json:"health_path_wakes,omitempty"`
}

type projectEnvironmentResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Slug      string `json:"slug"`
	Protected bool   `json:"protected"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type domainRequest struct {
	Domain string `json:"domain"`
	AppID  string `json:"app_id"`
}

type domainResponse struct {
	Domain           string   `json:"domain"`
	AppID            string   `json:"app_id"`
	ChallengeToken   string   `json:"challenge_token,omitempty"`
	TXTRecord        string   `json:"txt_record,omitempty"`
	Verified         bool     `json:"verified"`
	VerifiedAt       string   `json:"verified_at,omitempty"`
	Default          bool     `json:"default,omitempty"`
	CertNotAfter     string   `json:"cert_not_after,omitempty"`
	CertSANs         []string `json:"cert_sans,omitempty"`
	CertExpiresAt    string   `json:"cert_expires_at,omitempty"`
	CertLastError    string   `json:"cert_last_error,omitempty"`
	DNSLastCheckedAt string   `json:"dns_last_checked_at,omitempty"`
	CertStatus       string   `json:"cert_status,omitempty"`
}

type alertRuleRequest struct {
	Name            string  `json:"name"`
	Enabled         *bool   `json:"enabled,omitempty"`
	Metric          string  `json:"metric"`
	Comparison      string  `json:"comparison"`
	Threshold       float64 `json:"threshold"`
	WindowSpec      string  `json:"window_spec"`
	FailureSource   string  `json:"failure_source,omitempty"`
	Action          *string `json:"action,omitempty"`
	WebhookURL      string  `json:"webhook_url"`
	WebhookSecret   string  `json:"webhook_secret"`
	CooldownMinutes *int    `json:"cooldown_minutes,omitempty"`
}

type alertRulePatch struct {
	Name            *string  `json:"name,omitempty"`
	Enabled         *bool    `json:"enabled,omitempty"`
	Metric          *string  `json:"metric,omitempty"`
	Comparison      *string  `json:"comparison,omitempty"`
	Threshold       *float64 `json:"threshold,omitempty"`
	WindowSpec      *string  `json:"window_spec,omitempty"`
	Action          *string  `json:"action,omitempty"`
	WebhookURL      *string  `json:"webhook_url,omitempty"`
	WebhookSecret   *string  `json:"webhook_secret,omitempty"`
	CooldownMinutes *int     `json:"cooldown_minutes,omitempty"`
}

type alertRuleResponse struct {
	ID                        string  `json:"id"`
	AppID                     string  `json:"app_id"`
	Name                      string  `json:"name"`
	Enabled                   bool    `json:"enabled"`
	Metric                    string  `json:"metric"`
	Comparison                string  `json:"comparison"`
	Threshold                 float64 `json:"threshold"`
	WindowSpec                string  `json:"window_spec"`
	FailureSource             string  `json:"failure_source,omitempty"`
	Action                    string  `json:"action"`
	WebhookURL                string  `json:"webhook_url"`
	WebhookSecretSealedMasked string  `json:"webhook_secret_sealed_masked"`
	CooldownMinutes           int     `json:"cooldown_minutes"`
	State                     string  `json:"state"`
	LastFiredAt               string  `json:"last_fired_at,omitempty"`
	LastEvaluatedAt           string  `json:"last_evaluated_at,omitempty"`
	CreatedAt                 string  `json:"created_at"`
	UpdatedAt                 string  `json:"updated_at"`
}

type cronRequest struct {
	AppID         string `json:"app_id"`
	Schedule      string `json:"schedule"`
	Path          string `json:"path,omitempty"`
	Enabled       *bool  `json:"enabled,omitempty"`
	Timezone      string `json:"timezone,omitempty"`
	SkipIfRunning *bool  `json:"skip_if_running,omitempty"`
}

type cronPatch struct {
	Schedule      *string `json:"schedule,omitempty"`
	Path          *string `json:"path,omitempty"`
	Enabled       *bool   `json:"enabled,omitempty"`
	Timezone      *string `json:"timezone,omitempty"`
	SkipIfRunning *bool   `json:"skip_if_running,omitempty"`
}

type cronResponse struct {
	ID              string `json:"id"`
	AppID           string `json:"app_id"`
	Schedule        string `json:"schedule"`
	Path            string `json:"path"`
	Enabled         bool   `json:"enabled"`
	SuspendedReason string `json:"suspended_reason,omitempty"`
	Timezone        string `json:"timezone"`
	SkipIfRunning   bool   `json:"skip_if_running"`
	CreatedAt       string `json:"created_at"`
	LastFiredAt     string `json:"last_fired_at,omitempty"`
}

type secretRequest struct {
	Value string `json:"value"`
}

type secretMetadata struct {
	Key       string `json:"key"`
	Scope     string `json:"scope"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	Kid       string `json:"kid,omitempty"`
	ValueHash string `json:"value_hash,omitempty"`
}

type secretListResponse struct {
	Secrets []secretMetadata `json:"secrets"`
	Quota   int              `json:"quota_max"`
	Count   int              `json:"count"`
}

type envRequest struct {
	Value string `json:"value"`
}

type envMetadata struct {
	Key       string `json:"key"`
	Scope     string `json:"scope,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type envListResponse struct {
	Env   []envMetadata `json:"env"`
	Quota int           `json:"quota_max"`
	Count int           `json:"count"`
}

type deploymentRequest struct {
	Repo        string `json:"repo"`
	Ref         string `json:"ref"`
	Environment string `json:"environment,omitempty"`
	NoTriggers  bool   `json:"no_triggers,omitempty"`
}

type deploymentResponse struct {
	StageState  json.RawMessage `json:"stage_state,omitempty"`
	ID          string          `json:"id"`
	AppID       string          `json:"app_id"`
	BuildID     string          `json:"build_id,omitempty"`
	ImageDigest string          `json:"image_digest,omitempty"`
	Kind        string          `json:"kind"`
	Status      string          `json:"status"`
	Error       string          `json:"error,omitempty"`
	ErrorCode   string          `json:"error_code,omitempty"`
	ErrorHint   string          `json:"error_hint,omitempty"`
	ErrorWhy    string          `json:"error_why,omitempty"`
	ErrorFix    string          `json:"error_fix,omitempty"`
	CreatedAt   string          `json:"created_at"`
	SourceURL   string          `json:"source_url,omitempty"`
	CommitSHA   string          `json:"commit_sha,omitempty"`
	Scope       string          `json:"scope,omitempty"`
}

type deploymentURLResponse struct {
	DeploymentID string `json:"deployment_id"`
	Host         string `json:"host,omitempty"`
	URL          string `json:"url,omitempty"`
	Alive        bool   `json:"alive"`
}

func newClient(rawBaseURL, token string) (*client, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(rawBaseURL), "/"))
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("base URL must use http or https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("base URL must include a host")
	}
	return &client{
		baseURL: strings.TrimRight(parsed.String(), "/"),
		token:   token,
		http:    &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (c *client) request(ctx context.Context, method, path string, body any, out any, idempotencyKey bool) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode Gregale request: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return fmt.Errorf("build Gregale request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if idempotencyKey {
		key, keyErr := randomKey()
		if keyErr != nil {
			return fmt.Errorf("create idempotency key: %w", keyErr)
		}
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Gregale API request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		var problem problemResponse
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		_ = json.Unmarshal(body, &problem)
		return &apiError{status: res.StatusCode, code: problem.Code, title: problem.Title, detail: problem.Detail}
	}
	if out == nil || res.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("decode Gregale response: %w", err)
	}
	return nil
}

func randomKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func escapePath(value string) string {
	return url.PathEscape(value)
}

func isNotFound(err error) bool {
	var apiErr *apiError
	return errors.As(err, &apiErr) && apiErr.status == http.StatusNotFound
}

func (c *client) createApp(ctx context.Context, req appRequest) (appResponse, error) {
	var out appResponse
	err := c.request(ctx, http.MethodPost, "/v1/apps", req, &out, true)
	return out, err
}

func (c *client) getApp(ctx context.Context, slug string) (appResponse, error) {
	var out appResponse
	err := c.request(ctx, http.MethodGet, "/v1/apps/"+escapePath(slug), nil, &out, false)
	return out, err
}

func (c *client) updateApp(ctx context.Context, slug string, patch appPatch) (appResponse, error) {
	var out appResponse
	err := c.request(ctx, http.MethodPatch, "/v1/apps/"+escapePath(slug), patch, &out, false)
	return out, err
}

func (c *client) deleteApp(ctx context.Context, slug string) error {
	return c.request(ctx, http.MethodDelete, "/v1/apps/"+escapePath(slug), nil, nil, false)
}

func (c *client) getProjectEnvironment(ctx context.Context, project, environment string) (projectEnvironmentResponse, error) {
	var out projectEnvironmentResponse
	path := "/v1/projects/" + escapePath(project) + "/environments/" + escapePath(environment)
	err := c.request(ctx, http.MethodGet, path, nil, &out, false)
	return out, err
}

func (c *client) createDomain(ctx context.Context, req domainRequest) (domainResponse, error) {
	var out domainResponse
	err := c.request(ctx, http.MethodPost, "/v1/domains", req, &out, true)
	return out, err
}

func (c *client) getDomain(ctx context.Context, domain string) (domainResponse, error) {
	var out domainResponse
	err := c.request(ctx, http.MethodGet, "/v1/domains/"+escapePath(domain), nil, &out, false)
	return out, err
}

func (c *client) deleteDomain(ctx context.Context, domain string) error {
	return c.request(ctx, http.MethodDelete, "/v1/domains/"+escapePath(domain), nil, nil, false)
}

func (c *client) createAlertRule(ctx context.Context, appSlug string, req alertRuleRequest) (alertRuleResponse, error) {
	var out alertRuleResponse
	path := "/v1/apps/" + escapePath(appSlug) + "/alerts"
	err := c.request(ctx, http.MethodPost, path, req, &out, true)
	return out, err
}

func (c *client) getAlertRule(ctx context.Context, appSlug, alertID string) (alertRuleResponse, error) {
	var out alertRuleResponse
	path := "/v1/apps/" + escapePath(appSlug) + "/alerts/" + escapePath(alertID)
	err := c.request(ctx, http.MethodGet, path, nil, &out, false)
	return out, err
}

func (c *client) updateAlertRule(ctx context.Context, appSlug, alertID string, patch alertRulePatch) (alertRuleResponse, error) {
	var out alertRuleResponse
	path := "/v1/apps/" + escapePath(appSlug) + "/alerts/" + escapePath(alertID)
	err := c.request(ctx, http.MethodPatch, path, patch, &out, false)
	return out, err
}

func (c *client) deleteAlertRule(ctx context.Context, appSlug, alertID string) error {
	path := "/v1/apps/" + escapePath(appSlug) + "/alerts/" + escapePath(alertID)
	return c.request(ctx, http.MethodDelete, path, nil, nil, false)
}

func (c *client) createCron(ctx context.Context, req cronRequest) (cronResponse, error) {
	var out cronResponse
	err := c.request(ctx, http.MethodPost, "/v1/crons", req, &out, true)
	return out, err
}

func (c *client) getCron(ctx context.Context, cronID string) (cronResponse, error) {
	var out cronResponse
	err := c.request(ctx, http.MethodGet, "/v1/crons/"+escapePath(cronID), nil, &out, false)
	return out, err
}

func (c *client) updateCron(ctx context.Context, cronID string, patch cronPatch) (cronResponse, error) {
	var out cronResponse
	err := c.request(ctx, http.MethodPatch, "/v1/crons/"+escapePath(cronID), patch, &out, false)
	return out, err
}

func (c *client) deleteCron(ctx context.Context, cronID string) error {
	return c.request(ctx, http.MethodDelete, "/v1/crons/"+escapePath(cronID), nil, nil, false)
}

func (c *client) setSecret(ctx context.Context, appSlug, scope, key, value string) error {
	path := "/v1/apps/" + escapePath(appSlug) + "/secrets/" + escapePath(key)
	path = withSecretScope(path, scope)
	return c.request(ctx, http.MethodPut, path, secretRequest{Value: value}, nil, true)
}

func (c *client) getSecret(ctx context.Context, appSlug, scope, key string) (secretMetadata, bool, error) {
	path := withSecretScope("/v1/apps/"+escapePath(appSlug)+"/secrets", scope)
	var out secretListResponse
	if err := c.request(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return secretMetadata{}, false, err
	}
	for _, secret := range out.Secrets {
		if secret.Key != key {
			continue
		}
		if secret.Scope == "" {
			secret.Scope = defaultSecretScope
		}
		wantedScope := scope
		if wantedScope == "" {
			wantedScope = defaultSecretScope
		}
		if secret.Scope == wantedScope {
			return secret, true, nil
		}
	}
	return secretMetadata{}, false, nil
}

func (c *client) deleteSecret(ctx context.Context, appSlug, scope, key string) error {
	path := withSecretScope("/v1/apps/"+escapePath(appSlug)+"/secrets/"+escapePath(key), scope)
	return c.request(ctx, http.MethodDelete, path, nil, nil, false)
}

func withSecretScope(path, scope string) string {
	if scope == "" || scope == defaultSecretScope {
		return path
	}
	return path + "?scope=" + url.QueryEscape(scope)
}

func (c *client) setEnv(ctx context.Context, appSlug, scope, key, value string) error {
	path := "/v1/apps/" + escapePath(appSlug) + "/env/" + escapePath(key)
	path = withEnvScope(path, scope)
	return c.request(ctx, http.MethodPut, path, envRequest{Value: value}, nil, true)
}

func (c *client) getEnv(ctx context.Context, appSlug, scope, key string) (envMetadata, bool, error) {
	path := withEnvScope("/v1/apps/"+escapePath(appSlug)+"/env", scope)
	var out envListResponse
	if err := c.request(ctx, http.MethodGet, path, nil, &out, false); err != nil {
		return envMetadata{}, false, err
	}
	wantedScope := scope
	if wantedScope == "" {
		wantedScope = defaultEnvScope
	}
	for _, env := range out.Env {
		if env.Key != key {
			continue
		}
		if env.Scope == "" {
			env.Scope = wantedScope
		}
		if env.Scope == wantedScope {
			return env, true, nil
		}
	}
	return envMetadata{}, false, nil
}

func (c *client) deleteEnv(ctx context.Context, appSlug, scope, key string) error {
	path := "/v1/apps/" + escapePath(appSlug) + "/env/" + escapePath(key)
	path = withEnvScope(path, scope)
	return c.request(ctx, http.MethodDelete, path, nil, nil, false)
}

func withEnvScope(path, scope string) string {
	if scope == "" || scope == defaultEnvScope {
		return path
	}
	return path + "?scope=" + url.QueryEscape(scope)
}

func (c *client) createSourceRefDeployment(ctx context.Context, appSlug string, req deploymentRequest) (deploymentResponse, error) {
	var out deploymentResponse
	path := "/v1/apps/" + escapePath(appSlug) + "/deployments/source-ref"
	err := c.request(ctx, http.MethodPost, path, req, &out, true)
	return out, err
}

func (c *client) getDeployment(ctx context.Context, deploymentID string) (deploymentResponse, error) {
	var out deploymentResponse
	path := "/v1/deployments/" + escapePath(deploymentID)
	err := c.request(ctx, http.MethodGet, path, nil, &out, false)
	return out, err
}

func (c *client) getLatestAppDeployment(ctx context.Context, appSlug string) (deploymentResponse, error) {
	var out deploymentResponse
	path := "/v1/apps/" + escapePath(appSlug) + "/deployments/latest"
	err := c.request(ctx, http.MethodGet, path, nil, &out, false)
	return out, err
}

func (c *client) getDeploymentURL(ctx context.Context, deploymentID string) (deploymentURLResponse, error) {
	var out deploymentURLResponse
	path := "/v1/deployments/" + escapePath(deploymentID) + "/url"
	err := c.request(ctx, http.MethodGet, path, nil, &out, false)
	return out, err
}

func (c *client) cancelDeployment(ctx context.Context, appSlug, deploymentID, reason string) (deploymentResponse, error) {
	var out deploymentResponse
	path := "/v1/apps/" + escapePath(appSlug) + "/deployments/" + escapePath(deploymentID) + "/cancel"
	body := struct {
		Reason string `json:"reason,omitempty"`
	}{Reason: reason}
	err := c.request(ctx, http.MethodPost, path, body, &out, false)
	return out, err
}
