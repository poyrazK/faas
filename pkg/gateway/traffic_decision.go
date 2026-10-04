// adr: 531
package gateway

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/reqbudget"
	"github.com/onebox-faas/faas/pkg/trafficrevocation"
	"go.opentelemetry.io/otel/attribute"
)

type trafficDecisionKey struct{}
type trafficPhase uint8

const (
	trafficOwnership trafficPhase = iota
	trafficIdentity
	trafficPolicy
	trafficAuthentication
	trafficValidation
	trafficRates
	trafficCache
	trafficBody
	trafficWake
	trafficCapacity
	trafficForward
	trafficBackoff
	trafficResponse
	trafficPhaseCount
)

func (phase trafficPhase) String() string {
	switch phase {
	case trafficOwnership:
		return "ownership"
	case trafficIdentity:
		return "identity"
	case trafficPolicy:
		return "policy"
	case trafficAuthentication:
		return "authentication"
	case trafficValidation:
		return "validation"
	case trafficRates:
		return "rates"
	case trafficCache:
		return "cache"
	case trafficBody:
		return "body"
	case trafficWake:
		return "wake"
	case trafficCapacity:
		return "capacity"
	case trafficForward:
		return "forward"
	case trafficBackoff:
		return "backoff"
	case trafficResponse:
		return "response"
	default:
		return "unknown"
	}
}

// Fixed scalars bound both retained state and exported evidence. No customer
// text, event list, target roster or request body enters this record.
type trafficDecisionSnapshot struct {
	path, outcome, refusal, retryStop, limiterScope, cache, circuit string
	phase                                                           trafficPhase
	attempts                                                        int64
	measured                                                        uint16
	durations                                                       [trafficPhaseCount]int64
	streamDetached                                                  bool
	edgeResponse                                                    bool
}

type trafficDecision struct {
	sync.Mutex
	trafficDecisionSnapshot
	sealed bool
	active [trafficPhaseCount]trafficPhaseTimer
}

type trafficPhaseTimer struct {
	started time.Time
	depth   int64
}

func withTrafficDecision(ctx context.Context, service bool) context.Context {
	value := &trafficDecision{trafficDecisionSnapshot: trafficDecisionSnapshot{path: "public_http", cache: "not_consulted", circuit: "not_observed"}}
	if service {
		value.path, value.phase = "managed_service", trafficIdentity
	}
	return context.WithValue(ctx, trafficDecisionKey{}, value)
}

func withoutTrafficDecision(ctx context.Context) context.Context {
	return context.WithValue(ctx, trafficDecisionKey{}, (*trafficDecision)(nil))
}

func trafficDecisionFrom(ctx context.Context) *trafficDecision {
	if ctx == nil {
		return nil
	}
	value, _ := ctx.Value(trafficDecisionKey{}).(*trafficDecision)
	return value
}

func mutateTrafficDecision(ctx context.Context, update func(*trafficDecisionSnapshot)) {
	if value := trafficDecisionFrom(ctx); value != nil {
		value.Lock()
		defer value.Unlock()
		if !value.sealed {
			update(&value.trafficDecisionSnapshot)
		}
	}
}

func markTrafficPhase(ctx context.Context, phase trafficPhase) {
	mutateTrafficDecision(ctx, func(value *trafficDecisionSnapshot) { value.phase = phase })
}

// Stop only measures caller wait/work. Detached wake leaders retain their
// own lifecycle and do not revise this request's wait when they finish.
func measureTrafficPhase(ctx context.Context, phase trafficPhase) func() {
	markTrafficPhase(ctx, phase)
	value := trafficDecisionFrom(ctx)
	if value == nil || phase >= trafficPhaseCount {
		return func() {}
	}
	value.Lock()
	if value.sealed || value.active[phase].depth == math.MaxInt64 {
		value.Unlock()
		return func() {}
	}
	timer := &value.active[phase]
	if timer.depth == 0 {
		timer.started = time.Now()
	}
	timer.depth++
	value.Unlock()
	var stopped atomic.Bool
	return func() {
		if stopped.Swap(true) {
			return
		}
		value.Lock()
		defer value.Unlock()
		if value.sealed {
			return
		}
		timer.depth--
		if timer.depth == 0 {
			value.addDuration(phase, time.Since(timer.started))
			timer.started = time.Time{}
		}
	}
}

func (value *trafficDecisionSnapshot) addDuration(phase trafficPhase, elapsed time.Duration) {
	value.measured |= 1 << phase
	value.durations[phase] += min(max(0, elapsed.Nanoseconds()), math.MaxInt64-value.durations[phase])
}

func recordTrafficAttempt(ctx context.Context) {
	mutateTrafficDecision(ctx, func(value *trafficDecisionSnapshot) {
		value.phase = trafficForward
		if value.attempts < math.MaxInt64 {
			value.attempts++
		}
	})
}

func recordTrafficStreamDetached(ctx context.Context) {
	mutateTrafficDecision(ctx, func(value *trafficDecisionSnapshot) { value.streamDetached = true })
}

func recordTrafficRetryStop(ctx context.Context, reason string) {
	switch reason {
	case "", RetrySkipCommitted, RetrySkipNonIdempotent, RetrySkipNoTarget, RetrySkipBudget,
		RetrySkipAttempts, RetrySkipBodyNotReplay, RetrySkipIdempotency, RetrySkipAggregate:
	default:
		reason = "other"
	}
	mutateTrafficDecision(ctx, func(value *trafficDecisionSnapshot) {
		// A safety or aggregate-budget gate may reduce the loop to one
		// attempt. Retain that reason when the original later reports stale.
		if value.retryStop == "" {
			value.retryStop = reason
		}
	})
}

func recordTrafficRefusal(ctx context.Context, reason string) {
	switch reason {
	case "deadline", "rate_limited", "rate_limit_unavailable", "security_revoked", "security_unavailable", "policy_unavailable", "capacity", "identity", "authorization", "circuit":
	default:
		reason = "other"
	}
	mutateTrafficDecision(ctx, func(value *trafficDecisionSnapshot) { value.refusal = reason })
}

func recordTrafficLimiter(ctx context.Context, scope string) {
	switch scope {
	case "account", "app", "tenant", "surface", "consumer", "rule":
	default:
		scope = "other"
	}
	mutateTrafficDecision(ctx, func(value *trafficDecisionSnapshot) { value.limiterScope = scope })
}

func recordTrafficCache(ctx context.Context, outcome string) {
	switch outcome {
	case "hit", "miss", "bypass_authed", "bypass_uncacheable", "stale_while_revalidate_served", "stale_if_error_served", "stale_while_waking":
	default:
		outcome = "other"
	}
	mutateTrafficDecision(ctx, func(value *trafficDecisionSnapshot) {
		value.cache = outcome
		switch outcome {
		case "hit", "stale_while_revalidate_served", "stale_if_error_served", "stale_while_waking":
			value.edgeResponse, value.phase = true, trafficCache
		}
	})
}

func recordTrafficEdgeResponse(ctx context.Context) {
	mutateTrafficDecision(ctx, func(value *trafficDecisionSnapshot) {
		value.edgeResponse, value.phase = true, trafficResponse
	})
}

func recordTrafficCircuit(ctx context.Context, admitted bool) {
	mutateTrafficDecision(ctx, func(value *trafficDecisionSnapshot) {
		value.circuit = "refused"
		if admitted {
			value.circuit = "admitted"
		}
	})
}

func trafficDecisionEvidence(ctx context.Context, status int, seal bool) trafficDecisionSnapshot {
	value := trafficDecisionFrom(ctx)
	if value == nil {
		return trafficDecisionSnapshot{}
	}
	value.Lock()
	defer value.Unlock()
	if value.sealed {
		return value.trafficDecisionSnapshot
	}
	result := value.trafficDecisionSnapshot
	for phase, timer := range value.active {
		if timer.depth > 0 {
			result.addDuration(trafficPhase(phase), time.Since(timer.started))
		}
	}
	if cause := trafficRevocationCause(ctx); cause != nil {
		result.refusal = trafficSecurityDecision(cause)
	}
	err := ctx.Err()
	if result.streamDetached {
		err = reqbudget.CancellationFenceCause(ctx)
	}
	switch {
	case result.refusal == "deadline":
		result.outcome, result.refusal = "deadline", "deadline"
	case result.refusal != "":
		result.outcome = "refused"
	// A socket or derived transport timer can finish before ctx.Err is
	// visible. Ordinary exchanges retain the budget's wall-clock verdict.
	case errors.Is(err, context.DeadlineExceeded) || !result.streamDetached && requestBudgetExpired(ctx):
		result.outcome, result.refusal = "deadline", "deadline"
	case err != nil:
		result.outcome = "canceled"
	case result.attempts > 0:
		result.outcome = "upstream_response"
	case result.edgeResponse:
		result.outcome = "edge_response"
	case status >= 400:
		result.outcome, result.refusal = "refused", result.phase.String()+"_response"
	default:
		result.outcome = "edge_response"
	}
	if seal {
		value.trafficDecisionSnapshot, value.sealed = result, true
	}
	return result
}

func trafficSecurityDecision(err error) string {
	if errors.Is(err, trafficrevocation.ErrRevoked) {
		return "security_revoked"
	}
	return "security_unavailable"
}

func (value trafficDecisionSnapshot) attributes() []attribute.KeyValue {
	if value.path == "" {
		return nil
	}
	return []attribute.KeyValue{
		attribute.String("gregale.traffic.decision_version", "v1"),
		attribute.String("gregale.traffic.path", value.path),
		attribute.String("gregale.traffic.phase", value.phase.String()),
		attribute.String("gregale.traffic.outcome", value.outcome),
		attribute.String("gregale.traffic.rejection_reason", value.refusal),
		attribute.Int64("gregale.traffic.forward_attempts", value.attempts),
		attribute.Int64("gregale.traffic.replays", max(0, value.attempts-1)),
		attribute.String("gregale.traffic.retry_stop", value.retryStop),
		attribute.String("gregale.traffic.limiter_scope", value.limiterScope),
		attribute.String("gregale.traffic.cache_outcome", value.cache),
		attribute.String("gregale.traffic.circuit_verdict", value.circuit),
		attribute.String("gregale.traffic.measured_phases", value.measuredPhases()),
		attribute.Int64("gregale.traffic.policy_read_ms", value.durations[trafficPolicy]/int64(time.Millisecond)),
		attribute.Int64("gregale.traffic.body_admission_ms", value.durations[trafficBody]/int64(time.Millisecond)),
		attribute.Int64("gregale.traffic.wake_admission_ms", value.durations[trafficWake]/int64(time.Millisecond)),
		attribute.Int64("gregale.traffic.capacity_wait_ms", value.durations[trafficCapacity]/int64(time.Millisecond)),
		attribute.Int64("gregale.traffic.retry_backoff_ms", value.durations[trafficBackoff]/int64(time.Millisecond)),
	}
}

func (value trafficDecisionSnapshot) measuredPhases() string {
	var phases []string
	for _, phase := range []trafficPhase{trafficPolicy, trafficBody, trafficWake, trafficCapacity, trafficBackoff} {
		if value.measured&(1<<phase) != 0 {
			phases = append(phases, phase.String())
		}
	}
	return strings.Join(phases, ",")
}

func (value trafficDecisionSnapshot) logAttribute() slog.Attr {
	attributes := value.attributes()
	fields := make([]slog.Attr, 0, len(attributes))
	for _, attr := range attributes {
		key := strings.TrimPrefix(string(attr.Key), "gregale.traffic.")
		if attr.Value.Type() == attribute.STRING {
			fields = append(fields, slog.String(key, attr.Value.AsString()))
		} else {
			fields = append(fields, slog.Int64(key, attr.Value.AsInt64()))
		}
	}
	return slog.Attr{Key: "traffic_decision", Value: slog.GroupValue(fields...)}
}
