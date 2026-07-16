package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// AsProblem walks err's chain and returns the first *Problem. Returns nil
// if none of the wrapped errors is a *Problem. Used by gRPC handlers in
// pkg/vmmdgrpc to lift a Manager-emitted error without leaking internal
// strings through the wire.
func AsProblem(err error) *Problem {
	if err == nil {
		return nil
	}
	var p *Problem
	if errors.As(err, &p) {
		return p
	}
	return nil
}

// Problem is an RFC 7807 problem+json body. It is the platform's single error
// contract: apid emits it, the CLI and dashboard render it verbatim (spec
// §Conventions, UX spec §7). Every limit error carries the limit, the observed
// value, and a docs URL so the surface never has to invent copy.
type Problem struct {
	// Type is a docs URL identifying the problem class (RFC 7807 "type").
	Type string `json:"type"`
	// Title is a short, stable, human-readable summary.
	Title string `json:"title"`
	// Status is the HTTP status code, duplicated in the body per RFC 7807.
	Status int `json:"status"`
	// Code is a stable machine-readable string (e.g. "plan_limit_apps") that
	// clients branch on. It must never change once shipped.
	Code string `json:"code"`
	// Detail is the specific cause including the observed value.
	Detail string `json:"detail,omitempty"`
	// Limit and Observed are set on quota/limit errors (spec §Conventions).
	Limit    *int64 `json:"limit,omitempty"`
	Observed *int64 `json:"observed,omitempty"`
	// DocsURL points the user at the single next action.
	DocsURL string `json:"docs_url,omitempty"`
}

// Error implements the error interface so a Problem can flow through %w chains.
func (p *Problem) Error() string {
	if p.Detail != "" {
		return fmt.Sprintf("%s: %s", p.Code, p.Detail)
	}
	return p.Code
}

// WriteProblem renders p as an RFC 7807 problem+json response with its status
// code. Every HTTP surface (gatewayd, apid) uses this so error shape is uniform.
func WriteProblem(w http.ResponseWriter, p *Problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// NewProblem builds a Problem with the common fields set.
func NewProblem(status int, code, title, detail string) *Problem {
	return &Problem{Status: status, Code: code, Title: title, Detail: detail}
}

// WithLimit annotates a Problem with the limit and observed value that tripped
// it, returning the same pointer for chaining.
func (p *Problem) WithLimit(limit, observed int64) *Problem {
	p.Limit = &limit
	p.Observed = &observed
	return p
}

// WithDocs sets the docs URL and returns the same pointer for chaining.
func (p *Problem) WithDocs(url string) *Problem {
	p.DocsURL = url
	return p
}

// Stable error codes (spec Appendix A, UX spec §7). Keep in sync with docs and
// the CLI's exit-code mapping.
const (
	CodePlanLimitApps   = "plan_limit_apps"
	CodePlanLimitRAM    = "plan_limit_ram"
	CodePlanLimitConcur = "plan_limit_concurrency"
	CodeSourceTooLarge  = "source_too_large"
	CodeAppLayerTooBig  = "app_layer_too_large"
	CodeBuildUndetected = "build_undetected"
	CodeBuildOOM        = "build_oom"
	CodeBuildTimeout    = "build_timeout"
	CodeQuotaExhausted  = "quota_exhausted"
	CodeBillingPastDue  = "billing_past_due"
	CodeCapacity        = "capacity_unavailable"
	CodeUnauthorized    = "unauthorized"
	CodeNotFound        = "not_found"
	CodeValidation      = "validation_failed"
)

// Convenience constructors for the most common limit errors keep call sites to
// one line and guarantee the limit/observed/docs fields are always populated.

// ErrPlanLimitApps is returned when a deploy would exceed the plan's app count.
func ErrPlanLimitApps(l Limits, observed int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanLimitApps,
		"App limit reached",
		fmt.Sprintf("%s plan allows %d deployed app(s); you have %d.", l.Plan, l.DeployedApps, observed)).
		WithLimit(int64(l.DeployedApps), int64(observed)).
		WithDocs("https://docs.DOMAIN/plans#apps")
}

// ErrPlanLimitRAM is returned when a requested ram_mb exceeds the plan cap.
func ErrPlanLimitRAM(l Limits, requestedMB int) *Problem {
	return NewProblem(http.StatusForbidden, CodePlanLimitRAM,
		"RAM over plan limit",
		fmt.Sprintf("%s plan caps %d MB/app; requested %d MB.", l.Plan, l.RAMMB, requestedMB)).
		WithLimit(int64(l.RAMMB), int64(requestedMB)).
		WithDocs("https://docs.DOMAIN/plans#ram")
}

// ErrAppLayerTooLarge is returned when the built app layer (deps + code) exceeds
// the plan's drive1 cap (spec §4.6). The message names the cap and observed size
// so the deploy failure is actionable.
func ErrAppLayerTooLarge(l Limits, observedBytes int64) *Problem {
	capBytes := int64(l.AppLayerMaxMB) * 1024 * 1024
	return NewProblem(http.StatusForbidden, CodeAppLayerTooBig,
		"App too large",
		fmt.Sprintf("%s plan caps the app layer at %d MB; built layer is %.1f MB.",
			l.Plan, l.AppLayerMaxMB, float64(observedBytes)/(1024*1024))).
		WithLimit(capBytes, observedBytes).
		WithDocs("https://docs.DOMAIN/build/limits#app-layer")
}

// ErrPlanLimitConcurrency is returned when waking another instance would exceed
// the app's concurrency (spec §4.3 admission, invariant §6.2-1).
func ErrPlanLimitConcurrency(l Limits, observed int) *Problem {
	return NewProblem(http.StatusTooManyRequests, CodePlanLimitConcur,
		"Concurrency limit reached",
		fmt.Sprintf("%s plan allows %d concurrent instance(s) per app; %d already live.", l.Plan, l.MaxConcurrency, observed)).
		WithLimit(int64(l.MaxConcurrency), int64(observed)).
		WithDocs("https://docs.DOMAIN/plans#concurrency")
}

// ErrCapacity is returned when admission is refused for lack of box capacity
// (RAM headroom or vCPU slots, spec §4.3). This should be near-impossible in
// practice — admission alerts fire long before customers see it (spec §12) — so
// it is a page for us, not just a message for them (UX spec §7).
func ErrCapacity(detail string) *Problem {
	return NewProblem(http.StatusServiceUnavailable, CodeCapacity,
		"Briefly at capacity", detail).
		WithDocs("https://status.DOMAIN")
}

// ErrSourceTooLarge is returned when an uploaded tarball exceeds the plan cap.
func ErrSourceTooLarge(l Limits, observedBytes int64) *Problem {
	capBytes := int64(l.SourceTarballMaxMB) * 1024 * 1024
	return NewProblem(http.StatusRequestEntityTooLarge, CodeSourceTooLarge,
		"Source too large",
		fmt.Sprintf("%s plan caps source at %d MB.", l.Plan, l.SourceTarballMaxMB)).
		WithLimit(capBytes, observedBytes).
		WithDocs("https://docs.DOMAIN/build/limits")
}
