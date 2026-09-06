//go:build !no_pg

package state_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRetryDeployment_PreservesInputsAndQueues(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
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
	customStages := json.RawMessage(`[{"percent":5,"duration":"1m"},{"percent":50,"duration":"1m"},{"percent":100,"duration":"0s"}]`)
	oldStarted := time.Now().UTC().Add(-time.Hour)
	if _, err := pool.Exec(ctx, `
		update deployments
		   set rollback_on_5xx = true,
		       reason = 'retry after dependency recovery',
		       tag = 'hotfix',
		       deployed_by = 'Release Operator',
		       pr_number = 1419,
		       priority = 7,
		       canary_preset = 'custom',
		       canary_step = 2,
		       canary_total_steps = 3,
		       canary_step_started_at = $2,
		       canary_stages = $3,
		       rollout_state = 'rolling_out',
		       rollout_started_at = $2,
		       traffic_percent = 50
		 where id = $1`, original.ID, oldStarted, customStages); err != nil {
		t.Fatal(err)
	}
	original, err = s.DeploymentByID(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(original.CanaryStages, customStages) {
		t.Fatalf("deployment projection lost custom canary stages: %s", original.CanaryStages)
	}
	retry, err := s.RetryDeploymentFromStage(ctx, original.ID, state.StageSecurityScan)
	if err != nil {
		t.Fatal(err)
	}
	if retry.SourcePath != original.SourcePath || retry.SourceRoot != original.SourceRoot || !retry.FullRootfsAllowAuto || retry.FullRootfsOverride == nil || !*retry.FullRootfsOverride || string(retry.Workflows) != string(original.Workflows) {
		t.Fatalf("retry lost input settings: %+v", retry)
	}
	if !retry.RollbackOn5xx || retry.Reason != original.Reason || retry.Tag != original.Tag || retry.DeployedBy != original.DeployedBy || retry.PRNumber != original.PRNumber || retry.Priority != original.Priority {
		t.Fatalf("retry lost policy or annotation metadata: %+v", retry)
	}
	if retry.CanaryPreset != "custom" || retry.CanaryStep != 0 || retry.CanaryTotalSteps != 3 || retry.TrafficPercent != 5 || !jsonEqual(retry.CanaryStages, customStages) {
		t.Fatalf("retry did not restart custom canary: %+v", retry)
	}
	if retry.CanaryStepStartedAt == nil || !retry.CanaryStepStartedAt.After(oldStarted) || retry.RolloutState != "pending" || retry.RolloutStartedAt != nil {
		t.Fatalf("retry did not reset execution timers: %+v", retry)
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

func jsonEqual(a, b []byte) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}
