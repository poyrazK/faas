package imaged

import (
	"context"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/state"
)

// AssignedBasesResult describes the runtime bases reconciled before imaged
// becomes ready. Runtimes is sorted so startup logs and tests are stable.
type AssignedBasesResult struct {
	Runtimes []string
	Minimal  bool
}

type assignedBaseStore interface {
	ComputeNodeByName(context.Context, string) (state.ComputeNode, error)
	ListAppsByNodeID(context.Context, string) ([]state.App, error)
	ListAllApps(context.Context) ([]state.App, error)
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
	for _, runtime := range runtimes {
		if _, err := h.EnsureRuntimeBase(ctx, runtime, arch, envLookup); err != nil {
			return AssignedBasesResult{}, fmt.Errorf("imaged: reconcile assigned runtime %s: %w", runtime, err)
		}
	}
	return AssignedBasesResult{Runtimes: runtimes, Minimal: minimal}, nil
}

func assignedBasePlan(ctx context.Context, store assignedBaseStore, nodeName string) ([]string, bool, error) {
	var (
		apps []state.App
		err  error
	)
	if nodeName == "" {
		apps, err = store.ListAllApps(ctx)
	} else {
		node, lookupErr := store.ComputeNodeByName(ctx, nodeName)
		if lookupErr != nil {
			return nil, false, fmt.Errorf("imaged: resolve owner node %s for runtime-base reconciliation: %w", nodeName, lookupErr)
		}
		apps, err = store.ListAppsByNodeID(ctx, node.ID)
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
		seen[app.Runtime] = struct{}{}
	}
	runtimes := make([]string, 0, len(seen))
	for runtime := range seen {
		runtimes = append(runtimes, runtime)
	}
	sort.Strings(runtimes)
	return runtimes, minimal, nil
}
