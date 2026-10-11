package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// cliPreAuthPatch builds the pre_auth_rate_limit value for
// `gregale app <slug> --pre-auth MODE [--pre-auth-rps N] [--pre-auth-burst N]`.
// The API replaces the whole config, so stored route overrides are carried
// over unchanged.
func cliPreAuthPatch(current *api.PreAuthRateLimitConfig, mode string, rps, burst int, setMode, setRPS, setBurst bool) (*api.PreAuthRateLimitConfig, error) {
	next := &api.PreAuthRateLimitConfig{}
	if current != nil {
		*next = *current
		next.Routes = slices.Clone(current.Routes)
	} else if !setMode {
		return nil, errors.New("--pre-auth-rps and --pre-auth-burst need --pre-auth when the app has no pre-auth config")
	}
	if setMode {
		switch mode {
		case api.PreAuthRateLimitOff, api.PreAuthRateLimitObserve, api.PreAuthRateLimitEnforce:
		default:
			return nil, fmt.Errorf("--pre-auth must be off, observe, or enforce; got %q", mode)
		}
		next.Mode = mode
	}
	if setRPS {
		next.RequestsPerSecond = rps
	}
	if setBurst {
		next.Burst = burst
	}
	if next.Mode != api.PreAuthRateLimitOff && (next.RequestsPerSecond < 1 || next.Burst < 1) {
		return nil, errors.New("--pre-auth observe|enforce needs --pre-auth-rps and --pre-auth-burst of at least 1")
	}
	return next, nil
}

// printPreAuthEnforceAdvice shows the server's enforce suggestion for the last
// 24h before the CLI switches a guard to enforce. It never blocks the change:
// the customer asked for it explicitly.
func printPreAuthEnforceAdvice(ctx context.Context, client *Client, slug string, w io.Writer) {
	obs, err := client.GetAppPreAuthObservations(ctx, slug, "24h")
	if err != nil {
		_, _ = fmt.Fprintf(w, "note: could not check pre-auth observations: %v\n", err)
		return
	}
	s := obs.Suggestion
	if s == nil {
		return
	}
	prefix := "note"
	if s.Status == api.PreAuthSuggestionReview {
		prefix = "warning"
	}
	_, _ = fmt.Fprintf(w, "%s: pre-auth check (24h) is %s: %s\n", prefix, s.Status, s.Reason)
}

// preAuthSummary is the one-line pre-auth state for `gregale app <slug>`.
func preAuthSummary(config *api.PreAuthRateLimitConfig) string {
	if config == nil || config.Mode == api.PreAuthRateLimitOff {
		return api.PreAuthRateLimitOff
	}
	summary := fmt.Sprintf("%s (%d rps, burst %d per source)", config.Mode, config.RequestsPerSecond, config.Burst)
	if n := len(config.Routes); n > 0 {
		summary += fmt.Sprintf(", %d route overrides", n)
	}
	return summary
}
