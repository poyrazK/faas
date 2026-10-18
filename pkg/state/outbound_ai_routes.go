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

// OutboundAIRouteTarget is one ordered fallback step (ADR-990).
type OutboundAIRouteTarget struct {
	IntegrationID string
	Model         string
	// Weight is the target's weight on a weighted route (ADR-1434).
	Weight *int
}

// OutboundAIRouteRouting is a route's strategy and a cost route's latency
// target (ADR-1434).
type OutboundAIRouteRouting struct {
	Strategy  string
	MaxTTFBMS *int
}

// OutboundAIRoute is an account-owned ordered list of same-format managed AI
// integrations. outboundd reads it; apid owns its lifecycle.
type OutboundAIRoute struct {
	ID        string
	AccountID string
	Name      string
	// Format is "native" or "openai" (ADR-991).
	Format    string
	Targets   []OutboundAIRouteTarget
	CreatedAt time.Time
	UpdatedAt time.Time
	// Canary is the route's model canary, if any (ADR-1340).
	Canary *OutboundAIRouteCanary
	// Strategy and MaxTTFBMS pick the target each call tries first
	// (ADR-1434).
	Strategy  string
	MaxTTFBMS *int
}

// OutboundAIRouteCanary sends a share of a route's calls to the target at
// Position first. outboundd rolls it back; apid starts, promotes and stops
// it (ADR-1340).
type OutboundAIRouteCanary struct {
	Position                  int
	Percent                   int
	MaxErrorRateIncrease      int
	MaxLatencyIncreasePercent *int
	MinCalls                  int
	WindowMinutes             int
	State                     string
	Reason                    string
	StartedAt                 time.Time
	DecidedAt                 *time.Time
}

// OutboundAIRouteTargetStats sums one target's settled attempts.
type OutboundAIRouteTargetStats struct {
	Position       int
	Calls          int64
	Errors         int64
	TTFBCalls      int64
	TTFBMSTotal    int64
	CostMillicents int64
}

// OutboundAIRouteStatsWindow is each target's settled attempts since
// WindowStart (ADR-1434).
type OutboundAIRouteStatsWindow struct {
	Strategy    string
	WindowStart time.Time
	Targets     []OutboundAIRouteTargetStats
}

// OutboundAIRouteCanaryStatus is a canary with its first target's and its
// own counts since WindowStart.
type OutboundAIRouteCanaryStatus struct {
	Canary      OutboundAIRouteCanary
	WindowStart time.Time
	Baseline    OutboundAIRouteTargetStats
	CanaryStats OutboundAIRouteTargetStats
}

// OutboundAIRouteStore owns customer AI fallback routes and their canaries.
type OutboundAIRouteStore interface {
	ListOutboundAIRoutes(ctx context.Context, accountID string) ([]OutboundAIRoute, error)
	GetOutboundAIRoute(ctx context.Context, accountID, routeID string) (OutboundAIRoute, error)
	CreateOutboundAIRoute(ctx context.Context, route OutboundAIRoute) (OutboundAIRoute, error)
	// SetOutboundAIRouteTargets replaces the targets and the strategy, ends
	// any canary and clears the per-target counts.
	SetOutboundAIRouteTargets(ctx context.Context, accountID, routeID string, targets []OutboundAIRouteTarget,
		routing OutboundAIRouteRouting) error
	DeleteOutboundAIRoute(ctx context.Context, accountID, routeID string) error
	// SetOutboundAIRouteCanary starts or replaces the canary and clears its
	// counts. The position must name a target after the first.
	SetOutboundAIRouteCanary(ctx context.Context, accountID, routeID string, canary OutboundAIRouteCanary) (OutboundAIRouteCanary, error)
	DeleteOutboundAIRouteCanary(ctx context.Context, accountID, routeID string) error
	// PromoteOutboundAIRouteCanary moves the canary target to the front and
	// ends the canary.
	PromoteOutboundAIRouteCanary(ctx context.Context, accountID, routeID string) (OutboundAIRoute, error)
	OutboundAIRouteCanaryStatus(ctx context.Context, accountID, routeID string) (OutboundAIRouteCanaryStatus, error)
	// OutboundAIRouteStats returns each target's counts over the strategy
	// window (ADR-1434).
	OutboundAIRouteStats(ctx context.Context, accountID, routeID string) (OutboundAIRouteStatsWindow, error)
}

// validOutboundAIRouteCanary checks a canary's settings against the route's
// target count. The database enforces the same ranges.
func validOutboundAIRouteCanary(canary OutboundAIRouteCanary, targets int) bool {
	request := api.PutOutboundAIRouteCanaryRequest{
		Target: canary.Position, Percent: canary.Percent, MaxErrorRateIncrease: &canary.MaxErrorRateIncrease,
		MaxLatencyIncreasePercent: canary.MaxLatencyIncreasePercent, MinCalls: &canary.MinCalls, WindowMinutes: &canary.WindowMinutes,
	}
	return request.Valid() && canary.Position <= targets
}

// promotedOutboundAIRouteTargets moves the target at position (1-based) to
// the front and keeps the others in order.
func promotedOutboundAIRouteTargets(targets []OutboundAIRouteTarget, position int) []OutboundAIRouteTarget {
	out := make([]OutboundAIRouteTarget, 0, len(targets))
	out = append(out, targets[position-1])
	for index, target := range targets {
		if index != position-1 {
			out = append(out, target)
		}
	}
	return out
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

// normalizeOutboundAIRouteRouting defaults an empty strategy to ordered and
// checks it with the targets' weights (ADR-1434).
func normalizeOutboundAIRouteRouting(routing OutboundAIRouteRouting, targets []OutboundAIRouteTarget) (OutboundAIRouteRouting, bool) {
	if routing.Strategy == "" {
		routing.Strategy = api.OutboundAIRouteStrategyOrdered
	}
	wire := make([]api.OutboundAIRouteTarget, 0, len(targets))
	for _, target := range targets {
		wire = append(wire, api.OutboundAIRouteTarget{IntegrationID: target.IntegrationID, Weight: target.Weight})
	}
	return routing, api.ValidOutboundAIRouteStrategy(routing.Strategy, routing.MaxTTFBMS, wire)
}

// normalizeOutboundAIRouteFormat defaults an empty format to native.
func normalizeOutboundAIRouteFormat(format string) (string, bool) {
	if format == "" {
		return api.OutboundAIRouteFormatNative, true
	}
	return format, api.ValidOutboundAIRouteFormat(format)
}

// outboundAIRouteTarget is what route validation needs to know about one
// target integration.
type outboundAIRouteTarget struct {
	provider string
	// pathPrefixes are the integration's allowed path prefixes; a Vertex
	// target of an openai route needs one project and location in them.
	pathPrefixes []string
}

// outboundAIRouteTargetsAllowed checks that every target is a known AI
// integration the route format can serve. A native route needs one wire
// format; an openai route accepts translatable providers and needs a model
// for every non-OpenAI target (ADR-991), and a Vertex target whose prefixes
// name one project and location (ADR-995). integrations maps integration ID
// to its details.
func outboundAIRouteTargetsAllowed(format string, targets []OutboundAIRouteTarget, integrations map[string]outboundAIRouteTarget) bool {
	first := ""
	for _, target := range targets {
		integration, ok := integrations[target.IntegrationID]
		provider := integration.provider
		if !ok || provider == "" {
			return false
		}
		allowed, needsModel := api.OutboundAIRouteFormatTargetProvider(format, provider)
		if !allowed || (needsModel && target.Model == "") {
			return false
		}
		if format == api.OutboundAIRouteFormatOpenAI && provider == api.OutboundAIProviderVertex {
			if _, ok := api.OutboundVertexBasePath(integration.pathPrefixes); !ok {
				return false
			}
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
	for index, target := range in.Targets {
		if target.Weight != nil {
			weight := *target.Weight
			in.Targets[index].Weight = &weight
		}
	}
	if in.MaxTTFBMS != nil {
		maxTTFB := *in.MaxTTFBMS
		in.MaxTTFBMS = &maxTTFB
	}
	if in.Canary != nil {
		canary := *in.Canary
		if canary.MaxLatencyIncreasePercent != nil {
			latency := *canary.MaxLatencyIncreasePercent
			canary.MaxLatencyIncreasePercent = &latency
		}
		if canary.DecidedAt != nil {
			decided := *canary.DecidedAt
			canary.DecidedAt = &decided
		}
		in.Canary = &canary
	}
	return in
}
