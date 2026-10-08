package api

// Production fork wire shapes and problems (ADR-732). A fork is a
// quarantined, non-serving restore of the app's newest capture that is
// destroyed at its TTL.

import (
	"fmt"
	"net/http"
	"time"
)

// CreateAppForkRequest is the body of POST /v1/apps/{slug}/forks. An
// omitted TTL takes AppForkDefaultTTL.
type CreateAppForkRequest struct {
	TTLSeconds *int `json:"ttl_seconds,omitempty"`
}

// AppForkRequestMaxBytes bounds the create body; it carries one integer.
const AppForkRequestMaxBytes = 1024

// ResolveTTL returns the requested TTL in seconds, or a 400 problem when it
// is outside AppForkMinTTL..AppForkMaxTTL.
func (r CreateAppForkRequest) ResolveTTL() (int, *Problem) {
	if r.TTLSeconds == nil {
		return int(AppForkDefaultTTL / time.Second), nil
	}
	ttl := *r.TTLSeconds
	minS, maxS := int(AppForkMinTTL/time.Second), int(AppForkMaxTTL/time.Second)
	if ttl < minS || ttl > maxS {
		return 0, ErrValidation(fmt.Sprintf("ttl_seconds must be between %d and %d", minS, maxS)).
			WithLimit(int64(maxS), int64(ttl)).
			WithDocs(docsBase + "/forks#ttl")
	}
	return ttl, nil
}

// AppForkStatus mirrors state.AppForkStatus on the wire.
type AppForkStatus string

// AppForkFailure explains a failed fork.
type AppForkFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// AppForkResponse is one fork. Timestamps are RFC3339Nano UTC.
type AppForkResponse struct {
	ID                string          `json:"id"`
	AppID             string          `json:"app_id"`
	DeploymentID      string          `json:"deployment_id"`
	Status            AppForkStatus   `json:"status"`
	TTLSeconds        int             `json:"ttl_seconds"`
	ExpiresAt         string          `json:"expires_at"`
	CancelRequestedAt *string         `json:"cancel_requested_at,omitempty"`
	StartedAt         *string         `json:"started_at,omitempty"`
	FinishedAt        *string         `json:"finished_at,omitempty"`
	Failure           *AppForkFailure `json:"failure,omitempty"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
}

// AppForkListResponse is GET /v1/apps/{slug}/forks, newest first.
type AppForkListResponse struct {
	Items []AppForkResponse `json:"items"`
}

const (
	// CodeAppForksNotEnabled: the operator has not enabled forks on this
	// control plane (FAAS_APP_FORKS).
	CodeAppForksNotEnabled = "app_forks_not_enabled"
	// CodePlanAppForksNotAllowed: the plan has no production forks.
	CodePlanAppForksNotAllowed = "plan_app_forks_not_allowed"
	// CodeAppForkLimit: an active-fork cap (per app or per account) is full.
	CodeAppForkLimit = "app_fork_limit"
	// CodeAppForkUnavailable: the app has no live deployment to fork.
	CodeAppForkUnavailable = "app_fork_unavailable"
)

// ErrAppForksNotEnabled is returned by every fork route until the operator
// turns forks on.
func ErrAppForksNotEnabled() *Problem {
	return NewProblem(http.StatusNotImplemented, CodeAppForksNotEnabled,
		"Production forks unavailable",
		"production forks are not enabled on this control-plane host").
		WithDocs(docsBase + "/forks")
}

// ErrPlanAppForksNotAllowed gates fork creation below Pro.
func ErrPlanAppForksNotAllowed(p Plan) *Problem {
	return NewProblem(http.StatusPaymentRequired, CodePlanAppForksNotAllowed,
		"Production forks unavailable on this plan",
		fmt.Sprintf("the %s plan does not include production forks; upgrade to Pro or above.", p)).
		WithDocs(docsBase + "/plans#forks")
}

// ErrAppForkLimit names the full cap ("app" or "account") with the limit and
// the observed active count.
func ErrAppForkLimit(scope string, limit, observed int) *Problem {
	return NewProblem(http.StatusConflict, CodeAppForkLimit,
		"Active fork limit reached",
		fmt.Sprintf("this %s already has %d active fork(s); the limit is %d. Delete a fork or wait for it to expire.", scope, observed, limit)).
		WithLimit(int64(limit), int64(observed)).
		WithDocs(docsBase + "/forks#limits")
}

// ErrAppForkUnavailable is returned when the app has no live deployment.
func ErrAppForkUnavailable() *Problem {
	return NewProblem(http.StatusConflict, CodeAppForkUnavailable,
		"Nothing to fork",
		"the app has no live deployment to fork; deploy it first.").
		WithDocs(docsBase + "/forks")
}
