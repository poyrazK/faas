package pgintegration_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/rollout"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestInvestigationWaitRejectsUnacknowledgedServingResident(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		lease, desired := serveRuntimeConvergenceGraph(t, basic)
		release, err := basic.(state.ProjectReleaseSetReader).ActiveProjectReleaseSet(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
		if err != nil || len(release.Members) == 0 {
			t.Fatalf("release: %+v %v", release, err)
		}
		dep, err := basic.DeploymentByID(t.Context(), release.Members[0].DeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		app, err := basic.AppByID(t.Context(), dep.AppID)
		if err != nil {
			t.Fatal(err)
		}
		instance := runtimeInstance(t, basic, app, dep, state.StateWaking)
		if err := basic.UpdateInstanceState(t.Context(), instance.ID, string(state.StateRunning)); err != nil {
			t.Fatal(err)
		}
		targets, err := basic.(state.EnvironmentGitOpsRuntimeStore).ObserveEnvironmentGitOpsRuntime(t.Context(), lease)
		if err != nil {
			t.Fatal(err)
		}
		unready := false
		for _, target := range targets {
			if target.AppID == app.ID && target.StaleResidents == 1 && !target.Ready() {
				unready = true
			}
		}
		if !unready {
			t.Fatalf("fixture has no stale resident: %+v", targets)
		}
		runtime := basic.(state.EnvironmentGitOpsRuntimeStore)
		if err := runtime.EnsureEnvironmentGitOpsRuntime(t.Context(), lease, claimedIntentPlan(t, basic.(intentTestStore), lease, desired)); err != nil {
			t.Fatal(err)
		}
		pending, err := runtime.PendingEnvironmentGitOpsRuntime(t.Context(), lease)
		if err != nil || len(pending) == 0 {
			t.Fatalf("missing runtime recovery effect: %+v %v", pending, err)
		}
		polls := basic.(state.EnvironmentGitSourcePollStore)
		now := time.Now().UTC()
		poll, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), poll, state.EnvironmentGitSourcePollResult{CommitSHA: lease.Revision.CommitSHA, Digest: lease.Revision.Digest}, now, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		source, err := basic.EnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
		if err != nil {
			t.Fatal(err)
		}
		evidence, err := basic.(state.EnvironmentGitOpsCurrentWorkloadEvidenceStore).CurrentEnvironmentGitOpsWorkloadEvidence(t.Context(), source.AccountID, source.ID)
		if err != nil || evidence == nil {
			t.Fatalf("evidence: %+v %v", evidence, err)
		}
		tracker, err := rollout.NewTracker(lease.Revision.CommitSHA, source.EnvironmentSlug)
		if err != nil {
			t.Fatal(err)
		}
		result, err := tracker.Observe(api.EnvironmentGitOpsStatusResponse{Source: source, WorkloadEvidence: evidence}, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("runtime ready=false, stale residents=1; evidence qualified=%t activated=%t serving=%t; wait=%s exit=%d", evidence.Qualified, evidence.Activated, evidence.Serving, result.Status, result.ExitCode())
		if result.Status == "serving" {
			t.Error("wait certified success without a current serving runtime input acknowledgement")
		}
		ackRuntimeInputs(t, basic, app, dep, instance)
		evidence, err = basic.(state.EnvironmentGitOpsCurrentWorkloadEvidenceStore).CurrentEnvironmentGitOpsWorkloadEvidence(t.Context(), source.AccountID, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if evidence == nil || evidence.Serving {
			t.Fatal("uncompleted durable runtime effect certified serving")
		}
		for _, effect := range pending {
			if progress, err := runtime.ReconcileEnvironmentGitOpsRuntime(t.Context(), lease, effect.ID); err != nil || !progress.Ready {
				t.Fatalf("fresh runtime effect did not complete: %+v %v", progress, err)
			}
		}
		evidence, err = basic.(state.EnvironmentGitOpsCurrentWorkloadEvidenceStore).CurrentEnvironmentGitOpsWorkloadEvidence(t.Context(), source.AccountID, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		result, err = tracker.Observe(api.EnvironmentGitOpsStatusResponse{Source: source, WorkloadEvidence: evidence}, time.Now().UTC())
		if err != nil || result.Status != "serving" {
			t.Fatalf("fresh resident did not restore serving readiness: %+v %v", result, err)
		}
	})
}

func TestInvestigationPruneMissingSecretReference(t *testing.T) {
	stores(t, func(t *testing.T, basic gitOpsTestStore) {
		store, source, desired, app := secretRefFixture(t, basic, "report")
		adoptSecretRefs(t, store, source)
		if err := store.DeleteAppSecretInScope(t.Context(), source.AccountID, app.ID, "production", "DATABASE_A"); err != nil {
			t.Fatal(err)
		}
		prune := true
		source, err := store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Prune: &prune})
		if err != nil {
			t.Fatal(err)
		}
		definition := desired.Definition
		definition.Workloads["api"] = api.EnvironmentWorkload{App: app.Slug, SecretRefs: map[string]string{}}
		desired, err = environmentsync.Compile(definition)
		if err != nil {
			t.Fatal(err)
		}
		source, _, err = store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("b", 40)))
		if err != nil {
			t.Fatal(err)
		}
		lease, err := store.ClaimEnvironmentGitOps(t.Context(), "prune-missing-secret", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		plan := claimedIntentPlan(t, store, lease, desired)
		removal := false
		for _, change := range plan.Changes {
			removal = removal || change.Path == "secret_refs/DATABASE_URL" && change.Action == "remove"
		}
		if !removal {
			t.Fatalf("fixture has no reviewed secret-ref removal: %+v", plan)
		}
		t.Logf("reviewed removal of broken reference: can_apply=%t blockers=%v", plan.CanApply(), plan.BlockingReasons)
		if err := store.UpsertAppSecretInScope(t.Context(), source.AccountID, app.ID, "production", "DATABASE_A", []byte("fixture-restored-ciphertext")); err != nil {
			t.Fatal(err)
		}
		restored := claimedIntentPlan(t, store, lease, desired)
		if !restored.CanApply() {
			t.Fatalf("restoring old secret did not clear blocker: %+v", restored)
		}
		if !plan.CanApply() {
			t.Error("must recreate a deleted secret just to prune its obsolete reference")
		}
		if err := store.DeleteAppSecretInScope(t.Context(), source.AccountID, app.ID, "production", "DATABASE_A"); err != nil {
			t.Fatal(err)
		}
		if err := store.SetEnvironmentGitOpsOverride(t.Context(), source.AccountID, source.ID, state.EnvironmentGitOpsOverrideRequest{Resource: "workload/api", Path: "secret_refs/DATABASE_URL", Reason: "retain existing reference", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
		if held := claimedIntentPlan(t, store, lease, desired); held.CanApply() {
			t.Fatal("an override retained a broken reference without a blocker")
		}
		if err := store.RemoveEnvironmentGitOpsOverride(t.Context(), source.AccountID, source.ID, "workload/api", "secret_refs/DATABASE_URL"); err != nil {
			t.Fatal(err)
		}
		source, err = store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Mode: "enforce"})
		if err != nil {
			t.Fatal(err)
		}
		lease, err = store.ClaimEnvironmentGitOps(t.Context(), "apply-obsolete-reference-removal", time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, claimedIntentPlan(t, store, lease, desired)); err != nil {
			t.Fatalf("approved removal failed: %v", err)
		}
		if after := claimedIntentPlan(t, store, lease, desired); !after.CanApply() || after.HasDrift() {
			t.Fatalf("obsolete reference did not converge: %+v", after)
		}
	})
}

func TestInvestigationObservationUsesCompanyPlan(t *testing.T) {
	for _, tc := range []struct {
		name              string
		personal, company api.Plan
		allowed           bool
	}{
		{"paid-company-free-creator", api.PlanFree, api.PlanPro, true},
		{"free-company-paid-creator", api.PlanPro, api.PlanFree, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stores(t, func(t *testing.T, basic gitOpsTestStore) {
				ctx := t.Context()
				identity, err := basic.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: "observe-company@example.test", Plan: tc.personal})
				if err != nil {
					t.Fatal(err)
				}
				company, err := basic.CreateOrg(ctx, state.Org{Slug: "observe-company", Name: "Observe Company", Plan: api.PlanPro})
				if err != nil {
					t.Fatal(err)
				}
				if err := basic.AddOrgMember(ctx, company.ID, identity.Account.ID, state.OrgRoleOwner, nil); err != nil {
					t.Fatal(err)
				}
				if err := basic.UpdateAccountProviderCustomerID(ctx, company.ID, "cus_observe_company"); err != nil {
					t.Fatal(err)
				}
				if err := basic.UpdateAccountStripeSubscriptionItem(ctx, company.ID, "si_observe_company"); err != nil {
					t.Fatal(err)
				}
				if tc.company != api.PlanPro {
					if err := basic.UpdateOrgPlan(ctx, company.ID, tc.company); err != nil {
						t.Fatal(err)
					}
				}
				project, err := basic.CreateProject(ctx, state.Project{AccountID: identity.Account.ID, OrgID: company.ID, Slug: "company-shop", RepoFullName: "example/company-shop", ProductionBranch: "main", InstallID: 42})
				if err != nil {
					t.Fatal(err)
				}
				app, err := basic.CreateApp(ctx, state.App{AccountID: identity.Account.ID, OrgID: company.ID, ProjectID: project.ID, Slug: "company-api", Type: state.AppTypeApp, Status: state.AppActive})
				if err != nil {
					t.Fatal(err)
				}
				owner, err := state.AppResourceAccount(ctx, basic, app)
				if err != nil || !owner.ResourceOwnershipEnabled || owner.Plan != tc.company {
					t.Fatalf("owner setup: %+v %v", owner, err)
				}
				source, err := basic.CreateEnvironmentGitSource(ctx, project.AccountID, project.ID, "production", state.EnvironmentGitSourceSpec{RepositoryID: 123, InstallationID: 42, Repository: project.RepoFullName, Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: "report", ApprovalPolicy: "manual"})
				if err != nil {
					t.Fatal(err)
				}
				vars := map[string]string{}
				for i := 0; i < 20; i++ {
					vars[fmt.Sprintf("VAR_%02d", i)] = "value"
				}
				desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{"api": {App: app.Slug, Variables: vars}}})
				if err != nil {
					t.Fatal(err)
				}
				source, _, err = basic.ApproveEnvironmentDesiredRevision(ctx, approval(source, desired, strings.Repeat("a", 40)))
				if err != nil {
					t.Fatal(err)
				}
				preview, err := basic.(state.EnvironmentGitOpsIntentStore).PreviewEnvironmentGitOpsAdoption(ctx, source.AccountID, source.ID)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("personal=%s company=%s, desired_vars=20, personal_limit=%d company_limit=%d, can_apply=%t blockers=%v", tc.personal, tc.company, api.MustLimitsFor(tc.personal).EnvVarsMax, api.MustLimitsFor(tc.company).EnvVarsMax, preview.CanApply(), preview.BlockingReasons)
				if preview.CanApply() != tc.allowed {
					t.Errorf("observation used creator plan instead of current company plan")
				}
			})
		})
	}
}
