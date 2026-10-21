package promotionrollout_test

import (
	"os"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/internal/promotionrollouttest"
	"github.com/onebox-faas/faas/pkg/promotionrollout"
)

func TestCoordinatedPromotionRequiresExplicitApproval(t *testing.T) {
	for _, scenario := range []string{"approved", "old_approval", "missing_receipt", "missing_resolution_receipt", "missing_worker_receipt", "missing_migration_receipt", "disabled_migration", "disabled_worker"} {
		t.Run(scenario, func(t *testing.T) {
			selected := target()
			f := promotionrollouttest.New(t, selected)
			if scenario == "old_approval" {
				f.Approval.Capabilities = slices.DeleteFunc(f.Approval.Capabilities, func(c promotionrollout.Capability) bool { return c == promotionrollout.Coordinated })
			}
			if scenario == "missing_receipt" {
				delete(f.Approval.Gates, "coordinated-migration-workers")
			}
			if scenario == "missing_resolution_receipt" {
				delete(f.Approval.Gates, "worker-replacement-resolve")
			}
			if scenario == "missing_worker_receipt" {
				delete(f.Approval.Gates, "worker-retention-expiry")
			}
			if scenario == "missing_migration_receipt" {
				delete(f.Approval.Gates, "migration-input-drift")
			}
			if err := os.WriteFile(f.ApprovalPath, unchecked(t, f.Approval, f.Private), 0600); err != nil {
				t.Fatal(err)
			}
			enabled := map[promotionrollout.Capability]bool{promotionrollout.Health: true, promotionrollout.Workers: scenario != "disabled_worker", promotionrollout.Migrations: scenario != "disabled_migration"}
			gate := promotionrollout.New(promotionrollout.Config{PolicyPath: f.PolicyPath, PublicKey: f.Public, SourceSHA: promotionrollouttest.Revision, Enabled: enabled, Now: func() time.Time { return f.Now }})
			if gate.Check(selected, promotionrollout.Coordinated) != (scenario == "approved") {
				t.Fatal("coordinated scope was not enforced")
			}
			if scenario == "missing_resolution_receipt" && gate.Check(selected, promotionrollout.Workers) {
				t.Fatal("worker approval omitted replacement resolution evidence")
			}
			if !gate.Check(selected, promotionrollout.Health) {
				t.Fatal("combined scope changed basic HTTP admission")
			}
		})
	}
}
