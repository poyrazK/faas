package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// ErrOutboundAIRouteLimit reports an account at its AI route ceiling.
var ErrOutboundAIRouteLimit = errors.New("state: outbound AI route limit reached")

// OutboundAIRouteTarget is one ordered fallback step (ADR-983).
type OutboundAIRouteTarget struct {
	IntegrationID string
	Model         string
}

// OutboundAIRoute is an account-owned ordered list of same-format managed AI
// integrations. outboundd reads it; apid owns its lifecycle.
type OutboundAIRoute struct {
	ID        string
	AccountID string
	Name      string
	Targets   []OutboundAIRouteTarget
	CreatedAt time.Time
	UpdatedAt time.Time
}

// OutboundAIRouteStore owns customer AI fallback routes.
type OutboundAIRouteStore interface {
	ListOutboundAIRoutes(ctx context.Context, accountID string) ([]OutboundAIRoute, error)
	GetOutboundAIRoute(ctx context.Context, accountID, routeID string) (OutboundAIRoute, error)
	CreateOutboundAIRoute(ctx context.Context, route OutboundAIRoute) (OutboundAIRoute, error)
	SetOutboundAIRouteTargets(ctx context.Context, accountID, routeID string, targets []OutboundAIRouteTarget) error
	DeleteOutboundAIRoute(ctx context.Context, accountID, routeID string) error
}

func validOutboundAIRouteShape(targets []OutboundAIRouteTarget) bool {
	wire := make([]api.OutboundAIRouteTarget, 0, len(targets))
	for _, target := range targets {
		if _, err := uuid.Parse(target.IntegrationID); err != nil {
			return false
		}
		wire = append(wire, api.OutboundAIRouteTarget{IntegrationID: target.IntegrationID, Model: target.Model})
	}
	return api.ValidOutboundAIRouteTargetShape(wire)
}

// sameOutboundAIProvider checks that every target is a known AI integration
// of one wire format. providers maps integration ID to its ai_provider.
func sameOutboundAIProvider(targets []OutboundAIRouteTarget, providers map[string]string) bool {
	first := ""
	for _, target := range targets {
		provider, ok := providers[target.IntegrationID]
		if !ok || provider == "" || (first != "" && provider != first) {
			return false
		}
		first = provider
	}
	return true
}

func copyOutboundAIRoute(in OutboundAIRoute) OutboundAIRoute {
	in.Targets = append([]OutboundAIRouteTarget(nil), in.Targets...)
	return in
}
