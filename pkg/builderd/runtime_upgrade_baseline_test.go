package builderd

// adr: 684

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

type rejectedRuntimeUpgradeBaseline struct {
	state.RuntimeUpgradeBaselineStore
	err error
}

func (s rejectedRuntimeUpgradeBaseline) ValidateDeploymentRuntimeUpgradeBaseline(context.Context, string) error {
	return s.err
}

func TestRuntimeUpgradeBuildRefusesStaleOrUnreadableBaseline(t *testing.T) {
	for _, err := range []error{state.ErrConflict, errors.New("baseline database unavailable")} {
		t.Run(err.Error(), func(t *testing.T) {
			readDefault := false
			_, got := resolveDeploymentRuntimeBaseRef(t.Context(), rejectedRuntimeUpgradeBaseline{err: err}, state.App{Runtime: "node22"}, state.Deployment{}, FrameworkUnknown, func(string) string {
				readDefault = true
				return "default"
			})
			if !errors.Is(got, err) || readDefault {
				t.Fatal("baseline failure selected daemon default", got, readDefault)
			}
		})
	}
}
