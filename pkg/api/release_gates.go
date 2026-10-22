package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
)

// ReleaseGate is one of an app's release gates (ADR-20261019071734).
type ReleaseGate struct {
	Name string `json:"name"`
	// Kind is slo, new_errors, custom_metric, synthetic or metric_change.
	Kind string `json:"kind"`
	// SLO names the SLO an slo gate judges; MaxBurnRate fails the gate.
	SLO         string `json:"slo,omitempty"`
	MaxBurnRate int    `json:"max_burn_rate,omitempty"`
	// MaxNewGroups and MinEvents configure a new_errors gate.
	MaxNewGroups *int `json:"max_new_groups,omitempty"`
	MinEvents    int  `json:"min_events,omitempty"`
	// Metric, Comparison (below or above) and Threshold configure a
	// custom_metric gate.
	Metric     string   `json:"metric,omitempty"`
	Comparison string   `json:"comparison,omitempty"`
	Threshold  *float64 `json:"threshold,omitempty"`
	// Check and MaxFailurePercent configure a synthetic gate: the synthetic
	// check probed on the canary and the share of failed probes it allows
	// (ADR-20261022082305).
	Check             string `json:"check,omitempty"`
	MaxFailurePercent *int   `json:"max_failure_percent,omitempty"`
	// MaxChangePercent is how far a metric_change gate's metric may move in
	// its comparison direction, in percent of its average before the rollout.
	MaxChangePercent int `json:"max_change_percent,omitempty"`
	// MinSamples is the requests, probes or metric values a release needs
	// before the gate is judged.
	MinSamples int    `json:"min_samples"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  string `json:"created_at"`
}

// CreateReleaseGateRequest creates a release gate. Omitted numbers take the
// defaults in limits.go.
type CreateReleaseGateRequest struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	SLO          string   `json:"slo,omitempty"`
	MaxBurnRate  int      `json:"max_burn_rate,omitempty"`
	MaxNewGroups *int     `json:"max_new_groups,omitempty"`
	MinEvents    int      `json:"min_events,omitempty"`
	Metric       string   `json:"metric,omitempty"`
	Comparison   string   `json:"comparison,omitempty"`
	Threshold    *float64 `json:"threshold,omitempty"`
	MinSamples   int      `json:"min_samples,omitempty"`
	// Check, MaxFailurePercent and MaxChangePercent configure synthetic and
	// metric_change gates.
	Check             string `json:"check,omitempty"`
	MaxFailurePercent *int   `json:"max_failure_percent,omitempty"`
	MaxChangePercent  int    `json:"max_change_percent,omitempty"`
}

// ListReleaseGatesResponse lists an app's release gates.
type ListReleaseGatesResponse struct {
	Gates []ReleaseGate `json:"gates"`
}

// ReleaseGateResult is one judged gate in a rollout decision. It carries
// aggregates only: no routes, messages or metric labels.
type ReleaseGateResult struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Passed bool   `json:"passed"`
	// Observed is the burn rate, the number of new error groups, or the
	// metric average; Bound is the value that fails the gate.
	Observed float64 `json:"observed"`
	Bound    float64 `json:"bound"`
	// Samples is the release's requests, new-error events, or metric values.
	Samples int64 `json:"samples"`
	// BaselineBadFraction is the predecessor's bad fraction (slo gates).
	BaselineBadFraction float64 `json:"baseline_bad_fraction,omitempty"`
	// Baseline is the stable revision's failed-probe percentage (synthetic
	// gates) or the metric's average before the rollout (metric_change).
	Baseline float64 `json:"baseline,omitempty"`
	// ErrorClasses are the new groups' error classes (new_errors gates).
	ErrorClasses []string `json:"error_classes,omitempty"`
	Summary      string   `json:"summary"`
}

func (r ReleaseGateResult) valid() bool {
	if len(r.Name) == 0 || len(r.Name) > 63 || len(r.Summary) > RolloutDecisionSummaryMaxBytes || len(r.ErrorClasses) > ReleaseGateErrorClassesMax {
		return false
	}
	switch r.Kind {
	case "slo", "new_errors", "custom_metric", "synthetic", "metric_change":
	default:
		return false
	}
	for _, v := range []float64{r.Observed, r.Bound, r.BaselineBadFraction, r.Baseline} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return r.Samples >= 0
}

func validReleaseGateResults(results []ReleaseGateResult) error {
	if len(results) > ReleaseGatesPerAppMax {
		return fmt.Errorf("rollout decision has more than %d gate results", ReleaseGatesPerAppMax)
	}
	for _, r := range results {
		if !r.valid() {
			return errors.New("rollout decision gate results must be closed-set, finite and bounded")
		}
	}
	return nil
}

// ErrReleaseGateLimit reports the per-app gate limit.
func ErrReleaseGateLimit(observed int) *Problem {
	return NewProblem(http.StatusConflict, CodeReleaseGateLimit, "Too many release gates",
		fmt.Sprintf("An app can have %d release gates; this one has %d.", ReleaseGatesPerAppMax, observed)).
		WithHint("Delete a gate you no longer need with `gregale release-gates rm`.").
		WithDocs(docsBase + "/deploys#release-gates")
}

// ErrReleaseGateNotFound reports a missing gate.
func ErrReleaseGateNotFound(name string) *Problem {
	return NewProblem(http.StatusNotFound, CodeReleaseGateNotFound, "Release gate not found",
		fmt.Sprintf("The app has no release gate named %q.", name)).
		WithHint("List gates with `gregale release-gates list`.")
}

// ListReleaseGates lists an app's release gates.
func (c *Client) ListReleaseGates(ctx context.Context, slug string) (ListReleaseGatesResponse, error) {
	var out ListReleaseGatesResponse
	return out, c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/release-gates", nil, &out)
}

// CreateReleaseGate creates a release gate.
func (c *Client) CreateReleaseGate(ctx context.Context, slug string, req CreateReleaseGateRequest) (ReleaseGate, error) {
	var out ReleaseGate
	return out, c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/release-gates", req, &out)
}

// DeleteReleaseGate deletes a release gate.
func (c *Client) DeleteReleaseGate(ctx context.Context, slug, name string) (ReleaseGate, error) {
	var out ReleaseGate
	return out, c.do(ctx, http.MethodDelete, "/v1/apps/"+url.PathEscape(slug)+"/release-gates/"+url.PathEscape(name), nil, &out)
}
