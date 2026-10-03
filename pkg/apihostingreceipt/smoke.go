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
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/onebox-faas/faas/pkg/safetext"
)

const (
	SmokeErrorNotConfigured            = "smoke_not_configured"
	SmokeErrorVerifierNotConfigured    = "smoke_verifier_not_configured"
	SmokeErrorAuthorizationFailed      = "smoke_authorization_failed"
	SmokeErrorAuthorizationUnavailable = "smoke_authorization_unavailable"
	SmokeErrorVerificationUnavailable  = "smoke_verification_unavailable"
	SmokeErrorGatewayUnavailable       = "smoke_gateway_unavailable"
	SmokeErrorTransportUnavailable     = "smoke_transport_unavailable"
	SmokeErrorDeploymentMismatch       = "smoke_deployment_mismatch"
	SmokeErrorResponseUnproven         = "smoke_response_unproven"
	SmokeErrorContractUnavailable      = "smoke_contract_unavailable"
	SmokeErrorContractInvalid          = "smoke_contract_invalid"
	SmokeErrorContractRouteFailed      = "smoke_contract_route_failed"
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

// VerifyDeploymentAPIRoute checks one explicitly selected, read-only API
// operation against the exact candidate. Authentication challenges, redirects,
// and other non-error responses are accepted; 404 and server failures are
// application verdicts and do not prove the selected operation works.
func (v Verifier) VerifyDeploymentAPIRoute(ctx context.Context, slug, path, deploymentID string) (SmokeResult, error) {
	if validationErr := ValidateAPIRouteProbe(APIRouteProbe{Method: "GET", Path: path}); validationErr != nil {
		invalid := fmt.Errorf("invalid API route check: %w", validationErr)
		return failedSmoke(path, SmokeErrorContractInvalid, invalid), invalid
	}
	return v.verifyWithContract(ctx, slug, path, deploymentID, VerificationAPIRouteContract)
}

// VerifyDeploymentAPIRoutes checks a bounded set of contract-selected GET
// operations. The whole set shares the verifier timeout, and each unavailable
// route returns typed evidence so imaged can retry the same candidate through
// its durable hosting-verification window.
func (v Verifier) VerifyDeploymentAPIRoutes(ctx context.Context, slug, deploymentID string, routes []APIRouteProbe) ([]RouteCheckResult, error) {
	if len(routes) == 0 || len(routes) > MaxAPIRouteChecks {
		return nil, fmt.Errorf("API route check count must be between 1 and %d", MaxAPIRouteChecks)
	}
	ordered := append([]APIRouteProbe(nil), routes...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	for _, route := range ordered {
		if err := ValidateAPIRouteProbe(route); err != nil {
			return nil, fmt.Errorf("invalid API route check")
		}
	}

	checkCtx := ctx
	cancel := func() {}
	if v.Timeout > 0 {
		checkCtx, cancel = context.WithTimeout(ctx, v.Timeout)
	}
	defer cancel()

	checks := make([]RouteCheckResult, 0, len(ordered))
	for i, route := range ordered {
		probeVerifier := v
		if deadline, ok := checkCtx.Deadline(); ok {
			probeVerifier.Timeout = time.Until(deadline)
			if probeVerifier.Timeout <= 0 {
				probeVerifier.Timeout = time.Nanosecond
			}
		}
		result, err := probeVerifier.VerifyDeploymentAPIRoute(checkCtx, slug, route.Path, deploymentID)
		checks = append(checks, RouteCheckResult{
			Method: "GET", Path: route.Path, Status: result.Status, StatusCode: result.StatusCode,
			LatencyMS: result.LatencyMS, VerifiedAt: result.VerifiedAt, RequestID: result.RequestID,
			ErrorCode: result.ErrorCode, Error: result.Error,
		})
		if err != nil || result.Status != SmokeVerified {
			for _, remaining := range ordered[i+1:] {
				checks = append(checks, RouteCheckResult{Method: "GET", Path: remaining.Path, Status: SmokeSkipped, ErrorCode: "smoke_route_check_not_run"})
			}
			return checks, err
		}
	}
	return checks, nil
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
	origin, err := url.Parse(v.BaseURL)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" {
		return failedSmoke(path, SmokeErrorVerifierNotConfigured, fmt.Errorf("public hosting smoke origin must be an absolute HTTP or HTTPS URL")), nil
	}
	verifyCtx := ctx
	cancel := func() {}
	if v.Timeout > 0 {
		verifyCtx, cancel = context.WithTimeout(ctx, v.Timeout)
	}
	defer cancel()

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
		authorizeCtx, authorizeCancel := challengePublicationContext(verifyCtx, v.RequestTimeout)
		err := v.Authorize(authorizeCtx, deploymentID, token, expiresAt)
		if err == nil {
			err = authorizeCtx.Err()
		}
		authorizeCancel()
		if err != nil {
			unavailable := &ChallengePublicationError{Cause: err}
			return SmokeResult{Status: SmokeSkipped, Path: path, ErrorCode: SmokeErrorAuthorizationUnavailable, Error: unavailable.Error()}, unavailable
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
	if requestTimeout > 0 || deploymentID != "" {
		copy := *client
		if requestTimeout > 0 {
			copy.Timeout = requestTimeout
		}
		if deploymentID != "" {
			// Candidate proofs apply to one response. Redirects cannot move
			// a platform challenge to a different route, host or deployment.
			copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		}
		client = &copy
	}
	return v.verifyAttempts(verifyCtx, client, slug, path, deploymentID, token, verification)
}

func (v Verifier) verifyAttempts(ctx context.Context, client *http.Client, slug, path, deploymentID, token, verification string) (SmokeResult, error) {
	var result SmokeResult
	var attemptErr error
	started := time.Now()
	recoveryDeadline, _ := ctx.Value(verificationRecoveryDeadlineKey{}).(time.Time)
	proven := false
	for {
		if result.Status != "" && verificationContextExpired(ctx) {
			return result, attemptErr
		}
		attemptCtx := ctx
		attemptCancel := func() {}
		if !proven && !recoveryDeadline.IsZero() {
			attemptCtx, attemptCancel = context.WithDeadline(ctx, recoveryDeadline)
		}
		result, attemptErr = verifyOnce(attemptCtx, client, v.BaseURL, v.AppsDomain, slug, path, deploymentID, token, verification)
		attemptCancel()
		proven = attemptErr == nil
		result.LatencyMS = time.Since(started).Milliseconds()
		if result.Status == SmokeVerified || v.Timeout <= 0 || !retryableSmoke(result) {
			return result, attemptErr
		}
		if attemptErr != nil && !recoveryDeadline.IsZero() && !time.Now().Before(recoveryDeadline) {
			return result, attemptErr
		}
		interval := v.RetryInterval
		if interval <= 0 {
			interval = 100 * time.Millisecond
		}
		if attemptErr != nil && !recoveryDeadline.IsZero() {
			interval = min(interval, time.Until(recoveryDeadline))
		}
		if deadline, ok := ctx.Deadline(); ok {
			interval = min(interval, time.Until(deadline))
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, attemptErr
		case <-timer.C:
			if verificationContextExpired(ctx) || (attemptErr != nil && !recoveryDeadline.IsZero() && !time.Now().Before(recoveryDeadline)) {
				return result, attemptErr
			}
		}
	}
}

func verificationContextExpired(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	// The deadline can pass before the context's cancellation timer runs.
	// Check the timestamp too so a retry cannot replace the last verdict
	// with a request that starts after the probe budget has expired.
	deadline, ok := ctx.Deadline()
	return ok && !time.Now().Before(deadline)
}

func verifyOnce(ctx context.Context, client *http.Client, baseURL, appsDomain, slug, path, deploymentID, token, verification string) (SmokeResult, error) {
	result := SmokeResult{Status: SmokeSkipped, Path: path, Verification: verification}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+path, nil)
	if err != nil {
		return failedSmoke(path, SmokeErrorVerifierNotConfigured, fmt.Errorf("public hosting smoke request is invalid")), nil
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
		if deploymentID != "" {
			return unavailableSmoke(result, SmokeErrorTransportUnavailable, err)
		}
		return failedSmoke(path, "smoke_request_failed", err), nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
	result.StatusCode = resp.StatusCode
	result.VerifiedAt = time.Now().UTC()
	if id := safeSmokeRequestID(resp.Header.Get("X-Faas-Request-ID")); id != "" {
		result.RequestID = id
	} else if id := safeSmokeRequestID(resp.Header.Get("X-Request-ID")); id != "" {
		result.RequestID = id
	}
	result.DeploymentID = resp.Header.Get(ServedDeploymentHeader)
	if deploymentID != "" {
		if result.DeploymentID != "" && result.DeploymentID != deploymentID {
			return unavailableSmoke(result, SmokeErrorDeploymentMismatch, nil)
		}
		proof := resp.Header.Get(ServedResponseHeader)
		if result.DeploymentID != deploymentID || !hmac.Equal([]byte(proof), []byte(CandidateResponseProof(deploymentID, token))) {
			code := SmokeErrorResponseUnproven
			if proof == "" && resp.StatusCode >= http.StatusBadRequest {
				code = SmokeErrorGatewayUnavailable
			}
			unavailable, err := unavailableSmoke(result, code, nil)
			if problem := problemCode(resp.Header.Get("Content-Type"), body); code == SmokeErrorGatewayUnavailable && problem != "" {
				// A bounded problem identifier is diagnostic only. An app can
				// imitate this body; only the proof establishes its origin.
				unavailable.Error += " (" + problem + ")"
			}
			return unavailable, err
		}
	}
	// Only authenticated candidate responses can establish an app verdict.
	// An identical gateway status without proof remains unavailable evidence.
	acceptable := resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
	switch verification {
	case VerificationRouteConnectivity:
		// 415 is how a gRPC server answers a non-gRPC request (the gRPC
		// HTTP/2 spec); like 401/403/404 it proves the candidate answered.
		acceptable = (resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusBadRequest) ||
			resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound ||
			resp.StatusCode == http.StatusUnsupportedMediaType
	case VerificationAPIRouteContract:
		acceptable = (resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusBadRequest) ||
			resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden
	}
	if !acceptable {
		result.Status = SmokeFailed
		result.ErrorCode = "smoke_http_status"
		if verification == VerificationAPIRouteContract {
			result.Error = fmt.Sprintf("API route check returned HTTP %d", resp.StatusCode)
		} else {
			result.Error = fmt.Sprintf("health probe returned HTTP %d", resp.StatusCode)
		}
		if code := problemCode(resp.Header.Get("Content-Type"), body); code != "" {
			result.Error += " (" + code + ")"
		}
		return result, nil
	}
	if deploymentID != "" && readErr != nil {
		return unavailableSmoke(result, SmokeErrorTransportUnavailable, readErr)
	}
	result.Status = SmokeVerified
	return result, nil
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

func safeSmokeRequestID(value string) string {
	value = safetext.Truncate(strings.TrimSpace(value), 128)
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return ""
	}
	return value
}

func retryableSmoke(result SmokeResult) bool {
	if result.Verification == VerificationAPIRouteContract {
		return IsVerificationRecoveryCode(result.ErrorCode)
	}
	if IsVerificationRecoveryCode(result.ErrorCode) {
		return true
	}
	if result.ErrorCode == "smoke_request_failed" {
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
