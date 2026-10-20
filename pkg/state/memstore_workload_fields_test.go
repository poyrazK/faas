package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreWorkloadFieldsDefaultsAndUpdate(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	account, err := m.CreateAccount(ctx, "workload-fields-mem@example.test", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	created, err := m.CreateApp(ctx, state.App{
		AccountID: account.ID,
		Slug:      "workload-fields-mem-create",
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if created.WorkloadClass != state.WorkloadClassHTTP {
		t.Fatalf("CreateApp default WorkloadClass = %q, want %q", created.WorkloadClass, state.WorkloadClassHTTP)
	}

	quotaCreated, err := m.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: account.ID,
		Slug:      "workload-fields-mem-quota",
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	if quotaCreated.WorkloadClass != state.WorkloadClassHTTP {
		t.Fatalf("CreateAppIfUnderQuota default WorkloadClass = %q, want %q", quotaCreated.WorkloadClass, state.WorkloadClassHTTP)
	}

	class := state.WorkloadClassWorker
	updated, err := m.UpdateApp(ctx, created.ID, state.UpdateAppParams{WorkloadClass: &class})
	if err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	if updated.WorkloadClass != class {
		t.Fatalf("UpdateApp WorkloadClass = %q, want %q", updated.WorkloadClass, class)
	}
}
