package promotionrollout_test

import (
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/internal/promotionrollouttest"
	"github.com/onebox-faas/faas/pkg/promotionrollout"
)

func TestSecretPromotionRequiresExplicitReviewedScope(t *testing.T) {
	for _, scenario := range []string{"accepted", "scope_missing", "cutover_missing", "rollback_missing", "recovery_missing", "rotation_missing"} {
		t.Run(scenario, func(t *testing.T) {
			target := promotionrollout.Target{AccountID: uuid.NewString(), ProjectID: uuid.NewString()}
			fixture := promotionrollouttest.New(t, target)
			switch scenario {
			case "scope_missing":
				fixture.Policy.Capabilities = []promotionrollout.Capability{promotionrollout.Health}
			case "cutover_missing":
				delete(fixture.Approval.Gates, "promotion-secret-cutover")
			case "rollback_missing":
				delete(fixture.Approval.Gates, "promotion-secret-rollback")
			case "recovery_missing":
				delete(fixture.Approval.Gates, "promotion-secret-recovery")
			case "rotation_missing":
				delete(fixture.Approval.Gates, "promotion-secret-key-rotation")
			}
			fixture.WritePolicy(t)
			if err := os.WriteFile(fixture.ApprovalPath, unchecked(t, fixture.Approval, fixture.Private), 0600); err != nil {
				t.Fatal(err)
			}
			if !fixture.Gate.Check(target, promotionrollout.Health) {
				t.Fatal("secret scope changed ordinary health admission")
			}
			if fixture.Gate.Check(target, promotionrollout.Health, promotionrollout.Secrets) != (scenario == "accepted") {
				t.Fatal("secret scope skipped reviewed evidence")
			}
		})
	}
}
