package apihostingreceipt

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
	SmokeErrorResponseUnproven      = "smoke_response_unproven"
)

const (
	PlatformSmokeHeader           = "X-Gregale-Platform-Smoke"
	PlatformSmokeTokenHeader      = "X-Faas-Platform-Smoke-Token"
	PlatformSmokeDeploymentHeader = "X-Faas-Platform-Smoke-Deployment"
	ServedDeploymentHeader        = "X-Faas-Deployment-Id"
	// ServedResponseHeader is gateway-authored only after an upstream response.
	ServedResponseHeader = "X-Faas-Platform-Smoke-Response"
)

// Verifier performs the post-readiness public HTTP check. BaseURL is the
// gateway's public origin; AppsDomain is used to construct the tenant Host
// header when the origin is shared by many apps.
type Verifier struct {
	Client     *http.Client
	BaseURL    string
	AppsDomain string
	Timeout    time.Duration
	// RequestTimeout bounds one public gateway attempt independently of the
	// whole verification budget. A stalled node must not consume the entire
	// budget before another attempt can reach a healthy candidate.
	RequestTimeout time.Duration
	RetryInterval  time.Duration
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
	return v.verifyWithContract(ctx, slug, path, deploymentID, VerificationHTTPHealth)
}

// VerifyDeploymentRoute checks candidate reachability for TCP-ready images
// that declare no HTTP health endpoint. It never establishes endpoint health.
func (v Verifier) VerifyDeploymentRoute(ctx context.Context, slug, deploymentID string) (SmokeResult, error) {
	return v.verifyWithContract(ctx, slug, "/", deploymentID, VerificationRouteConnectivity)
}

func (v Verifier) verifyWithContract(ctx context.Context, slug, path, deploymentID, verification string) (SmokeResult, error) {
	result, err := v.verifyDeployment(ctx, slug, path, deploymentID, verification)
	result.Verification = verification
	if deploymentID != "" {
		result.Authentication = AuthenticationPlatformChallenge
	}
	return result, err
}

func (v Verifier) verifyDeployment(ctx context.Context, slug, path, deploymentID, verification string) (SmokeResult, error) {
	path = normalizePath(path)
	result := SmokeResult{Status: SmokeSkipped, Path: path}
	if verification == VerificationRouteConnectivity && strings.TrimSpace(deploymentID) == "" {
		return failedSmoke(path, SmokeErrorDeploymentMismatch, fmt.Errorf("route verification requires a candidate deployment")), nil
	}
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
	requestTimeout := v.Timeout
	if v.RequestTimeout > 0 {
		requestTimeout = v.RequestTimeout
	}
	if requestTimeout > 0 || verification == VerificationRouteConnectivity {
		copy := *client
		if requestTimeout > 0 {
			copy.Timeout = requestTimeout
		}
		if verification == VerificationRouteConnectivity {
			// A root redirect itself proves reachability; following it can
			// leave the candidate and disclose the short-lived challenge.
			copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		}
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
		result = verifyOnce(verifyCtx, client, v.BaseURL, v.AppsDomain, slug, path, deploymentID, token, verification)
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

func verifyOnce(ctx context.Context, client *http.Client, baseURL, appsDomain, slug, path, deploymentID, token, verification string) SmokeResult {
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
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
	result.StatusCode = resp.StatusCode
	result.VerifiedAt = time.Now().UTC()
	if id := resp.Header.Get("X-Faas-Request-ID"); id != "" {
		result.RequestID = id
	} else if id := resp.Header.Get("X-Request-ID"); id != "" {
		result.RequestID = id
	}
	result.DeploymentID = resp.Header.Get(ServedDeploymentHeader)
	if verification == VerificationRouteConnectivity && readErr != nil {
		result.Status = SmokeFailed
		result.ErrorCode = "smoke_request_failed"
		result.Error = "candidate response could not be read"
		return result
	}
	// Report an error response as what it is. Checking the deployment header
	// first turned every gateway refusal (a 429 or 503 carries no deployment
	// header) into "reached deployment \"\"", which hid the real status.
	acceptable := resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
	if verification == VerificationRouteConnectivity {
		acceptable = (resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusBadRequest) ||
			resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound
	}
	if !acceptable {
		result.Status = SmokeFailed
		result.ErrorCode = "smoke_http_status"
		result.Error = fmt.Sprintf("health probe returned HTTP %d", resp.StatusCode)
		if code := problemCode(resp.Header.Get("Content-Type"), body); code != "" {
			result.Error += " (" + code + ")"
		}
		return result
	}
	if deploymentID != "" && result.DeploymentID != deploymentID {
		result.Status = SmokeFailed
		result.ErrorCode = SmokeErrorDeploymentMismatch
		result.Error = fmt.Sprintf("health probe reached deployment %q, expected %q", result.DeploymentID, deploymentID)
		return result
	}
	if verification == VerificationRouteConnectivity && !hmac.Equal([]byte(resp.Header.Get(ServedResponseHeader)), []byte(CandidateResponseProof(deploymentID, token))) {
		result.Status = SmokeFailed
		result.ErrorCode = SmokeErrorResponseUnproven
		result.Error = "gateway did not prove a response from the candidate application"
		return result
	}
	result.Status = SmokeVerified
	return result
}

// CandidateResponseProof binds upstream evidence to this authorized challenge
// and candidate. The token is never forwarded to the application or persisted.
func CandidateResponseProof(deploymentID, token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = mac.Write([]byte("gregale-candidate-response:" + deploymentID))
	return hex.EncodeToString(mac.Sum(nil))
}

// problemCode extracts the stable RFC 7807 "code" from a JSON error body.
// Only a short identifier is returned, never body content, so a receipt
// cannot persist anything the app or gateway wrote.
func problemCode(contentType string, body []byte) string {
	if !strings.Contains(strings.ToLower(contentType), "json") || len(body) == 0 {
		return ""
	}
	var problem struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &problem); err != nil || problem.Code == "" || len(problem.Code) > 64 {
		return ""
	}
	for _, r := range problem.Code {
		allowed := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.'
		if !allowed {
			return ""
		}
	}
	return problem.Code
}

func retryableSmoke(result SmokeResult) bool {
	if result.ErrorCode == "smoke_request_failed" {
		return true
	}
	if result.ErrorCode == SmokeErrorDeploymentMismatch || result.ErrorCode == SmokeErrorResponseUnproven {
		return true
	}
	switch result.StatusCode {
	case http.StatusNotFound, http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
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
