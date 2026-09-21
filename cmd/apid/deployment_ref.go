package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ParseDeploymentRevisionRef reports whether ref is a customer-facing
// revision handle (ADR-198) and, if so, the revision it names.
//
// Accepted: "v42" / "V42" (the rendered form) and a bare "42" (what a
// customer types after reading `v42` in the CLI). Anything else — most
// importantly a uuid — is left alone for the caller to treat as an id.
//
// A uuid can never be mistaken for a revision: uuids always contain
// non-digit characters, and strconv.Atoi rejects them outright.
// Non-positive values are rejected here rather than reaching the store,
// because revisions are 1-based and 0 is the "unassigned" sentinel.
func ParseDeploymentRevisionRef(ref string) (int, bool) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
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

// resolveDeploymentRef turns a deployment reference into a deployment id.
// A uuid passes through untouched; a `v42` / `42` revision handle is
// resolved against appID's ladder (ADR-198).
//
// Scoping the lookup to appID is what keeps revisions safe as a public
// handle: `v42` is only ever meaningful relative to one app, so this can
// never reach across accounts. An unknown revision returns the same
// rollback-target-not-found problem an unknown uuid would, so the two
// reference forms are indistinguishable to a prober.
func (s *server) resolveDeploymentRef(ctx context.Context, appID, ref string) (string, *api.Problem) {
	revision, ok := ParseDeploymentRevisionRef(ref)
	if !ok {
		return ref, nil
	}
	dep, err := s.store.DeploymentByRevision(ctx, appID, revision)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return "", api.ErrRollbackTargetNotFound(fmt.Sprintf("no deployment %s exists for this app", renderRevision(revision)))
		}
		return "", api.ErrCapacity(fmt.Sprintf("resolve deployment revision: %v", err))
	}
	return dep.ID, nil
}

// renderRevision is the single place that decides how a revision is
// spelled to a customer. Keep the CLI, dashboard and API problem strings
// going through this shape so `v42` never drifts into `#42` or `42`.
func renderRevision(revision int) string {
	if revision <= 0 {
		return ""
	}
	return "v" + strconv.Itoa(revision)
}
