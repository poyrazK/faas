package outbound

import (
	"context"
	"hash/fnv"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// RouteCanaryHeader marks a route call that was sent to the route's canary
// target first (ADR-1340).
const RouteCanaryHeader = "X-Gregale-AI-Route-Canary"

// AIRouteCanary is a route's active model canary: the 1-based target
// position tried first by Percent of the route's calls (ADR-1340). A
// rolled-back canary is not loaded.
type AIRouteCanary struct {
	Position int
	Percent  int
}

// routeAttempt is one route attempt's place in its route. tracked attempts
// add to the canary's per-minute target rows at settlement; canary marks
// the canary target's attempts.
type routeAttempt struct {
	routeID  string
	position int
	tracked  bool
	canary   bool
}

type routeAttemptContextKey struct{}

func withRouteAttempt(ctx context.Context, attempt routeAttempt) context.Context {
	return context.WithValue(ctx, routeAttemptContextKey{}, attempt)
}

func routeAttemptFromContext(ctx context.Context) routeAttempt {
	attempt, _ := ctx.Value(routeAttemptContextKey{}).(routeAttempt)
	return attempt
}

// routeStep is one target with its 1-based position in the route.
type routeStep struct {
	position int
	target   AIRouteTarget
}

// chooseRouteCanary decides whether a call goes to the canary first. A call
// naming a session, or else an end user, always gets the same answer for
// the route, so one conversation stays on one model; other calls are chosen
// at random.
func chooseRouteCanary(route AIRoute, r *http.Request) bool {
	if route.Canary == nil || route.Canary.Position < 2 || route.Canary.Position > len(route.Targets) {
		return false
	}
	key := r.Header.Get(api.OutboundAISessionHeader)
	if key == "" {
		key = r.Header.Get(api.OutboundAIUserHeader)
	}
	if key == "" {
		return rand.IntN(100) < route.Canary.Percent //nolint:gosec // traffic splitting, not security.
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(route.ID + "\x00" + key))
	return int(hash.Sum64()%100) < route.Canary.Percent
}

// AIRouteCanaryJudgement is one active canary with its first target's and
// its own counts over its window.
type AIRouteCanaryJudgement struct {
	RouteID                   string
	Position                  int
	MaxErrorRateIncrease      int
	MaxLatencyIncreasePercent *int
	MinCalls                  int
	Baseline, Canary          AIRouteTargetCounts
}

// AIRouteTargetCounts sums one target's settled attempts. CostMillicents
// is their cost, which cost routes rank by (ADR-1434).
type AIRouteTargetCounts struct {
	Calls, Errors, TTFBCalls, TTFBMSTotal int64
	CostMillicents                        int64
}

// rollbackReason returns why a canary must be rolled back, or "". It waits
// for MinCalls canary attempts. The error margin is in percentage points
// of the error rates; the latency margin compares mean time to first byte
// of successful attempts and needs both targets to have one.
func (j AIRouteCanaryJudgement) rollbackReason() string {
	canary, baseline := j.Canary, j.Baseline
	if canary.Calls < int64(j.MinCalls) {
		return ""
	}
	// canaryErrors/canaryCalls - baselineErrors/baselineCalls > margin/100,
	// in integers.
	baselineCalls := max(baseline.Calls, 1)
	if 100*canary.Errors*baselineCalls-100*baseline.Errors*canary.Calls > int64(j.MaxErrorRateIncrease)*canary.Calls*baselineCalls {
		return api.OutboundAIRouteCanaryReasonErrorRate
	}
	if j.MaxLatencyIncreasePercent != nil && canary.TTFBCalls > 0 && baseline.TTFBCalls > 0 {
		// canaryMean > baselineMean * (1 + margin/100)
		if 100*canary.TTFBMSTotal*baseline.TTFBCalls > (100+int64(*j.MaxLatencyIncreasePercent))*baseline.TTFBMSTotal*canary.TTFBCalls {
			return api.OutboundAIRouteCanaryReasonLatency
		}
	}
	return ""
}

// AIRouteCanaryStore reads active canaries and rolls them back.
type AIRouteCanaryStore interface {
	ActiveAIRouteCanaries(ctx context.Context) ([]AIRouteCanaryJudgement, error)
	// RollBackAIRouteCanary rolls back the canary at position if it is
	// still active there, and reports whether it did.
	RollBackAIRouteCanary(ctx context.Context, routeID string, position int, reason string) (bool, error)
}

// AIRouteCanaryEvaluator judges active canaries on an interval (ADR-1340).
// Every outboundd replica may run it: a rollback applies only to a canary
// that is still active.
type AIRouteCanaryEvaluator struct {
	Store    AIRouteCanaryStore
	Metrics  *Metrics
	Interval time.Duration
	Log      *slog.Logger
}

// Run evaluates until ctx ends.
func (e *AIRouteCanaryEvaluator) Run(ctx context.Context) {
	interval := e.Interval
	if interval <= 0 {
		interval = api.OutboundAIRouteCanaryEvaluateIntervalSeconds * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := e.EvaluateOnce(ctx); err != nil && ctx.Err() == nil && e.Log != nil {
			e.Log.Warn("outboundd: AI route canaries", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// EvaluateOnce judges every active canary once and returns how many it
// rolled back.
func (e *AIRouteCanaryEvaluator) EvaluateOnce(ctx context.Context) (int, error) {
	judgements, err := e.Store.ActiveAIRouteCanaries(ctx)
	if err != nil {
		return 0, err
	}
	rolledBack := 0
	for _, judgement := range judgements {
		reason := judgement.rollbackReason()
		if reason == "" {
			continue
		}
		done, err := e.Store.RollBackAIRouteCanary(ctx, judgement.RouteID, judgement.Position, reason)
		if err != nil {
			return rolledBack, err
		}
		if done {
			rolledBack++
			e.Metrics.ObserveAIRouteCanaryRollback(reason)
			if e.Log != nil {
				e.Log.Info("outboundd: AI route canary rolled back", "route_id", judgement.RouteID, "position", judgement.Position, "reason", reason)
			}
		}
	}
	return rolledBack, nil
}
