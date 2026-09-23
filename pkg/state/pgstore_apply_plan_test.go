//go:build !no_pg

// ApplyProjectPlan coverage tests for the ADR-050 Phase 3 transactional
// seam. The function is the centerpiece of POST /v1/projects — every
// branch matters:
//   - happy path (project + apps + crons in one Tx)
//   - over-quota apps (apps QuotaError)
//   - Free-plan crons not allowed (crons NotAllowed QuotaError)
//   - over-quota crons on paid plan (crons QuotaError)
//   - len(crons)==0 skip (PR #454 review F2 finding)
//   - deferred cron (empty AppID) is skipped inside Tx
//   - account-missing → ErrNotFound
//   - duplicate slug → ErrConflict
//   - duplicate workload name within the apply batch → ErrConflict
//
// Each test exercises a distinct error path so the CI coverage gate
// (Makefile test-state-coverage ≥ 70%) doesn't drop when ApplyProjectPlan
// grows. pgtest.Open handles the Postgres-not-reachable skip cleanly.

package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// phase3PlanProject seeds an account on the requested plan and returns
// a Project stub suitable for ApplyProjectPlan. Each call gets its own
// schema (pgtest.Open) so the tests don't trip each other.
func phase3PlanProject(plan api.Plan) (state.Project, []state.App, []state.Cron, api.Limits) {
	limits := api.MustLimitsFor(plan)
	apps := []state.App{
		{
			Slug:           "phase3-api",
			WorkloadName:   "api",
			RootDir:        "services/api",
			Type:           state.AppTypeFunction,
			Runtime:        "node22",
			RAMMB:          256,
			MaxConcurrency: 5,
			IdleTimeoutS:   60,
			WorkloadClass:  state.WorkloadClassHTTP,
		},
	}
	crons := []state.Cron{
		{
			Schedule: "*/5 * * * *",
			Path:     "/wake",
			Enabled:  true,
			// AppID is empty → resolved by the apply handler post-Tx
			// from the insertedApps slice. The Tx step skips these.
		},
	}
	return state.Project{
		Slug:             "phase3-fixture",
		ProductionBranch: "main",
		ScanSource:       state.ProjectScanSourceCompose,
	}, apps, crons, limits
}

func TestPg_ApplyProjectPlan_HappyPath(t *testing.T) {
	s, _, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "phase3-happy@example.test", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	proj, apps, crons, limits := phase3PlanProject(api.PlanPro)
	proj.AccountID = acct.ID

	insertedProject, insertedApps, _, err := s.ApplyProjectPlan(ctx, proj, apps, crons, limits)
	if err != nil {
		t.Fatalf("ApplyProjectPlan happy: %v", err)
	}
	if insertedProject.ID == "" {
		t.Errorf("project.ID empty after insert")
	}
	if len(insertedApps) != 1 {
		t.Errorf("len(insertedApps) = %d, want 1", len(insertedApps))
	}
	if insertedApps[0].ID == "" {
		t.Errorf("inserted app.ID empty")
	}
	personalOrg, err := s.OrgByPersonalAccount(ctx, acct.ID)
	if err != nil {
		t.Fatalf("OrgByPersonalAccount: %v", err)
	}
	if insertedApps[0].OrgID != personalOrg.ID {
		t.Errorf("inserted app.OrgID = %q, want personal org %q", insertedApps[0].OrgID, personalOrg.ID)
	}
	// scan_source round-trips verbatim (PR #454 — reposcan source pin).
	if insertedProject.ScanSource != state.ProjectScanSourceCompose {
		t.Errorf("ScanSource = %q, want compose", insertedProject.ScanSource)
	}
}

func TestPg_ApplyProjectPlan_OverQuotaApps(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "phase3-quota-apps@example.test", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	limits := api.MustLimitsFor(api.PlanFree) // DeployedApps: 1
	// Free plan caps at 1 deployed app; ask for 2 → over-quota.
	proj, apps, _, _ := phase3PlanProject(api.PlanFree)
	apps = append(apps, apps[0]) // second app with the same slug → unique trip instead
	apps[1].Slug = "phase3-extra"
	apps[1].WorkloadName = "extra"
	proj.AccountID = acct.ID
	proj.Slug = "phase3-quota-apps"

	_, _, _, err = s.ApplyProjectPlan(ctx, proj, apps, nil, limits)
	if err == nil {
		t.Fatalf("ApplyProjectPlan over-quota apps: want error, got nil")
	}
	var qe *state.QuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("ApplyProjectPlan over-quota apps = %v, want *QuotaError", err)
	}
	if qe.Kind != state.QuotaErrorKindApps {
		t.Errorf("QuotaError.Kind = %q, want apps", qe.Kind)
	}
	if qe.Limit != limits.DeployedApps {
		t.Errorf("QuotaError.Limit = %d, want %d", qe.Limit, limits.DeployedApps)
	}
}

func TestPg_ApplyProjectPlan_FreePlanCronsNotAllowed(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "phase3-free-cron@example.test", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	proj, apps, crons, limits := phase3PlanProject(api.PlanFree)
	proj.AccountID = acct.ID

	_, _, _, err = s.ApplyProjectPlan(ctx, proj, apps, crons, limits)
	var qe *state.QuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("ApplyProjectPlan Free+cron = %v, want *QuotaError", err)
	}
	if qe.Kind != state.QuotaErrorKindCrons || !qe.NotAllowed {
		t.Errorf("QuotaError = %+v, want Kind=crons NotAllowed=true", qe)
	}
}

func TestPg_ApplyProjectPlan_OverQuotaCrons(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "phase3-quota-crons@example.test", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	proj, apps, _, limits := phase3PlanProject(api.PlanHobby)
	proj.AccountID = acct.ID
	// Hobby cron cap is 5/account; ask for 6 → over-quota.
	crons := make([]state.Cron, limits.CronLimitPerAccount+1)
	for i := range crons {
		crons[i] = state.Cron{Schedule: "0 * * * *", Path: "/x", Enabled: true}
	}
	_, _, _, err = s.ApplyProjectPlan(ctx, proj, apps, crons, limits)
	var qe *state.QuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("ApplyProjectPlan over-quota crons = %v, want *QuotaError", err)
	}
	if qe.Kind != state.QuotaErrorKindCrons {
		t.Errorf("QuotaError.Kind = %q, want crons", qe.Kind)
	}
}

func TestPg_ApplyProjectPlan_ZeroCronsSkipsCheck(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "phase3-zero-cron@example.test", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	proj, apps, _, limits := phase3PlanProject(api.PlanFree)
	proj.AccountID = acct.ID

	_, _, _, err = s.ApplyProjectPlan(ctx, proj, apps, nil, limits)
	if err != nil {
		t.Fatalf("ApplyProjectPlan zero-cron on Free plan: %v", err)
	}
}

func TestPg_ApplyProjectPlan_DeferredCronSkipped(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "phase3-deferred-cron@example.test", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	proj, apps, crons, limits := phase3PlanProject(api.PlanPro)
	proj.AccountID = acct.ID

	insertedProject, insertedApps, insertedCrons, err := s.ApplyProjectPlan(ctx, proj, apps, crons, limits)
	if err != nil {
		t.Fatalf("ApplyProjectPlan deferred cron: %v", err)
	}
	// Tx must have skipped the deferred cron (AppID == ""). The quota
	// check still ran (step 3), so this is the F1 pin: empty-AppID
	// crons are not promoted to rows inside the Tx.
	_ = insertedProject
	_ = insertedApps
	if len(insertedCrons) != 0 {
		t.Errorf("len(insertedCrons) = %d, want 0 (deferred skipped in Tx)", len(insertedCrons))
	}
}

func TestPg_ApplyProjectPlan_AccountMissing(t *testing.T) {
	s, ctx := pgStore(t)
	proj, apps, _, limits := phase3PlanProject(api.PlanPro)
	proj.AccountID = "00000000-0000-0000-0000-000000000000"

	_, _, _, err := s.ApplyProjectPlan(ctx, proj, apps, nil, limits)
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("ApplyProjectPlan missing acct = %v, want ErrNotFound", err)
	}
}

func TestPg_ApplyProjectPlan_DuplicateSlug(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "phase3-dup-slug@example.test", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	proj, apps, _, limits := phase3PlanProject(api.PlanPro)
	proj.AccountID = acct.ID
	proj.Slug = "phase3-dup"

	if _, _, _, err := s.ApplyProjectPlan(ctx, proj, apps, nil, limits); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	// Second apply with the same (account_id, slug) → ErrConflict.
	proj2 := proj
	proj2.ScanSource = state.ProjectScanSourceProcfile // different src is fine; slug collides
	if _, _, _, err := s.ApplyProjectPlan(ctx, proj2, nil, nil, limits); !errors.Is(err, state.ErrConflict) {
		t.Errorf("second apply = %v, want ErrConflict", err)
	}
}

func TestPg_ApplyProjectPlan_DuplicateWorkloadName(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "phase3-dup-wl@example.test", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	proj, apps, _, limits := phase3PlanProject(api.PlanPro)
	apps = append(apps, apps[0]) // duplicate workload_name → apps_project_workload_uniq
	apps[1].Slug = "phase3-extra-slug"
	proj.AccountID = acct.ID

	_, _, _, err = s.ApplyProjectPlan(ctx, proj, apps, nil, limits)
	if !errors.Is(err, state.ErrConflict) {
		t.Errorf("ApplyProjectPlan dup workload = %v, want ErrConflict", err)
	}
}
