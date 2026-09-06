//go:build !no_pg

package state_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRetryDeployment_PreservesInputsAndQueues(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "retry-pg@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "retry-pg", Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	force := true
	original, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindDockerfile, SourcePath: "/var/spool/faas/retained.tar.gz", SourceRoot: "service", SourceBytes: 1024, FullRootfsAllowAuto: true, FullRootfsOverride: &force, Workflows: json.RawMessage(`[{"name":"retained"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FailSourceDeployment(ctx, original.ID, "test failure"); err != nil {
		t.Fatal(err)
	}
	retry, err := s.RetryDeploymentFromStage(ctx, original.ID, state.StageSecurityScan)
	if err != nil {
		t.Fatal(err)
	}
	if retry.SourcePath != original.SourcePath || retry.SourceRoot != original.SourceRoot || !retry.FullRootfsAllowAuto || retry.FullRootfsOverride == nil || !*retry.FullRootfsOverride || string(retry.Workflows) != string(original.Workflows) {
		t.Fatalf("retry lost input settings: %+v", retry)
	}
	var stages state.StageState
	if err := json.Unmarshal(retry.StageState, &stages); err != nil {
		t.Fatal(err)
	}
	if stages.Current != state.StageSourceDownload || stages.RetryRequestedStage != state.StageSecurityScan {
		t.Fatalf("stages=%+v", stages)
	}
	build, err := s.CreateBuildWithID(ctx, uuid.NewString(), retry.ID, retry.Kind, retry.SourceBytes, "/tmp/retry-build.log")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.BuildByDeployment(ctx, retry.ID)
	if err != nil || got.ID != build.ID || got.Status != state.BuildQueued {
		t.Fatalf("build=%+v err=%v", got, err)
	}
	old, err := s.DeploymentByID(ctx, original.ID)
	if err != nil || old.Status != state.DeployFailed {
		t.Fatalf("original changed: %+v %v", old, err)
	}
}
