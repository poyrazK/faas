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
