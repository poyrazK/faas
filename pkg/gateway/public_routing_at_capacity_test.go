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
		name    string
		replica bool
	}{
		{"rehydrate finds the replica", true},
		{"no replica", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, backend, _ := newTestHandler(t)
			deployment := uuid.NewString()
			wrapped := &atCapacityBackend{fakeBackend: backend}
			if tc.replica {
				wrapped.replica = &Target{InstanceID: "smoke", NodeID: "node", DeploymentID: deployment}
			}
			h.backend = wrapped
			_, _, _, err := h.wakeDeployment(t.Context(), backend.app, deployment, "production", "gateway", 1)
			var problem *api.Problem
			atCapacity := errors.As(err, &problem) && problem.Code == api.CodeAppConcurReached
			if tc.replica && err != nil {
				t.Fatalf("replica found at capacity was refused: %v", err)
			}
			if !tc.replica && !atCapacity {
				t.Fatalf("at capacity with no replica: got %v, want %s", err, api.CodeAppConcurReached)
			}
		})
	}
}
