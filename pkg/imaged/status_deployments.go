package imaged

import (
	"context"
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/state"
)

// statusDeploymentLister is the narrowed deployment listing PgStore and
// MemStore provide. The security and evidence sweeps act only on serving
// deployments, so they ask for those instead of the whole history.
type statusDeploymentLister interface {
	ListDeploymentsWithStatus(ctx context.Context, statuses ...state.DeploymentStatus) ([]state.Deployment, error)
}

// nodeStatusDeploymentLister narrows that listing to one node's apps (bug
// hunt O32): every node's imaged runs the sweeps, so a fleet-wide listing
// read, and rescanned, every serving deployment once per node.
type nodeStatusDeploymentLister interface {
	ListDeploymentsWithStatusForNode(ctx context.Context, nodeID string, statuses ...state.DeploymentStatus) ([]state.Deployment, error)
}

type computeNodeByNameStore interface {
	ComputeNodeByName(context.Context, string) (state.ComputeNode, error)
}

// deploymentsWithStatus returns the deployments of listed apps in one of
// statuses. A named imaged gets only its own node's apps (and those whose
// owner is not active); a store without the narrowed listing gets the same
// rows from ListAllDeployments.
func (l *Loop) deploymentsWithStatus(ctx context.Context, statuses ...state.DeploymentStatus) ([]state.Deployment, error) {
	if lister, ok := l.store.(nodeStatusDeploymentLister); ok && l.handler != nil && l.handler.nodeName != "" {
		nodeID, err := l.sweepNodeID(ctx)
		if err != nil {
			return nil, err
		}
		return lister.ListDeploymentsWithStatusForNode(ctx, nodeID, statuses...)
	}
	if lister, ok := l.store.(statusDeploymentLister); ok {
		return lister.ListDeploymentsWithStatus(ctx, statuses...)
	}
	deployments, err := l.store.ListAllDeployments(ctx)
	if err != nil {
		return nil, err
	}
	out := deployments[:0]
	for _, dep := range deployments {
		if slices.Contains(statuses, dep.Status) {
			out = append(out, dep)
		}
	}
	return out, nil
}

// sweepNodeID resolves this imaged's compute node once. A node's ID never
// changes, so only a failed lookup is retried.
func (l *Loop) sweepNodeID(ctx context.Context) (string, error) {
	l.sweepNodeMu.Lock()
	defer l.sweepNodeMu.Unlock()
	if l.sweepNode != "" {
		return l.sweepNode, nil
	}
	nodes, ok := l.store.(computeNodeByNameStore)
	if !ok {
		return "", fmt.Errorf("imaged: store cannot resolve node %q for the sweeps", l.handler.nodeName)
	}
	node, err := nodes.ComputeNodeByName(ctx, l.handler.nodeName)
	if err != nil {
		return "", fmt.Errorf("imaged: resolve node %q for the sweeps: %w", l.handler.nodeName, err)
	}
	l.sweepNode = node.ID
	return node.ID, nil
}
