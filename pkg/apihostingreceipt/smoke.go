package apihostingreceipt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Verifier performs the post-readiness public HTTP check. BaseURL is the
// gateway's public origin; AppsDomain is used to construct the tenant Host
// header when the origin is shared by many apps.
type Verifier struct {
	Client     *http.Client
	BaseURL    string
	AppsDomain string
	Timeout    time.Duration
}

func (v Verifier) Verify(ctx context.Context, slug, path string) (SmokeResult, error) {
	path = normalizePath(path)
	result := SmokeResult{Status: SmokeSkipped, Path: path}
	if strings.TrimSpace(v.BaseURL) == "" {
		result.ErrorCode = "smoke_not_configured"
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(v.BaseURL, "/")+path, nil)
	if err != nil {
		return failedSmoke(path, "smoke_request_failed", err), nil
	}
	req.Header.Set("X-Gregale-Platform-Smoke", "1")
	if host := smokeHost(slug, v.AppsDomain); host != "" {
		req.Host = host
	}
	started := time.Now()
	resp, err := client.Do(req)
	result.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		result.Status = SmokeFailed
		result.ErrorCode = "smoke_request_failed"
		result.Error = safeError(err)
		return result, nil
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
		return result, nil
	}
	result.Status = SmokeFailed
	result.ErrorCode = "smoke_http_status"
	result.Error = fmt.Sprintf("health probe returned HTTP %d", resp.StatusCode)
	return result, nil
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
