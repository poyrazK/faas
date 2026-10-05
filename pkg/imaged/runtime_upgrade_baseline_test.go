package imaged

// adr: 598

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type rejectedRuntimeUpgradeBaselineStore struct {
	*state.MemStore
	err error
}

func (s rejectedRuntimeUpgradeBaselineStore) ValidateDeploymentRuntimeUpgradeBaseline(context.Context, string) error {
	return s.err
}

func TestRuntimeUpgradeImageRefusesStaleOrUnreadableBaseline(t *testing.T) {
	for _, err := range []error{state.ErrConflict, errors.New("baseline database unavailable")} {
		t.Run(err.Error(), func(t *testing.T) {
			h := &Handler{store: rejectedRuntimeUpgradeBaselineStore{MemStore: state.NewMemStore(), err: err}}
			_, got := h.explicitRuntimeUpgradeTarget(t.Context(), state.App{}, state.Deployment{}, "node22")
			if !errors.Is(got, err) {
				t.Fatal("baseline failure bypassed image preparation", got)
			}
		})
	}
}
