package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

type RuntimeUpgradeIngressGeneration struct{ PublicRevision, GatewayRevision string }

type RuntimeUpgradePublicIngressBinding struct {
	RuntimeUpgradeIngressGeneration
	RuntimeUpgradeIngressBinding
}

type RuntimeUpgradePublicEdgeActivity struct {
	Version                    int64
	Known                      bool
	Pending, Current, Previous int
}

type RuntimeUpgradePublicEdgeActivityObservation struct {
	RuntimeUpgradePublicEdgeObservation
	Pending, Current, Previous int
}

// Snapshot must synchronously read the process tracker, without database IO.
// The store invokes it only after fencing both current heads and the fact row.
type RuntimeUpgradePublicEdgeActivityStore interface {
	AuthorizeRuntimeUpgradePublicEdgeIngress(context.Context, RuntimeUpgradePublicEdgeMember, string, string) (RuntimeUpgradePublicIngressBinding, error)
	RecordRuntimeUpgradePublicEdgeActivity(context.Context, RuntimeUpgradePublicEdgeMember, func(RuntimeUpgradeIngressGeneration) RuntimeUpgradePublicEdgeActivity) error
}

type RuntimeUpgradePublicEdgeActivityVerifier interface {
	ObserveRuntimeUpgradePublicEdgeActivity(context.Context, string) (RuntimeUpgradePublicEdgeActivityObservation, error)
}

func (a RuntimeUpgradePublicEdgeActivity) valid() bool {
	return a.Version > 0 && a.Pending >= 0 && a.Current >= 0 && a.Previous >= 0 &&
		a.Pending <= api.RuntimeUpgradeActivityForwardLimit && a.Current <= api.RuntimeUpgradeActivityForwardLimit && a.Previous <= api.RuntimeUpgradeActivityForwardLimit &&
		a.Pending+a.Current+a.Previous <= api.RuntimeUpgradeActivityForwardLimit
}
