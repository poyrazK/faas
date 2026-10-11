package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/safetext"
)

// Request bodies and user-entered paths stay in memory. They may contain
// customer data and must not become durable onboarding metadata.
type startRequestResult struct {
	path, appURL, body, invocationID, status, servedDeployment string
	httpStatus                                                 int
	elapsed                                                    time.Duration
}

func validStartRequestPath(path string) bool {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\r\n") {
		return false
	}
	parsed, err := url.Parse(path)
	return err == nil && parsed.Host == "" && parsed.Scheme == "" && parsed.Fragment == ""
}

func safeStartHealthPath(path string) bool {
	if !validStartRequestPath(path) {
		return false
	}
	parsed, _ := url.Parse(path)
	return parsed.RawQuery == ""
}

func (r *startRunner) captureDeployment(dep api.DeploymentResponse) {
	r.session.Revision = dep.Revision
	receipt, err := apihostingreceipt.Decode(dep.APIHostingReceipt)
	if err != nil || receipt.DeploymentID != dep.ID || receipt.AppID != dep.AppID {
		return
	}
	path := receipt.Profile.HealthPath
	if receipt.Smoke.Status == apihostingreceipt.SmokeVerified && receipt.Smoke.Verification != apihostingreceipt.VerificationRouteConnectivity && receipt.Smoke.Path != "" {
		path = receipt.Smoke.Path
	}
	if safeStartHealthPath(path) {
		r.session.HealthPath = path
	}
}

func (r *startRunner) suggestedRequestPath() string {
	// The starter root returns the editable greeting. Existing projects get
	// the detected/verified health endpoint rather than a likely root 404.
	if startTemplateAllowed(r.session.Template) {
		return "/"
	}
	if validStartRequestPath(r.session.HealthPath) {
		return r.session.HealthPath
	}
	return "/"
}

func (r *startRunner) sendTestRequest(path, expectedGreeting string) int {
	r.lastRequest, r.requestFailure = nil, &startRequestFailure{}
	if r.ctx.Err() != nil {
		return 130
	}
	if !validStartRequestPath(path) {
		return printErr("Invalid request path", errors.New("enter an app-relative path such as / or /health, without a fragment"))
	}
	app, err := r.client.GetApp(r.ctx, r.session.AppSlug)
	if err != nil {
		return printErr("Could not look up app access", err)
	}
	if r.session.AppID != "" && app.ID != r.session.AppID {
		return printErr("App identity changed", errors.New("this app is no longer the one recorded by the deployment session"))
	}
	r.requestFailure.logsAllowed = true
	var result startRequestResult
	if !app.RequireAuthn && (app.PublicAuth.Mode == "" || app.PublicAuth.Mode == api.AppPublicAuthModeOpen) {
		result, err = r.requestPublic(app, path)
	} else {
		result, err = r.requestPrivate(app, path)
	}
	if err != nil {
		if r.ctx.Err() != nil {
			return 130
		}
		PrintWarn(osStdout, "The request did not complete. Check the recent app logs, then retry this check.")
		return printErr("Test request failed", err)
	}
	return r.verifyRequest(result, expectedGreeting)
}

func (r *startRunner) requestPrivate(app api.AppResponse, path string) (startRequestResult, error) {
	// Account credentials stay on the configured control plane. Invocation
	// completion is reported honestly; it is not proof of an HTTP 200.
	started := time.Now()
	response, err := r.client.InvokeApp(r.ctx, r.session.AppSlug, api.InvokeRequest{Method: http.MethodGet, Path: path})
	if err != nil {
		return startRequestResult{}, err
	}
	result := startRequestResult{path: path, appURL: canonicalAppURL(app), body: string(response.Result), invocationID: response.ID, status: response.Status, elapsed: time.Since(started)}
	if response.Status != "completed" {
		return result, fmt.Errorf("invocation %s ended with status %s: %s", response.ID, response.Status, startResponseText(response.Error))
	}
	return result, nil
}

func (r *startRunner) requestPublic(app api.AppResponse, path string) (startRequestResult, error) {
	targetURL := canonicalAppURL(app)
	if preview := deploymentPreviewURL(r.ctx, r.client, r.session.DeploymentID); preview != "" {
		targetURL = preview
	}
	base, err := url.Parse(targetURL)
	if err != nil || base.Host == "" || base.User != nil || (base.Scheme != "https" && base.Scheme != "http") {
		return startRequestResult{}, errors.New("API did not return an HTTP app URL")
	}
	if !validStartRequestPath(path) {
		return startRequestResult{}, errors.New("invalid app-relative request path")
	}
	relative, _ := url.Parse(path)
	target := base.ResolveReference(relative)
	request, err := http.NewRequestWithContext(r.ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return startRequestResult{}, fmt.Errorf("prepare test request: %w", err)
	}
	// Public requests never carry account credentials and never follow a
	// redirect into a different host or mistake that host for this app.
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	started := time.Now()
	response, err := client.Do(request)
	if err != nil {
		return startRequestResult{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil {
		return startRequestResult{}, fmt.Errorf("read test response: %w", err)
	}
	served := response.Header.Get(api.DeploymentIDHeader)
	if served == "" {
		served = response.Header.Get(api.RevisionHeader)
	}
	return startRequestResult{path: path, appURL: target.String(), body: string(body), httpStatus: response.StatusCode, elapsed: time.Since(started), servedDeployment: served}, nil
}

// Keep the direct helper for CLI integration tests and callers that already
// fetched app access. It still follows the same verification contract.
func (r *startRunner) publicRequest(app api.AppResponse, path string) int {
	result, err := r.requestPublic(app, path)
	if err != nil {
		if r.ctx.Err() != nil {
			return 130
		}
		return printErr("Test request failed", err)
	}
	return r.verifyRequest(result, "")
}

func (r *startRunner) verifyRequest(result startRequestResult, expectedGreeting string) int {
	r.lastRequest, r.requestFailure = nil, &startRequestFailure{httpStatus: result.httpStatus, logsAllowed: true}
	if result.httpStatus != 0 && (result.httpStatus < 200 || result.httpStatus >= 300) {
		r.renderRequestDetails(result, false)
		PrintWarn(osStdout, "%s", startHTTPFailureHint(result.httpStatus))
		return 1
	}
	if result.servedDeployment != "" && r.session.DeploymentID != "" && result.servedDeployment != r.session.DeploymentID {
		r.renderRequestDetails(result, false)
		PrintWarn(osStdout, "The request reached deployment %s, while this session deployed %s. Inspect traffic before retrying.", startResponseText(result.servedDeployment), r.session.DeploymentID)
		return 1
	}
	if expectedGreeting != "" && startResponseGreeting(result.body) != expectedGreeting {
		r.renderRequestDetails(result, false)
		PrintWarn(osStdout, "The new greeting was not confirmed. The response above may be from another revision; inspect logs or send another request.")
		return 1
	}
	r.lastRequest = &result
	r.requestFailure = nil
	r.renderRequestSuccess(result)
	return 0
}

func (r *startRunner) renderRequestSuccess(result startRequestResult) {
	_, _ = fmt.Fprintln(osStdout)
	PrintOK(osStdout, "Your app answered. Keep building.")
	r.renderRequestDetails(result, true)
	if !r.startedAt.IsZero() {
		_, _ = fmt.Fprintf(osStdout, "  Session       %s elapsed\n", time.Since(r.startedAt).Round(time.Second))
	}
}

func (r *startRunner) renderRequestDetails(result startRequestResult, verified bool) {
	if result.httpStatus != 0 {
		label := "Request URL"
		if verified {
			label = "Verified URL"
		}
		_, _ = fmt.Fprintf(osStdout, "  %s  %s\n  Request       GET %s · HTTP %d · %s\n", label, startResponseText(result.appURL), result.path, result.httpStatus, result.elapsed.Round(time.Millisecond))
	} else {
		_, _ = fmt.Fprintf(osStdout, "  Private app   %s\n  Request       GET %s · invocation %s · %s · %s\n", startResponseText(result.appURL), result.path, result.invocationID, result.status, result.elapsed.Round(time.Millisecond))
	}
	body := strings.TrimSpace(startResponseText(result.body))
	if body == "" {
		body = "(empty response)"
	}
	lines := strings.SplitN(body, "\n", 9)
	if len(lines) > 8 {
		lines[8] = "... (response shortened)"
	}
	_, _ = fmt.Fprintf(osStdout, "  Response\n    %s\n", strings.Join(lines, "\n    "))
}

func startResponseGreeting(body string) string {
	var payload struct {
		Message string          `json:"message"`
		Body    json.RawMessage `json:"body"`
	}
	if json.Unmarshal([]byte(body), &payload) != nil {
		return ""
	}
	if payload.Message != "" {
		return payload.Message
	}
	var encoded string
	if json.Unmarshal(payload.Body, &encoded) == nil {
		if json.Unmarshal([]byte(encoded), &payload) == nil {
			return payload.Message
		}
	} else if json.Unmarshal(payload.Body, &payload) == nil {
		return payload.Message
	}
	return ""
}

func startResponseText(body string) string {
	body = strings.Map(func(r rune) rune {
		if (r < 32 && r != '\n' && r != '\t') || (r >= 127 && r <= 159) {
			return -1
		}
		return r
	}, body)
	return safetext.Truncate(body, 4096)
}

func (r *startRunner) starterWalkthrough() int {
	_, _ = fmt.Fprintln(osStdout, "\nMake the starter yours. Enter a greeting to see your own change live.")
	greeting, err := r.prompt.text(r.ctx, "New public greeting (Enter to finish)", "")
	if err != nil {
		return startInputExit(err)
	}
	if greeting == "" {
		return 0
	}
	if !utf8.ValidString(greeting) || utf8.RuneCountInString(greeting) > 200 || strings.ContainsAny(greeting, "\x00\x1b") {
		return printErr("Invalid greeting", errors.New("use a greeting up to 200 characters, without terminal control characters"))
	}
	edit, err := startGreetingEdit(r.session.SourcePath, r.session.Template, greeting)
	if err != nil {
		return printErr("Could not propose starter edit", err)
	}
	code, submitted := r.reviewLaunch(&edit)
	if code != 0 {
		code, submitted = r.recoverFailure(code)
	}
	if code != 0 || !submitted {
		return code
	}
	PrintProgress(osStdout, "Checking the new greeting with GET /...")
	if code := r.checkFirstResponse("/", greeting); code != 0 {
		return code
	}
	PrintOK(osStdout, "You changed the code, deployed it, and saw the new greeting live.")
	return 0
}
