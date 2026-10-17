package imaged

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// AssignedBasesResult describes the runtime bases reconciled before imaged
// becomes ready. Runtimes is sorted so startup logs and tests are stable.
type AssignedBasesResult struct {
	Runtimes []string
	Minimal  bool
	// Deferred lists runtimes the rolling pre-stage left for the serving
	// imaged because the serving vmmd predates verified parents (H10-95).
	Deferred []string
}

type assignedBaseStore interface {
	ComputeNodeByName(context.Context, string) (state.ComputeNode, error)
	ListAppsByNodeID(context.Context, string) ([]state.App, error)
	ListAllApps(context.Context) ([]state.App, error)
	ListDeploymentsByNodeID(context.Context, string) ([]state.Deployment, error)
	ListAllDeployments(context.Context) ([]state.Deployment, error)
}

// EnsureAssignedBases stages the current release's base generation for every
// non-deleted app assigned to this imaged process. Runtime bases remain lazy on
// a new empty node, while a rolling release cannot leave an existing app with
// vmmd expecting a generation that has never been published.
func (h *Handler) EnsureAssignedBases(ctx context.Context, arch string, envLookup func(string) string) (AssignedBasesResult, error) {
	if h == nil || h.store == nil {
		return AssignedBasesResult{}, fmt.Errorf("imaged: ensure assigned bases: store is not configured")
	}
	runtimes, minimal, err := assignedBasePlan(ctx, h.store, h.nodeName)
	if err != nil {
		return AssignedBasesResult{}, err
	}
	if minimal {
		if _, err := h.EnsureMinimalBase(ctx, arch, envLookup); err != nil {
			return AssignedBasesResult{}, fmt.Errorf("imaged: reconcile assigned minimal base: %w", err)
		}
	}
	deferred, err := reconcileAssignedRuntimes(runtimes, h.deferLegacyParentBases, func(runtime string) error {
		_, err := h.EnsureRuntimeBase(ctx, runtime, arch, envLookup)
		return err
	})
	if err != nil {
		return AssignedBasesResult{}, err
	}
	return AssignedBasesResult{Runtimes: runtimes, Minimal: minimal, Deferred: deferred}, nil
}

// reconcileAssignedRuntimes stages each runtime in order. Only a pre-stage
// may defer, and only for ErrVerifiedParentUnsupported: every other failure,
// and every failure in the serving imaged, stays fatal.
func reconcileAssignedRuntimes(runtimes []string, deferLegacyParent bool, stage func(string) error) ([]string, error) {
	var deferred []string
	for _, runtime := range runtimes {
		err := stage(runtime)
		if err != nil && deferLegacyParent && errors.Is(err, ErrVerifiedParentUnsupported) {
			deferred = append(deferred, runtime)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("imaged: reconcile assigned runtime %s: %w", runtime, err)
		}
	}
	return deferred, nil
}

func assignedBasePlan(ctx context.Context, store assignedBaseStore, nodeName string) ([]string, bool, error) {
	var (
		apps []state.App
		deps []state.Deployment
		err  error
	)
	if nodeName == "" {
		apps, err = store.ListAllApps(ctx)
		if err == nil {
			deps, err = store.ListAllDeployments(ctx)
		}
	} else {
		node, lookupErr := store.ComputeNodeByName(ctx, nodeName)
		if lookupErr != nil {
			return nil, false, fmt.Errorf("imaged: resolve owner node %s for runtime-base reconciliation: %w", nodeName, lookupErr)
		}
		apps, err = store.ListAppsByNodeID(ctx, node.ID)
		if err == nil {
			deps, err = store.ListDeploymentsByNodeID(ctx, node.ID)
		}
	}
	if err != nil {
		return nil, false, fmt.Errorf("imaged: list assigned apps for runtime-base reconciliation: %w", err)
	}

	seen := make(map[string]struct{})
	minimal := false
	for _, app := range apps {
		if app.Runtime == "" {
			minimal = true
			continue
		}
		// Runtimes sharing a base (binary → go124, ADR-987) stage it once.
		seen[api.FunctionBaseRuntime(app.Runtime)] = struct{}{}
	}
	// A source app built for a shared runner base boots that base as drive0
	// (ADR-1500, H10-140), so it is staged like a function runtime even though
	// the app row carries no runtime.
	for _, dep := range deps {
		if dep.RuntimeBase == "" || dep.Status == state.DeployFailed || dep.Status == state.DeployCancelled {
			continue
		}
		seen[dep.RuntimeBase] = struct{}{}
	}
	runtimes := make([]string, 0, len(seen))
	for runtime := range seen {
		runtimes = append(runtimes, runtime)
	}
	sort.Strings(runtimes)
	return runtimes, minimal, nil
}
