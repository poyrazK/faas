package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
)

// Stable problem codes for the ADR-748 synthetic check endpoints.
const (
	CodeSyntheticCheckInvalid = "synthetic_check_invalid"
	CodeSyntheticCheckLimit   = "synthetic_check_limit_reached"
)

var (
	syntheticCheckNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	// syntheticCheckPathPattern mirrors synthetic_checks_path_chk: an
	// origin-relative path ("/" or "/x…"), never "//host", no whitespace
	// or backslashes, so the request can only reach the app's own host.
	syntheticCheckPathPattern = regexp.MustCompile(`^/([^/\s\\][^\s\\]*)?$`)
)

// CreateSyntheticCheckRequest is the POST /v1/apps/{slug}/synthetics body.
// Method defaults to GET, TimeoutMS to SyntheticCheckDefaultTimeoutMS, and
// ExpectedStatus 0 means any 2xx.
type CreateSyntheticCheckRequest struct {
	Name            string `json:"name"`
	Method          string `json:"method,omitempty"`
	Path            string `json:"path"`
	ExpectedStatus  int    `json:"expected_status,omitempty"`
	TimeoutMS       int    `json:"timeout_ms,omitempty"`
	IntervalSeconds int    `json:"interval_seconds"`
}

// UpdateSyntheticCheckRequest pauses or resumes a check.
type UpdateSyntheticCheckRequest struct {
	Enabled bool `json:"enabled"`
}

// SyntheticCheckResponse is one synthetic check definition.
type SyntheticCheckResponse struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Method          string `json:"method"`
	Path            string `json:"path"`
	URL             string `json:"url"`
	ExpectedStatus  int    `json:"expected_status,omitempty"`
	TimeoutMS       int    `json:"timeout_ms"`
	IntervalSeconds int    `json:"interval_seconds"`
	Enabled         bool   `json:"enabled"`
	CreatedAt       string `json:"created_at"`
}

// NormalizeCreateSyntheticCheck applies defaults and checks the request
// against the closed sets the synthetic_checks CHECKs enforce.
func NormalizeCreateSyntheticCheck(req CreateSyntheticCheckRequest) (CreateSyntheticCheckRequest, *Problem) {
	if req.Method == "" {
		req.Method = http.MethodGet
	}
	req.Method = strings.ToUpper(req.Method)
	if req.TimeoutMS == 0 {
		req.TimeoutMS = SyntheticCheckDefaultTimeoutMS
	}
	switch {
	case !syntheticCheckNamePattern.MatchString(req.Name):
		return req, ErrSyntheticCheckInvalid("name must match [a-z][a-z0-9_-]{0,62}")
	case req.Method != http.MethodGet && req.Method != http.MethodHead:
		return req, ErrSyntheticCheckInvalid("method must be GET or HEAD")
	case len(req.Path) > SyntheticCheckPathMaxBytes || !syntheticCheckPathPattern.MatchString(req.Path):
		return req, ErrSyntheticCheckInvalid(fmt.Sprintf("path must start with a single / and contain no whitespace or backslashes (at most %d bytes); the host is always the app's own", SyntheticCheckPathMaxBytes))
	case req.ExpectedStatus != 0 && (req.ExpectedStatus < 100 || req.ExpectedStatus > 599):
		return req, ErrSyntheticCheckInvalid("expected_status must be an HTTP status between 100 and 599, or omitted for any 2xx")
	case req.TimeoutMS < SyntheticCheckTimeoutMinMS || req.TimeoutMS > SyntheticCheckTimeoutMaxMS:
		return req, ErrSyntheticCheckInvalid(fmt.Sprintf("timeout_ms must be between %d and %d", SyntheticCheckTimeoutMinMS, SyntheticCheckTimeoutMaxMS))
	case !slices.Contains(SyntheticCheckIntervalsSeconds, req.IntervalSeconds):
		return req, ErrSyntheticCheckInvalid(fmt.Sprintf("interval_seconds must be one of %v; shorter intervals would keep the app permanently awake", SyntheticCheckIntervalsSeconds))
	}
	return req, nil
}

// ErrSyntheticCheckInvalid is a 400 for a malformed check definition.
func ErrSyntheticCheckInvalid(reason string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeSyntheticCheckInvalid, "Invalid synthetic check", reason).
		WithDocs(docsBase + "/synthetic-checks")
}

// ErrSyntheticCheckLimitReached is a 422 naming the per-app cap.
func ErrSyntheticCheckLimitReached(observed int) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, CodeSyntheticCheckLimit, "Synthetic check limit reached",
		fmt.Sprintf("an app can have at most %d synthetic checks; delete one to add another", MaxSyntheticChecksPerApp)).
		WithLimit(int64(MaxSyntheticChecksPerApp), int64(observed)).
		WithDocs(docsBase + "/synthetic-checks")
}

// ListSyntheticChecks returns an app's synthetic checks (ADR-748).
func (c *Client) ListSyntheticChecks(ctx context.Context, slug string) ([]SyntheticCheckResponse, error) {
	var out []SyntheticCheckResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/synthetics", nil, &out)
}

// CreateSyntheticCheck defines a synthetic check on an app (ADR-748).
func (c *Client) CreateSyntheticCheck(ctx context.Context, slug string, req CreateSyntheticCheckRequest) (SyntheticCheckResponse, error) {
	var out SyntheticCheckResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+slug+"/synthetics", req, &out)
}

// GetSyntheticCheck fetches one synthetic check (ADR-748).
func (c *Client) GetSyntheticCheck(ctx context.Context, slug, id string) (SyntheticCheckResponse, error) {
	var out SyntheticCheckResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+slug+"/synthetics/"+id, nil, &out)
}

// UpdateSyntheticCheck pauses or resumes a synthetic check (ADR-748).
func (c *Client) UpdateSyntheticCheck(ctx context.Context, slug, id string, req UpdateSyntheticCheckRequest) (SyntheticCheckResponse, error) {
	var out SyntheticCheckResponse
	return out, c.do(ctx, "PATCH", "/v1/apps/"+slug+"/synthetics/"+id, req, &out)
}

// DeleteSyntheticCheck removes one synthetic check (ADR-748).
func (c *Client) DeleteSyntheticCheck(ctx context.Context, slug, id string) error {
	return c.do(ctx, "DELETE", "/v1/apps/"+slug+"/synthetics/"+id, nil, nil)
}
