package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// parseRevisionRef reports whether ref is a revision handle (ADR-198) and
// the revision it names. Mirrors apid's ParseDeploymentRevisionRef exactly —
// both sides must agree on what "v42" means or the CLI would resolve a
// reference the server would have rejected (and vice versa).
//
// Accepts "v42", "V42" and a bare "42". A uuid always contains non-digits,
// so it can never be misread as a revision.
func parseRevisionRef(ref string) (int, bool) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return 0, false
	}
	// A deployment id WINS over the revision reading. Hex digits include
	// 0-9, so an all-numeric 32-char id ("000...001", which fixtures use
	// constantly and gen_random_uuid can produce) is simultaneously a valid
	// id and a valid bare revision. Resolving it as a revision would send
	// the customer to a different deployment than the one they named, or
	// fail outright — silently, and only for ids that happen to be
	// all-digits. Ambiguity is resolved toward the unambiguous form.
	if deploymentIDPattern.MatchString(trimmed) {
		return 0, false
	}
	digits := trimmed
	if c := digits[0]; c == 'v' || c == 'V' {
		digits = digits[1:]
	}
	if digits == "" {
		return 0, false
	}
	// Digits only. strconv.Atoi accepts a leading sign, so without this a
	// "+42" would parse as revision 42 — a form the product never renders
	// and a customer would never copy out of `traffic status`. Accepting
	// input we cannot produce only widens the surface a typo can slip
	// through. The n <= 0 check below already covers "-1".
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// renderRevision spells a revision the one way the product spells it.
// Returns "" for the unassigned sentinel so callers can fall back to the
// deployment id rather than printing a meaningless "v0".
func renderRevision(revision int) string {
	if revision <= 0 {
		return ""
	}
	return "v" + strconv.Itoa(revision)
}

// deploymentLabel is what the CLI prints to identify a deployment: the
// revision when the row has one, otherwise the raw id. Every human-facing
// deployment reference should go through this so output stays consistent
// for pre-ADR-198 rows.
func deploymentLabel(d api.DeploymentResponse) string {
	if label := renderRevision(d.Revision); label != "" {
		return label
	}
	return d.ID
}

// resolveDeploymentRef turns a user-supplied deployment reference into a
// deployment id, resolving a "v42" revision handle against appSlug's
// ladder. A uuid passes through untouched without an API round-trip.
//
// Resolution is client-side here because the traffic endpoint is addressed
// by deployment id alone and carries no app context; the app slug the user
// supplied is what makes a revision unambiguous. The rollback endpoint does
// carry app context and resolves server-side instead, so both reference
// forms work regardless of which client is calling.
func resolveDeploymentRef(ctx context.Context, client *api.Client, appSlug, ref string) (string, error) {
	revision, ok := parseRevisionRef(ref)
	if !ok {
		return ref, nil
	}
	if strings.TrimSpace(appSlug) == "" {
		return "", fmt.Errorf("--app is required to resolve the deployment revision %s", renderRevision(revision))
	}
	deployments, err := client.ListAppDeploymentsAll(ctx, appSlug)
	if err != nil {
		return "", fmt.Errorf("list deployments for %s: %w", appSlug, err)
	}
	for _, d := range deployments {
		if d.Revision == revision {
			return d.ID, nil
		}
	}
	return "", fmt.Errorf("no deployment %s exists for app %q", renderRevision(revision), appSlug)
}

// resolveDeploymentArg is the resolver every deployment-addressing command
// should call. It differs from resolveDeploymentRef in where the app comes
// from: a `v42` handle is only meaningful relative to one app, and most of
// these commands are addressed by deployment id alone.
//
// App resolution order, matching resolveAppFlagOrContext used across the CLI:
//
//  1. an explicit --app flag;
//  2. otherwise the app linked to this checkout (`gregale link --app`).
//
// So `gregale deployment v42` just works inside a linked project, and --app
// covers the unlinked case. A uuid short-circuits before any of this, so no
// command pays a lookup or requires an app for the reference form it already
// accepted — every existing invocation keeps working untouched.
//
// The error names --app explicitly rather than surfacing the link machinery's
// "project has multiple or no workloads" prose: the caller typed a revision,
// and the actionable fix is to say which app it belongs to.
func resolveDeploymentArg(ctx context.Context, client *api.Client, explicitApp, ref string) (string, error) {
	revision, ok := parseRevisionRef(ref)
	if !ok {
		return ref, nil
	}
	appSlug, err := resolveAppFlagOrContext(explicitApp)
	if err != nil || strings.TrimSpace(appSlug) == "" {
		return "", fmt.Errorf("cannot resolve deployment revision %s: pass --app <slug>, or run inside a linked project (gregale link --app <slug>)", renderRevision(revision))
	}
	return resolveDeploymentRef(ctx, client, appSlug, ref)
}

// validDeploymentRef reports whether ref is an acceptable deployment
// reference: either the 32-hex / dashed uuid shape deploymentIDPattern
// enforces, or an ADR-198 `v42` revision handle.
//
// This replaces bare deploymentIDPattern checks at ARGUMENT-VALIDATION time
// so `gregale deployment v42` is not rejected before it can be resolved. The
// uuid arm is unchanged, so a malformed id still fails locally with
// validation_failed rather than costing a 404 round-trip (UX §3.3, "the first
// error is the right one") — the only new thing accepted is a form the
// product itself prints.
func validDeploymentRef(ref string) bool {
	if deploymentIDPattern.MatchString(ref) {
		return true
	}
	_, ok := parseRevisionRef(ref)
	return ok
}
