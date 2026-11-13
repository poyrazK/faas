package state

import (
	"context"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// ServiceRolloutStepStore advances bounded service rollouts without requiring
// room for two complete cohorts. Replica withdrawals are durable and remain
// charged until the scheduler has acknowledged routes and drained requests.
type ServiceRolloutStepStore interface {
	BeginServiceRolloutStep(context.Context, string, int) (Deployment, error)
	ClaimServiceRolloutReplicaDrains(context.Context, string, int) (Deployment, error)
	CompleteServiceRolloutReplicaDrains(context.Context, string, []string) error
}

type serviceRolloutTrafficKey struct{}

func serviceRolloutTraffic(ctx context.Context) int {
	if percent, ok := ctx.Value(serviceRolloutTrafficKey{}).(int); ok {
		return percent
	}
	return 100
}

func serviceBindingTraffic(gate *api.ServiceRolloutBindingGate) int {
	if gate != nil && gate.TrafficPercent > 0 {
		return gate.TrafficPercent
	}
	return 100 // Existing persisted grants authorize the complete cutover.
}

// ServiceRolloutReadyTarget rounds candidate capacity up. The predecessor's
// complementary allocation rounds down, retaining at least one ready replica
// until the final cutover. This also handles percentages such as 33/67.
func ServiceRolloutReadyTarget(desired, percent int) int {
	return max(0, (desired*percent+99)/100)
}

func ServiceRolloutPredecessorTarget(desired, percent int) int {
	return max(1, desired*(100-percent)/100)
}

func (s *PgStore) BeginServiceRolloutStep(ctx context.Context, id string, percent int) (Deployment, error) {
	if percent <= 0 || percent >= 100 {
		return Deployment{}, ErrServiceRolloutInvalid
	}
	return s.BeginServiceRolloutCutover(context.WithValue(ctx, serviceRolloutTrafficKey{}, percent), id)
}

func (m *MemStore) BeginServiceRolloutStep(ctx context.Context, id string, percent int) (Deployment, error) {
	if percent <= 0 || percent >= 100 {
		return Deployment{}, ErrServiceRolloutInvalid
	}
	return m.BeginServiceRolloutCutover(context.WithValue(ctx, serviceRolloutTrafficKey{}, percent), id)
}

func serviceRolloutDrainClaimValid(target Deployment, percent int) bool {
	return percent > 0 && percent < 100 && target.TrafficPercent == percent &&
		target.ServiceRolloutHandoff.Action == ServiceRolloutActionPromote &&
		!target.ServiceRolloutHandoff.ActiveAbort() && target.ServiceRolloutHandoff.PredecessorDeploymentID != ""
}

func selectServiceRolloutDrains(ready []string, desired, percent int) []string {
	sort.Strings(ready)
	return ready[:max(0, len(ready)-ServiceRolloutPredecessorTarget(desired, percent))]
}
