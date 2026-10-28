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
	// Kind is slo, new_errors, custom_metric or ai.
	Kind string `json:"kind"`
	// SLO names the SLO an slo gate judges; MaxBurnRate fails the gate.
	SLO         string `json:"slo,omitempty"`
	MaxBurnRate int    `json:"max_burn_rate,omitempty"`
	// MaxNewGroups and MinEvents configure a new_errors gate.
	MaxNewGroups *int `json:"max_new_groups,omitempty"`
	MinEvents    int  `json:"min_events,omitempty"`
	// Metric, Comparison (below or above) and Threshold configure a
	// custom_metric gate. An ai gate takes Metric (error_rate,
	// flagged_rate, cost_per_call or ttfb_ms) and MaxIncreasePercent, the
	// largest increase over the baseline (ADR 20261022153155).
	Metric             string   `json:"metric,omitempty"`
	Comparison         string   `json:"comparison,omitempty"`
	Threshold          *float64 `json:"threshold,omitempty"`
	MaxIncreasePercent int      `json:"max_increase_percent,omitempty"`
	// MinSamples is the requests, metric values or AI calls a release
	// needs before the gate is judged.
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
	// MaxIncreasePercent configures an ai gate (ADR 20261022153155).
	MaxIncreasePercent int `json:"max_increase_percent,omitempty"`
	MinSamples         int `json:"min_samples,omitempty"`
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
	// Observed is the burn rate, the number of new error groups, the
	// metric average, or the release's AI metric; Bound is the value that
	// fails the gate.
	Observed float64 `json:"observed"`
	Bound    float64 `json:"bound"`
	// Samples is the release's requests, new-error events, metric values or
	// AI calls.
	Samples int64 `json:"samples"`
	// BaselineBadFraction is the predecessor's bad fraction (slo gates).
	BaselineBadFraction float64 `json:"baseline_bad_fraction,omitempty"`
	// ErrorClasses are the new groups' error classes (new_errors gates).
	ErrorClasses []string `json:"error_classes,omitempty"`
	Summary      string   `json:"summary"`
	// NotJudged marks a gate with too few samples or unreadable evidence.
	// It did not hold the rollout (gates never hold on missing evidence);
	// Summary says what it was waiting for (release review 2026-10-28).
	NotJudged bool `json:"not_judged,omitempty"`
}

func (r ReleaseGateResult) valid() bool {
	if len(r.Name) == 0 || len(r.Name) > 63 || len(r.Summary) > RolloutDecisionSummaryMaxBytes || len(r.ErrorClasses) > ReleaseGateErrorClassesMax {
		return false
	}
	switch r.Kind {
	case "slo", "new_errors", "custom_metric", "ai":
	default:
		return false
	}
	for _, v := range []float64{r.Observed, r.Bound, r.BaselineBadFraction} {
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
