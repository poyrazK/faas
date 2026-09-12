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

	// Hand-built callers may omit the detector hint. The SQL path must
	// preserve the schema-safe HTTP default rather than sending an empty
	// value that violates apps_workload_class_chk.
	defaulted, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID,
		Slug:      "workload-fields-create-default",
		Type:      state.AppTypeFunction,
	})
	if err != nil {
		t.Fatalf("CreateApp default: %v", err)
	}
	if defaulted.WorkloadClass != state.WorkloadClassHTTP {
		t.Fatalf("CreateApp default WorkloadClass = %q, want %q", defaulted.WorkloadClass, state.WorkloadClassHTTP)
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

	defaulted, err := s.CreateAppIfUnderQuota(ctx, state.App{
		AccountID: acct.ID,
		Slug:      "workload-fields-quota-default",
		Type:      state.AppTypeFunction,
	}, api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota default: %v", err)
	}
	if defaulted.WorkloadClass != state.WorkloadClassHTTP {
		t.Fatalf("CreateAppIfUnderQuota default WorkloadClass = %q, want %q", defaulted.WorkloadClass, state.WorkloadClassHTTP)
	}

	updatedClass := state.WorkloadClassWorker
	updated, err := s.UpdateApp(ctx, got.ID, state.UpdateAppParams{WorkloadClass: &updatedClass})
	if err != nil {
		t.Fatalf("UpdateApp WorkloadClass: %v", err)
	}
	if updated.WorkloadClass != updatedClass {
		t.Fatalf("UpdateApp WorkloadClass = %q, want %q", updated.WorkloadClass, updatedClass)
	}
}
