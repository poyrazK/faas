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
	// Format is "native" or "openai" (ADR-984).
	Format    string
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

// normalizeOutboundAIRouteFormat defaults an empty format to native.
func normalizeOutboundAIRouteFormat(format string) (string, bool) {
	if format == "" {
		return api.OutboundAIRouteFormatNative, true
	}
	return format, api.ValidOutboundAIRouteFormat(format)
}

// outboundAIRouteTargetsAllowed checks that every target is a known AI
// integration the route format can serve. A native route needs one wire
// format; an openai route accepts translatable providers and needs a model
// for every non-OpenAI target (ADR-984). providers maps integration ID to
// its ai_provider.
func outboundAIRouteTargetsAllowed(format string, targets []OutboundAIRouteTarget, providers map[string]string) bool {
	first := ""
	for _, target := range targets {
		provider, ok := providers[target.IntegrationID]
		if !ok || provider == "" {
			return false
		}
		allowed, needsModel := api.OutboundAIRouteFormatTargetProvider(format, provider)
		if !allowed || (needsModel && target.Model == "") {
			return false
		}
		if format != api.OutboundAIRouteFormatOpenAI && first != "" && provider != first {
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
