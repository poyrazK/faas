package promotion

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/internal/promotionrollouttest"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promotionrollout"
)

func TestCoordinatedRolloutDoesNotReuseIndependentApprovals(t *testing.T) {
	for _, approved := range []bool{false, true} {
		t.Run(map[bool]string{false: "independent", true: "coordinated"}[approved], func(t *testing.T) {
			target := promotionrollout.Target{AccountID: uuid.NewString(), ProjectID: uuid.NewString()}
			f := promotionrollouttest.New(t, target)
			if !approved {
				f.Approval.Capabilities = slices.DeleteFunc(f.Approval.Capabilities, func(c promotionrollout.Capability) bool { return c == promotionrollout.Coordinated })
				f.WriteApproval(t)
			}
			policy := &api.ProjectPromotionHealthPolicy{Worker: &api.ProjectReleaseWorker{}}
			if RolloutProblem(f.Gate, target.AccountID, target.ProjectID, policy, nil) != nil {
				t.Fatal("worker-only approval changed")
			}
			policy.Migration = &api.ProjectPromotionMigrationPolicy{}
			if (RolloutProblem(f.Gate, target.AccountID, target.ProjectID, policy, nil) == nil) != approved {
				t.Fatal("independent approval authorized composition")
			}
		})
	}
}
