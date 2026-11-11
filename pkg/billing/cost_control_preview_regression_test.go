package billing

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestCostControlAccountBudgetPreviewIncludesImageRuns(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "audit-image-preview@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.JobEnsureImageRun(t.Context(), state.ImageRunEnsure{AccountID: account.ID, Name: "audit-image-run", ImageRef: "example.test/audit:v1", RAMMB: 128, TaskTimeoutSec: 30, MaxCached: 10})
	if err != nil {
		t.Fatal(err)
	}
	members, err := financialBudgetMembers(t.Context(), store, account.ID, financial.BudgetScope{Kind: "account"})
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range members {
		if member.target.ID == result.Job.ID {
			return
		}
	}
	t.Errorf("account budget preview omits image-run job %s even though strict account funding covers image_run attempts", result.Job.ID)
}
