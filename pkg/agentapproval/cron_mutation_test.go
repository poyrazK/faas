package agentapproval

import (
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
)

func TestReviewedCronDeletionRetainsApproval(t *testing.T) {
	rule, ok := Classify("POST /v1/apps/{slug}/cron-deletions")
	if !ok || rule.Category != api.AgentApprovalDelete || rule.Window != api.CronMutationReviewMaxAge {
		t.Fatalf("deletion gate: %+v %v", rule, ok)
	}
	if _, ok := Classify("GET /v1/apps/{slug}/cron-mutations/{request_id}"); ok {
		t.Fatal("passive receipt requires mutation approval")
	}
}
