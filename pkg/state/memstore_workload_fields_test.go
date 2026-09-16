package state

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreWorkloadFieldsDefaultsAndUpdate(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	account, err := m.CreateAccount(ctx, "workload-fields-mem@example.test", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	created, err := m.CreateApp(ctx, App{
		AccountID: account.ID,
		Slug:      "workload-fields-mem-create",
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if created.WorkloadClass != WorkloadClassHTTP {
		t.Fatalf("CreateApp default WorkloadClass = %q, want %q", created.WorkloadClass, WorkloadClassHTTP)
	}

	quotaCreated, err := m.CreateAppIfUnderQuota(ctx, App{
		AccountID: account.ID,
		Slug:      "workload-fields-mem-quota",
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	if quotaCreated.WorkloadClass != WorkloadClassHTTP {
		t.Fatalf("CreateAppIfUnderQuota default WorkloadClass = %q, want %q", quotaCreated.WorkloadClass, WorkloadClassHTTP)
	}

	class := WorkloadClassWorker
	updated, err := m.UpdateApp(ctx, created.ID, UpdateAppParams{WorkloadClass: &class})
	if err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	if updated.WorkloadClass != class {
		t.Fatalf("UpdateApp WorkloadClass = %q, want %q", updated.WorkloadClass, class)
	}
}
