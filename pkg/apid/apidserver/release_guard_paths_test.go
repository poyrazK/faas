// adr: 20261019071733 amendment 1, 20261020085706 amendment 1 — every path
// that releases into production meets deploy freezes and release approvals.
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// guardLiveDeployment makes a live, serving release of appID in scope.
func guardLiveDeployment(t *testing.T, store *state.MemStore, appID, scope string) state.Deployment {
	t.Helper()
	ctx := context.Background()
	d, err := store.CreateDeployment(ctx, state.Deployment{AppID: appID, Scope: scope, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:guard" + time.Now().Format("150405.000000000"), Status: state.DeploySnapshotting})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []state.DeploymentStatus{state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting} {
		if err := store.UpdateDeploymentStatus(ctx, d.ID, s, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetDeploymentRootfs(ctx, d.ID, "/tmp/r.ext4", "apps/"+appID+"/"+d.ID+".ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	return d
}

// guardFailedSourceDeployment makes a failed source release of appID that a
// retry can rebuild.
func guardFailedSourceDeployment(t *testing.T, store *state.MemStore, appID string) state.Deployment {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := os.WriteFile(source, []byte("retained source"), 0o600); err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: appID, Kind: state.DeploymentKindTarball, SourcePath: source, SourceBytes: 15, Handler: "index.handler"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FailSourceDeployment(t.Context(), dep.ID, "test failure"); err != nil {
		t.Fatal(err)
	}
	return dep
}

func guardProblemCode(t *testing.T, body []byte) string {
	t.Helper()
	var p api.Problem
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("problem body %s: %v", body, err)
	}
	return p.Code
}

func TestRetryMeetsDeployFreezes(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	store := *e.Store()
	acct := *e.Acct()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "retry-frozen", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep := guardFailedSourceDeployment(t, store, app.ID)
	now := time.Now().UTC()
	frozen, err := store.CreateReleaseFreeze(t.Context(), state.ReleaseFreeze{AccountID: acct.ID, AppID: app.ID,
		Kind: state.ReleaseFreezeWindow, StartsAt: &now, Reason: "launch week", CreatedBy: "ops"}, api.ReleaseFreezesMax, now)
	if err != nil {
		t.Fatal(err)
	}
	r := e.Do(t, http.MethodPost, "/v1/deployments/"+dep.ID+"/retry", api.RetryDeploymentRequest{FromStage: "source_download"}, nil)
	if r.Code != http.StatusConflict || guardProblemCode(t, r.Body.Bytes()) != api.CodeReleaseFrozen {
		t.Fatalf("retry during a freeze = %d %s, want 409 release_frozen", r.Code, r.Body)
	}
	if !strings.Contains(r.Body.String(), "--override-freeze") {
		t.Fatalf("retry refusal does not name the deploy override: %s", r.Body)
	}
	if latest, err := store.LatestDeployment(t.Context(), app.ID); err != nil || latest.ID != dep.ID {
		t.Fatalf("a frozen retry created a deployment: %s %v", latest.ID, err)
	}
	if _, err := store.LiftReleaseFreeze(t.Context(), acct.ID, frozen.ID, "ops", now); err != nil {
		t.Fatal(err)
	}
	r = e.Do(t, http.MethodPost, "/v1/deployments/"+dep.ID+"/retry", api.RetryDeploymentRequest{FromStage: "source_download"}, nil)
	if r.Code != http.StatusAccepted {
		t.Fatalf("retry after the freeze lifted = %d %s", r.Code, r.Body)
	}
}

func TestRetryMeetsTheApprovalCanaryRequirement(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	t.Setenv("FAAS_SPOOL_ROOT", t.TempDir())
	store := *e.Store()
	acct := *e.Acct()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "retry-approvals", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	guardLiveDeployment(t, store, app.ID, api.DefaultEnvScope)
	if _, err := store.SetReleaseApprovalPolicy(t.Context(), state.ReleaseApprovalPolicy{AppID: app.ID, AccountID: acct.ID,
		RequiredApprovals: 1, AfterStage: 1, TimeoutMinutes: 60, Approvers: state.ReleaseApproversAdmin}, time.Now()); err != nil {
		t.Fatal(err)
	}
	dep := guardFailedSourceDeployment(t, store, app.ID)
	r := e.Do(t, http.MethodPost, "/v1/deployments/"+dep.ID+"/retry", api.RetryDeploymentRequest{FromStage: "source_download"}, nil)
	if r.Code != http.StatusConflict || guardProblemCode(t, r.Body.Bytes()) != api.CodeReleaseApprovalCanaryRequired {
		t.Fatalf("immediate retry under an approval policy = %d %s, want 409 release_approval_canary_required", r.Code, r.Body)
	}
}

func TestPromotionIntoProductionNeedsAnApprovalForAppsRequiringOne(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	store := *e.Store()
	acct := *e.Acct()
	srv := *e.S()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "promoted-shop", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	guardLiveDeployment(t, store, app.ID, "production")
	guardLiveDeployment(t, store, app.ID, "staging")
	if _, err := store.SetReleaseApprovalPolicy(t.Context(), state.ReleaseApprovalPolicy{AppID: app.ID, AccountID: acct.ID,
		RequiredApprovals: 1, AfterStage: 1, TimeoutMinutes: 60, Approvers: state.ReleaseApproversAdmin}, time.Now()); err != nil {
		t.Fatal(err)
	}
	p := srv.PromotionApprovalProblemForTest(t.Context(), []state.App{app}, "shop", "production", false)
	if p == nil || p.Code != api.CodeReleaseApprovalCanaryRequired || !strings.Contains(p.Hint, "projects environments protect shop production") {
		t.Fatalf("unprotected promotion into production = %+v, want release_approval_canary_required naming the protect command", p)
	}
	if p := srv.PromotionApprovalProblemForTest(t.Context(), []state.App{app}, "shop", "production", true); p != nil {
		t.Fatalf("a protected environment's promotion approval stands in for the policy: %+v", p)
	}
	if p := srv.PromotionApprovalProblemForTest(t.Context(), []state.App{app}, "shop", "staging", false); p != nil {
		t.Fatalf("promotions into other environments are not production releases: %+v", p)
	}
}

func TestGitOpsActivationIntoProductionMeetsTheReleaseGuards(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	store := *e.Store()
	acct := *e.Acct()
	srv := *e.S()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "gitops-shop", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	guardLiveDeployment(t, store, app.ID, "production")
	graph := state.EnvironmentWorkloadGraph{ID: "graph", Members: []state.EnvironmentWorkloadGraphMember{{AppID: app.ID, CandidateDeploymentID: "candidate"}}}
	lease := func(environment, approvalPolicy string) state.EnvironmentGitOpsLease {
		var l state.EnvironmentGitOpsLease
		l.Source.EnvironmentSlug = environment
		l.Source.Spec.ApprovalPolicy = approvalPolicy
		return l
	}
	hold := func(l state.EnvironmentGitOpsLease) string {
		t.Helper()
		got, err := srv.GitOpsReleaseHoldForTest(t.Context(), l, graph)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := hold(lease("production", "protected_branch")); got != "" {
		t.Fatalf("nothing holds an unguarded app: %q", got)
	}
	if _, err := store.SetReleaseApprovalPolicy(t.Context(), state.ReleaseApprovalPolicy{AppID: app.ID, AccountID: acct.ID,
		RequiredApprovals: 1, AfterStage: 1, TimeoutMinutes: 60, Approvers: state.ReleaseApproversAdmin}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := hold(lease("production", "protected_branch")); !strings.Contains(got, "release approvals") {
		t.Fatalf("a protected-branch revision of an app requiring approvals = %q, want an approvals hold", got)
	}
	if got := hold(lease("production", "manual")); got != "" {
		t.Fatalf("a revision approved in Gregale stands in for the policy: %q", got)
	}
	now := time.Now().UTC()
	if _, err := store.CreateReleaseFreeze(t.Context(), state.ReleaseFreeze{AccountID: acct.ID, AppID: app.ID,
		Kind: state.ReleaseFreezeWindow, StartsAt: &now, Reason: "launch week", CreatedBy: "ops"}, api.ReleaseFreezesMax, now); err != nil {
		t.Fatal(err)
	}
	if got := hold(lease("production", "manual")); !strings.Contains(got, "deploy freeze") {
		t.Fatalf("activation during a freeze = %q, want a freeze hold", got)
	}
	if got := hold(lease("staging", "protected_branch")); got != "" {
		t.Fatalf("other environments are not held: %q", got)
	}
}

func TestReleaseSafetyDashboardShowsTheAppsGuards(t *testing.T) {
	e := apidserver.Setup(t, api.PlanScale)
	store := *e.Store()
	acct := *e.Acct()
	srv := *e.S()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "guarded-shop", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if rec := srv.RenderReleaseSafetyForTest(acct, app.Slug); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Not frozen.") ||
		!strings.Contains(rec.Body.String(), "Releases do not wait for approval.") {
		t.Fatalf("unguarded app page = %d %s", rec.Code, rec.Body)
	}
	now := time.Now().UTC()
	if _, err := store.CreateReleaseFreeze(t.Context(), state.ReleaseFreeze{AccountID: acct.ID, AppID: app.ID,
		Kind: state.ReleaseFreezeWindow, StartsAt: &now, Reason: "quarter close", CreatedBy: "ops"}, api.ReleaseFreezesMax, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetReleaseApprovalPolicy(t.Context(), state.ReleaseApprovalPolicy{AppID: app.ID, AccountID: acct.ID,
		RequiredApprovals: 2, AfterStage: 1, TimeoutMinutes: 90, Approvers: state.ReleaseApproversAdmin}, now); err != nil {
		t.Fatal(err)
	}
	rec := srv.RenderReleaseSafetyForTest(acct, app.Slug)
	body := rec.Body.String()
	for _, want := range []string{"Production releases are frozen", "quarter close", "2 approval(s) from admins", "at most 90 minutes"} {
		if !strings.Contains(body, want) {
			t.Fatalf("guarded app page = %d, missing %q:\n%s", rec.Code, want, body)
		}
	}
	other, err := store.CreateAccount(t.Context(), "other-"+acct.ID+"@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	if rec := srv.RenderReleaseSafetyForTest(other, app.Slug); rec.Code != http.StatusNotFound {
		t.Fatalf("another account's app page = %d, want 404", rec.Code)
	}
}

func TestRollbackRestoreDatabasesIsHiddenWithoutPostgresRestore(t *testing.T) {
	f := newSwitchFixture(t)
	if err := f.env.InstallCapabilityGate(); err != nil {
		t.Fatal(err)
	}
	w := f.do(t, http.MethodPost, "/v1/apps/"+f.slug+"/rollback", api.RollbackRequest{TargetDeploymentID: &f.v1.ID, RestoreDatabases: true, AcknowledgeReleases: true})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `unknown field \"restore_databases\"`) {
		t.Fatalf("restore_databases with postgres-restore off = %d %s, want the unknown-field answer", w.Code, w.Body)
	}
}
