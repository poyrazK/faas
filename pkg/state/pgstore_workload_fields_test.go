//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCreateApp_PersistsWorkloadFields(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "workload-fields-create@example.test", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	got, err := s.CreateApp(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           "workload-fields-create",
		Type:           state.AppTypeFunction,
		Runtime:        "node22",
		RAMMB:          256,
		MaxConcurrency: 2,
		WorkloadClass:  state.WorkloadClassWorker,
		StartCommand:   "node worker.js",
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if got.WorkloadClass != state.WorkloadClassWorker || got.StartCommand != "node worker.js" {
		t.Fatalf("CreateApp fields = class %q command %q, want worker/node worker.js", got.WorkloadClass, got.StartCommand)
	}
}

func TestPgCreateAppIfUnderQuota_PersistsWorkloadFields(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "workload-fields-quota@example.test", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	got, err := s.CreateAppIfUnderQuota(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           "workload-fields-quota",
		Type:           state.AppTypeFunction,
		Runtime:        "python313",
		RAMMB:          256,
		MaxConcurrency: 2,
		WorkloadClass:  state.WorkloadClassJob,
		StartCommand:   "python job.py",
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	if got.WorkloadClass != state.WorkloadClassJob || got.StartCommand != "python job.py" {
		t.Fatalf("CreateAppIfUnderQuota fields = class %q command %q, want job/python job.py", got.WorkloadClass, got.StartCommand)
	}
}
