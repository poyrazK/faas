package apihostingreceipt

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	SmokeErrorNotConfigured         = "smoke_not_configured"
	SmokeErrorVerifierNotConfigured = "smoke_verifier_not_configured"
	SmokeErrorAuthorizationFailed   = "smoke_authorization_failed"
	SmokeErrorDeploymentMismatch    = "smoke_deployment_mismatch"
)

const (
	PlatformSmokeHeader           = "X-Gregale-Platform-Smoke"
	PlatformSmokeTokenHeader      = "X-Faas-Platform-Smoke-Token"
	PlatformSmokeDeploymentHeader = "X-Faas-Platform-Smoke-Deployment"
	ServedDeploymentHeader        = "X-Faas-Deployment-Id"
)

// Verifier performs the post-readiness public HTTP check. BaseURL is the
// gateway's public origin; AppsDomain is used to construct the tenant Host
// header when the origin is shared by many apps.
type Verifier struct {
	Client        *http.Client
	BaseURL       string
	AppsDomain    string
	Timeout       time.Duration
	RetryInterval time.Duration
	// Required makes an unset BaseURL a failed verification rather than a
	// compatibility skip. Public-beta compute nodes set this so a missing
	// verifier cannot promote a deployment with an unverified public route.
	Required bool
	// Authorize publishes a short-lived challenge to the gateways before the
	// public request is sent. The token is never persisted in the receipt.
	Authorize func(context.Context, string, string, time.Time) error
}

func (v Verifier) Verify(ctx context.Context, slug, path string) (SmokeResult, error) {
	return v.VerifyDeployment(ctx, slug, path, "")
}

// VerifyDeployment proves that the request reached the expected candidate.
// Production callers provide deploymentID and an Authorize callback; the
// legacy Verify wrapper remains useful for hermetic/offline callers.
func (v Verifier) VerifyDeployment(ctx context.Context, slug, path, deploymentID string) (SmokeResult, error) {
	path = normalizePath(path)
	result := SmokeResult{Status: SmokeSkipped, Path: path}
	if strings.TrimSpace(v.BaseURL) == "" {
		if v.Required {
			result.Status = SmokeFailed
			result.ErrorCode = SmokeErrorVerifierNotConfigured
			result.Error = "public hosting smoke verifier is required but not configured"
		} else {
			result.ErrorCode = SmokeErrorNotConfigured
		}
		return result, nil
	}

	var token string
	if deploymentID != "" {
		if v.Authorize == nil {
			return failedSmoke(path, SmokeErrorAuthorizationFailed, fmt.Errorf("platform smoke authorizer is not configured")), nil
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return failedSmoke(path, SmokeErrorAuthorizationFailed, err), nil
		}
		token = base64.RawURLEncoding.EncodeToString(raw)
		expiresAt := time.Now().UTC().Add(max(v.Timeout, 10*time.Second) + 5*time.Second)
		if err := v.Authorize(ctx, deploymentID, token, expiresAt); err != nil {
			return failedSmoke(path, SmokeErrorAuthorizationFailed, err), nil
		}
	}

	client := v.Client
	if client == nil {
		client = &http.Client{}
	}
	if v.Timeout > 0 {
		copy := *client
		copy.Timeout = v.Timeout
		client = &copy
	}
	verifyCtx := ctx
	cancel := func() {}
	if v.Timeout > 0 {
		verifyCtx, cancel = context.WithTimeout(ctx, v.Timeout)
	}
	defer cancel()
	started := time.Now()
	for {
		result = verifyOnce(verifyCtx, client, v.BaseURL, v.AppsDomain, slug, path, deploymentID, token)
		result.LatencyMS = time.Since(started).Milliseconds()
		if result.Status == SmokeVerified || v.Timeout <= 0 || !retryableSmoke(result) {
			return result, nil
		}
		interval := v.RetryInterval
		if interval <= 0 {
			interval = 100 * time.Millisecond
		}
		timer := time.NewTimer(interval)
		select {
		case <-verifyCtx.Done():
			timer.Stop()
			return result, nil
		case <-timer.C:
		}
	}
}

func verifyOnce(ctx context.Context, client *http.Client, baseURL, appsDomain, slug, path, deploymentID, token string) SmokeResult {
	result := SmokeResult{Status: SmokeSkipped, Path: path}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
	if err != nil {
		return failedSmoke(path, "smoke_request_failed", err)
	}
	req.Header.Set(PlatformSmokeHeader, "1")
	if deploymentID != "" {
		req.Header.Set(PlatformSmokeDeploymentHeader, deploymentID)
		req.Header.Set(PlatformSmokeTokenHeader, token)
	}
	if host := smokeHost(slug, appsDomain); host != "" {
		req.Host = host
	}
	resp, err := client.Do(req)
	if err != nil {
		return failedSmoke(path, "smoke_request_failed", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	result.StatusCode = resp.StatusCode
	result.VerifiedAt = time.Now().UTC()
	if id := resp.Header.Get("X-Faas-Request-ID"); id != "" {
		result.RequestID = id
	} else if id := resp.Header.Get("X-Request-ID"); id != "" {
		result.RequestID = id
	}
	result.DeploymentID = resp.Header.Get(ServedDeploymentHeader)
	if deploymentID != "" && result.DeploymentID != deploymentID {
		result.Status = SmokeFailed
		result.ErrorCode = SmokeErrorDeploymentMismatch
		result.Error = fmt.Sprintf("health probe reached deployment %q, expected %q", result.DeploymentID, deploymentID)
		return result
	}
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		result.Status = SmokeVerified
		return result
	}
	result.Status = SmokeFailed
	result.ErrorCode = "smoke_http_status"
	result.Error = fmt.Sprintf("health probe returned HTTP %d", resp.StatusCode)
	return result
}

func retryableSmoke(result SmokeResult) bool {
	if result.ErrorCode == "smoke_request_failed" {
		return true
	}
	if result.ErrorCode == SmokeErrorDeploymentMismatch {
		return true
	}
	switch result.StatusCode {
	case http.StatusNotFound, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/healthz"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

func smokeHost(slug, domain string) string {
	slug = strings.TrimSpace(slug)
	domain = strings.Trim(strings.TrimSpace(domain), ".")
	if slug == "" || domain == "" {
		return ""
	}
	return slug + "." + domain
}

func failedSmoke(path, code string, err error) SmokeResult {
	return SmokeResult{Status: SmokeFailed, Path: path, ErrorCode: code, Error: safeError(err)}
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
