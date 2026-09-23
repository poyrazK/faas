package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// newServiceProxyAuthorizer enforces the same-account boundary between a
// calling workload and the service it names (ADR-168), then applies the
// caller's opt-in declared-binding policy.
//
// Ids are shape-checked before they reach the store. `apps.id` is a uuid
// column, so a malformed id can never name an app — but passing one through
// makes Postgres raise 22P02, which the proxy could only report as
// "service authorization is unavailable" (503). That blames the platform for
// what is a caller error, and costs a round-trip that is guaranteed to fail.
// Guarding here keeps a genuine store failure — the case that really is a
// platform fault — as the only path that still returns 503.
//
// Extracted from run() so the boundary is unit-testable without the daemon.
func newServiceProxyAuthorizer(store state.Store) gateway.ServiceProxyAuthorizer {
	return func(ctx context.Context, callerAppID, targetAppID string) (gateway.ServiceCaller, error) {
		if !isAppID(callerAppID) || !isAppID(targetAppID) {
			return gateway.ServiceCaller{}, gateway.ErrServiceProxyDenied
		}
		caller, err := store.AppByID(ctx, callerAppID)
		if errors.Is(err, state.ErrNotFound) {
			return gateway.ServiceCaller{}, gateway.ErrServiceProxyDenied
		}
		if err != nil {
			return gateway.ServiceCaller{}, fmt.Errorf("load caller app: %w", err)
		}
		target, err := store.AppByID(ctx, targetAppID)
		if errors.Is(err, state.ErrNotFound) {
			return gateway.ServiceCaller{}, gateway.ErrServiceProxyDenied
		}
		if err != nil {
			return gateway.ServiceCaller{}, fmt.Errorf("load target app: %w", err)
		}
		// An empty account on either side is a broken row rather than a
		// match; fail closed instead of letting "" == "" authorize the call.
		if caller.AccountID == "" || caller.AccountID != target.AccountID {
			return gateway.ServiceCaller{}, gateway.ErrServiceProxyDenied
		}
		// A project PR preview may only call another preview selected from the
		// same project and PR. The resolver already enforces this during name
		// lookup; repeat the invariant here so alternate/out-of-tree resolver
		// wiring cannot turn a generated preview slug into a cross-environment
		// escape hatch.
		projectPreviewToPreview := caller.PreviewOfSlug != "" && caller.ProjectID != "" && caller.PreviewPrNumber > 0 && target.PreviewOfSlug != ""
		if projectPreviewToPreview {
			if target.ProjectID != caller.ProjectID || target.PreviewPrNumber != caller.PreviewPrNumber {
				return gateway.ServiceCaller{}, gateway.ErrServiceProxyDenied
			}
		}
		// When no same-PR workload exists, the resolver falls back to the
		// production service. Enforce the customer-owned project policy before
		// endpoint lookup or wake so a denied call cannot consume production
		// capacity or produce application side effects.
		if caller.PreviewOfSlug != "" && target.PreviewOfSlug == "" {
			projectID, err := previewCallerProjectID(ctx, store, caller)
			if err != nil {
				return gateway.ServiceCaller{}, err
			}
			if projectID != "" {
				policyStore, ok := store.(state.GitHubDeployPolicyStore)
				if !ok {
					return gateway.ServiceCaller{}, errors.New("preview service policy storage is unavailable")
				}
				policy, err := policyStore.GetGitHubDeployPolicy(ctx, projectID, caller.AccountID)
				if err != nil {
					return gateway.ServiceCaller{}, fmt.Errorf("load preview service policy: %w", err)
				}
				if policy.PreviewServicePolicy == state.PreviewServicePolicyDeny {
					return gateway.ServiceCaller{}, gateway.ErrServiceProxyPreviewProductionDenied
				}
			}
			if target.Manifest.EffectivePreviewServiceCallsPolicy() == api.PreviewServiceCallsDeny {
				return gateway.ServiceCaller{}, gateway.ErrServiceProxyPreviewDenied
			}
		}
		if caller.Manifest.EffectiveServiceBindingPolicy() == api.ServiceBindingPolicyDeclared {
			bindingTarget := target.Slug
			if projectPreviewToPreview {
				// Compose binds the logical workload name, not the generated
				// pr-N slug. The environment check above makes this alias safe.
				bindingTarget = target.PreviewOfSlug
			}
			declared := false
			for _, binding := range caller.Manifest.ServiceBindings {
				if strings.EqualFold(strings.TrimSpace(binding.Service), strings.TrimSpace(bindingTarget)) {
					declared = true
					break
				}
			}
			if !declared {
				return gateway.ServiceCaller{}, gateway.ErrServiceProxyBindingDenied
			}
		}
		// The caller row is already loaded; carrying its preview identity out
		// saves the hop a third store read for a fact we have in hand.
		return gateway.ServiceCaller{
			AppID:         caller.ID,
			PreviewOfSlug: caller.PreviewOfSlug,
			AccountID:     caller.AccountID,
		}, nil
	}
}

// previewCallerProjectID preserves policy enforcement for preview rows created
// before githubd copied project_id onto them. New previews take the zero-read
// fast path; legacy rows pay one parent lookup only while they remain alive.
// Standalone app previews have no project policy and retain the documented
// allow-and-mark behaviour.
func previewCallerProjectID(ctx context.Context, store state.Store, caller state.App) (string, error) {
	if caller.ProjectID != "" || caller.PreviewOfSlug == "" {
		return caller.ProjectID, nil
	}
	parent, err := store.AppBySlug(ctx, caller.PreviewOfSlug)
	if errors.Is(err, state.ErrNotFound) {
		// A dangling preview identity is not a standalone app. Without its
		// parent we cannot prove whether a project policy applies, so fail
		// closed instead of silently restoring production access.
		return "", gateway.ErrServiceProxyDenied
	}
	if err != nil {
		return "", fmt.Errorf("load preview parent app: %w", err)
	}
	if parent.AccountID != caller.AccountID {
		return "", gateway.ErrServiceProxyDenied
	}
	return parent.ProjectID, nil
}

// isAppID reports whether the id could name a row in apps.id (a uuid column).
func isAppID(id string) bool {
	if id == "" {
		return false
	}
	_, err := uuid.Parse(id)
	return err == nil
}
