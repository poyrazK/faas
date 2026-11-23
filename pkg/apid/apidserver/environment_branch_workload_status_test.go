// adr: 20261031193635
package apidserver_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A branch workload that never goes live is reported, not shown as starting
// forever: a failed branch deployment shows its error, and one still not
// live five minutes after the branch's data was ready says so.
func TestEnvironmentBranchUnservedWorkloadIsReported(t *testing.T) {
	e := apidserver.SetupSecrets(t, api.PlanPro)
	srv, store, acct := *e.S(), *e.Store(), *e.Acct()
	ctx := t.Context()
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "branch-status-web"})
	if err != nil {
		t.Fatal(err)
	}
	branch := state.ProjectEnvironmentBranch{Slug: "b1", CreatedAt: time.Now().Add(-time.Second), DataState: state.EnvironmentBranchDataReady}

	if w, err := srv.DescribeUnservedEnvironmentBranchWorkload(ctx, branch, app.ID, time.Now()); err != nil || w.DeploymentID != "" || w.Error != "" {
		t.Fatalf("no branch deployment: %+v, %v; want nothing to report", w, err)
	}
	d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "b1", Kind: state.DeploymentKindImage, ImageDigest: "sha256:branch"})
	if err != nil {
		t.Fatal(err)
	}
	if w, err := srv.DescribeUnservedEnvironmentBranchWorkload(ctx, branch, app.ID, time.Now()); err != nil || w.DeploymentID != d.ID || w.Error != "" {
		t.Fatalf("activating deployment: %+v, %v; want its id and no error yet", w, err)
	}
	late := time.Now().Add(api.EnvironmentBranchActivationStall + time.Minute)
	if w, err := srv.DescribeUnservedEnvironmentBranchWorkload(ctx, branch, app.ID, late); err != nil || !strings.Contains(w.Error, "has not gone live") {
		t.Fatalf("stalled deployment: %+v, %v; want it reported", w, err)
	}
	pending := branch
	pending.DataState = state.EnvironmentBranchDataPending
	if w, err := srv.DescribeUnservedEnvironmentBranchWorkload(ctx, pending, app.ID, late); err != nil || w.Error != "" {
		t.Fatalf("deployment waiting for data: %+v, %v; want no stall reported", w, err)
	}
	if _, err := store.SetDeploymentFailed(ctx, d.ID, api.CodeDeployFailed, "boom"); err != nil {
		t.Fatal(err)
	}
	if w, err := srv.DescribeUnservedEnvironmentBranchWorkload(ctx, branch, app.ID, time.Now()); err != nil || w.Status != "failed" || !strings.Contains(w.Error, "boom") {
		t.Fatalf("failed deployment: %+v, %v; want its failure", w, err)
	}
	reset := branch
	reset.CreatedAt = time.Now().Add(time.Minute)
	if w, err := srv.DescribeUnservedEnvironmentBranchWorkload(ctx, reset, app.ID, time.Now()); err != nil || w.DeploymentID != "" {
		t.Fatalf("an earlier incarnation's deployment: %+v, %v; want it ignored", w, err)
	}
}
