package apihostingreceipt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	SmokeErrorNotConfigured         = "smoke_not_configured"
	SmokeErrorVerifierNotConfigured = "smoke_verifier_not_configured"
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
}

func (v Verifier) Verify(ctx context.Context, slug, path string) (SmokeResult, error) {
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
		result = verifyOnce(verifyCtx, client, v.BaseURL, v.AppsDomain, slug, path)
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

func verifyOnce(ctx context.Context, client *http.Client, baseURL, appsDomain, slug, path string) SmokeResult {
	result := SmokeResult{Status: SmokeSkipped, Path: path}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
	if err != nil {
		return failedSmoke(path, "smoke_request_failed", err)
	}
	req.Header.Set("X-Gregale-Platform-Smoke", "1")
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
	if id := resp.Header.Get("X-Request-ID"); id != "" {
		result.RequestID = id
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
