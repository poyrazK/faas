// Dashboard retry form handler (ADR-117 §Production-ready
// follow-on, C4). Mirrors dashboard_cron_fire.go: the dashboard
// links are <form method="POST"> (not XHR), so we reuse the CSRF
// sealed-envelope pattern (IssueForAuthenticated →
// VerifyAuthenticated) and redirect-with-flash back to the app
// page on every branch.
//
// Two reasons this is NOT a thin wrapper around POST
// /v1/deployments/{id}/retry:
//
//  1. CSRF posture differs: v1 endpoints are Bearer-key-only and
//     need no form-binding token; the dashboard cookie session
//     requires a sealed (action, account_id) envelope to defend
//     against CSRF.
//  2. The handler can short-circuit on cross-account probes by
//     reading the row's AccountID directly from the same
//     DeploymentByID lookup the dashboard GET already did. The v1
//     handler does the same probe, but at the cost of an extra
//     round-trip through the deployment-detail's auth chain.
//
// Both paths must still produce a single fresh row in `deployments`
// (inserted via RetryDeploymentFromStage) and seed the row's
// `stage_state.current = from_stage`. The form action carries
// from_stage as a query param so the URL stays stable for the
// customer's bookmark (no hidden-form-only state).
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// dashboardRetryDeploymentAction is the CSRF action binding for
// the retry form on /dashboard/apps/{slug}/deployments/{id}.
// Shared across all failed deployments on the page (one envelope
// per render; the handler revalidates against the URL path +
// account_id). Matches the dashboardDelete / dashboardFireCron
// convention.
const dashboardRetryDeploymentAction = "retry_deployment"

// dashboardRetryDeploymentIDRe pins the deployment id shape used
// in the URL. Accepts either the 32-hex form (memstore's newID
// fallback for tests) or the 36-char UUID form (uuid.NewString
// — pgstore production path). Both are produced by uuid helpers
// in the codebase; the regex matches the canonical lowercase
// hex / hex-with-dashes shape. Validated here so the redirect
// target composed from it can never escape the /dashboard/apps/{slug}
// prefix (the form-post path is a CSRF-bound surface, but a
// G610 tripwire still demands explicit gating).
var dashboardRetryDeploymentIDRe = regexp.MustCompile(`^[0-9a-f]{32}$|^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// dashboardDeploymentSlugRe is the slug shape (lowercase + dash +
// digit, ≤ 48 chars; mirrors cmd/apid/handlers_apps.go's
// validSlug). Same purpose: kill the open-redirect tripwire
// before we concatenate the slug into the redirect target.
var dashboardDeploymentSlugRe = regexp.MustCompile(`^[a-z0-9-]{1,48}$`)

// failedStageFromJSON scans a deployment's stage_state jsonb for
// the first row whose status is "failed" and returns its name.
// Empty string when the jsonb is empty, malformed, or carries no
// failed row (e.g. a row that failed before the jsonb column
// existed). The render path uses this to drive both RetryFromStage
// (the hidden form input) and CanRetry (the form's visibility).
//
// We re-decode the jsonb here rather than threading it through
// the StagePayload struct because pkg/dashboard is the only
// caller of `StagePayload` and it never surfaces stage names to
// the template — the per-row BodyHTML is the only exposure. A
// dedicated helper at the handler edge keeps pkg/dashboard's
// contract intact (handler-edge projector, no template.HTML casts
// in the upstream package).
func failedStageFromJSON(stageState []byte) string {
	if len(stageState) == 0 {
		return ""
	}
	var ss state.StageState
	if uerr := json.Unmarshal(stageState, &ss); uerr != nil {
		return ""
	}
	for _, item := range ss.History {
		if item.Status == "failed" && item.Name != "" {
			return string(item.Name)
		}
	}
	// No history row is marked failed (e.g. the row was created
	// in a pre-ADR-117 world where stage_state was empty) — fall
	// back to the row's current stage when it's a non-empty
	// closed-vocab name. Empty current is "no recorded stage"
	// which the caller's CanRetry gate skips.
	if ss.Current != "" && state.IsStageName(ss.Current) {
		return string(ss.Current)
	}
	return ""
}

// dashboardRetryDeployment handles POST
// /dashboard/apps/{slug}/deployments/{id}/retry. The form posts
// here with a sealed csrf_token and a from_stage query param;
// we verify against the same (action="retry_deployment",
// account_id) envelope as the render path. On success the
// customer lands on /dashboard/apps/{slug}/deployments/{new-id}
// (the new row's id, not the source's) so the SSE stream + form
// re-render on the new deployment.
//
// Returns 302 to /dashboard/apps/{slug}/deployments/<new-id> on
// success, 302 to /dashboard/apps/{slug}/deployments/{id}
// ?retried=1 on v1 closed-vocab-slip (mapped to 302 with
// retried=error flash on every other failure so the dashboard
// doesn't double as an existence oracle).
func (s *server) dashboardRetryDeployment(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	id := r.PathValue("id")
	fromStage := r.URL.Query().Get("from")

	// G610 / G301 guards: validate slug + id against the regex
	// shapes BEFORE composing the redirect target so a malformed
	// form post can't open-redirect to an attacker URL. The
	// storage lookups below also IDOR-validate against the
	// account, but the path-param check is the cheap gate.
	if !dashboardDeploymentSlugRe.MatchString(slug) || !dashboardRetryDeploymentIDRe.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	if fromStage == "" || !state.IsStageName(state.StageName(fromStage)) {
		// Closed-vocab slip. The form was rendered by us, so this
		// only happens on a tampered request — redirect back to
		// the same detail page with a flash flag the template
		// surfaces, never 400 (we don't want to confirm the id
		// existence to a probe that already has the URL).
		http.Redirect(w, r, fmt.Sprintf("/dashboard/apps/%s/deployments/%s?retried=bad", slug, id), http.StatusFound)
		return
	}

	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := middleware.VerifyAuthenticated(s.sessions, r, dashboardRetryDeploymentAction, acct.ID); err != nil {
		http.Redirect(w, r, fmt.Sprintf("/dashboard/apps/%s/deployments/%s?retried=forbidden", slug, id), http.StatusFound)
		return
	}
	ctx := r.Context()

	// IDOR-safe two-step: app first, then the deployment filtered
	// by parent-app id (never account-only; a same-account but
	// cross-app id must not enqueue). Mirrors getDeploymentStages
	// (cmd/apid/handlers_stages.go:49) and retryDeployment
	// (cmd/apid/handlers_retry.go:48).
	app, err := s.store.AppBySlug(ctx, slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	dep, err := s.store.DeploymentByID(ctx, id)
	if err != nil || dep.AppID != app.ID {
		http.NotFound(w, r)
		return
	}

	newDep, err := s.enqueueRetry(ctx, app, dep, state.StageName(fromStage))
	if err != nil {
		if errors.Is(err, state.ErrInvalidArgument) {
			http.Redirect(w, r, fmt.Sprintf("/dashboard/apps/%s/deployments/%s?retried=bad", slug, id), http.StatusFound)
			return
		}
		s.log.Warn("dashboard: retry deployment failed",
			"deployment_id", id, "from_stage", fromStage,
			"account_id", acct.ID, "err", err)
		http.Redirect(w, r, fmt.Sprintf("/dashboard/apps/%s/deployments/%s?retried=error", slug, id), http.StatusFound)
		return
	}

	s.log.Info("dashboard: retry deployment enqueued",
		"source_deployment_id", id,
		"new_deployment_id", newDep.ID,
		"from_stage", fromStage,
		"account_id", acct.ID,
		"app_id", app.ID,
		"surface", "dashboard")
	http.Redirect(w, r, fmt.Sprintf("/dashboard/apps/%s/deployments/%s", slug, newDep.ID), http.StatusFound)
}
