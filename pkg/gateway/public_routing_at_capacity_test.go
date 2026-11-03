// adr: 570
package gateway

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// atCapacityBackend answers every admission at capacity. A non-empty replica
// stands for the target the at-capacity rehydrate finds, such as the instance
// a deployment smoke admitted before the deployment went live.
type atCapacityBackend struct {
	*fakeBackend
	replica *Target
}

func (b *atCapacityBackend) Admit(context.Context, string, string, string, string, int) (string, WakeMethod, bool, error) {
	if b.replica != nil {
		b.AddTarget(*b.replica)
	}
	return "", WakeMethodUnspecified, true, nil
}

func TestPublicRoutingWakeServesReplicaFoundAtCapacity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		replica  bool
		liveSlot bool
		want     string
	}{
		{name: "rehydrate finds the replica", replica: true},
		// #5709: nothing this gateway can see holds the slot, so it is not
		// the customer's limit (ADR-20261027173012).
		{name: "no replica and no visible instance", want: "settling"},
		{name: "another deployment's live instance holds the slot", liveSlot: true, want: api.CodeAppConcurReached},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, backend, _ := newTestHandler(t)
			backend.app.MaxConcurrency = 1
			deployment := uuid.NewString()
			wrapped := &atCapacityBackend{fakeBackend: backend}
			if tc.replica {
				wrapped.replica = &Target{InstanceID: "smoke", NodeID: "node", DeploymentID: deployment}
			}
			if tc.liveSlot {
				backend.AddTarget(Target{InstanceID: "live", NodeID: "node", DeploymentID: uuid.NewString()})
			}
			h.backend = wrapped
			_, _, _, err := h.wakeDeployment(t.Context(), backend.app, deployment, "production", "gateway", 1)
			var problem *api.Problem
			switch tc.want {
			case "":
				if err != nil {
					t.Fatalf("replica found at capacity was refused: %v", err)
				}
			case "settling":
				if !errors.Is(err, ErrInstanceSlotsSettling) {
					t.Fatalf("at capacity with nothing visible: got %v, want %v", err, ErrInstanceSlotsSettling)
				}
			default:
				if !errors.As(err, &problem) || problem.Code != tc.want || problem.Observed == nil || *problem.Observed != 1 {
					t.Fatalf("at capacity with a live slot: got %v, want %s observing 1", err, tc.want)
				}
			}
		})
	}
}
