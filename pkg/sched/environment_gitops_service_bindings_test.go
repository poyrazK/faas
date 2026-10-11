package sched

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAppendEnvironmentGitOpsJobServiceBindingsUsesFrozenTransport(t *testing.T) {
	intent := state.EnvironmentWorkloadIntent{
		Runtime: map[string]json.RawMessage{"service_binding_transport": json.RawMessage(`"https"`)},
		ServiceBindings: map[string]state.EnvironmentScopedServiceBinding{
			"database": {Workload: "postgres", EnvKey: "DATABASE_URL", TargetAppID: "target-id"},
		},
	}
	got, err := appendEnvironmentGitOpsJobServiceBindings(map[string]string{"GREGALE_RUN_ID": "run"}, intent)
	if err != nil {
		t.Fatal(err)
	}
	if got["DATABASE_URL"] != "https://postgres.internal" || got["GREGALE_RUN_ID"] != "run" {
		t.Fatalf("injected job environment = %#v", got)
	}
}

func TestEnvironmentGitOpsJobBindingIntentRequiresExactRunSnapshot(t *testing.T) {
	const accountID = "550e8400-e29b-41d4-a716-446655440001"
	const appID = "550e8400-e29b-41d4-a716-446655440002"
	const targetID = "550e8400-e29b-41d4-a716-446655440003"
	digest := "sha256:" + strings.Repeat("a", 64)
	image := "registry.example/reports@" + digest
	parallelism, retryMax, timeout, ramMB := 1, 0, 60, 256
	job := state.Job{ID: "job-1", AccountID: accountID, Kind: "recurring", Status: "active", ImageRef: image,
		CronSchedule: "0 3 * * *", CronTimezone: "UTC", MaxParallelism: parallelism, RetryMax: retryMax,
		TaskTimeoutS: timeout, ScheduleRevision: 1, RAMMB: ramMB, EnvOverrides: json.RawMessage(`{"MODE":"safe"}`),
		ImageMaterializationStatus: "ready", ImageResolvedDigest: digest, ImageStorageKey: "jobs/image.ext4"}
	run := state.JobRun{ID: "run-1", JobID: job.ID, AccountID: accountID, Tasks: 1, Parallelism: 1,
		ExecutionClass: "standard", FailurePolicy: "continue", RetryMax: &retryMax, TaskTimeoutS: &timeout,
		ImageRefSnapshot: image, ImageResolvedDigestSnapshot: digest, ImageStorageKeySnapshot: job.ImageStorageKey,
		RAMMBSnapshot: &ramMB, EnvOverrides: json.RawMessage(`{"MODE":"safe"}`), EffectiveEnvSnapshot: json.RawMessage(`{"MODE":"safe"}`)}
	intent := state.EnvironmentWorkloadIntent{AccountID: accountID, AppID: appID, JobID: job.ID,
		Source:    &api.EnvironmentWorkloadSource{Kind: "image", Image: image},
		Schedule:  &api.EnvironmentJobSchedule{Cron: job.CronSchedule, Timezone: job.CronTimezone},
		Variables: map[string]string{"MODE": "safe"},
		ServiceBindings: map[string]state.EnvironmentScopedServiceBinding{
			"database": {Workload: "postgres", EnvKey: "DATABASE_URL", TargetAppID: targetID},
		}}
	if !environmentGitOpsJobBindingIntentMatches(job, run, accountID, intent) {
		t.Fatal("exact reviewed scheduled-job snapshot did not match")
	}
	run.EffectiveEnvSnapshot = json.RawMessage(`{"MODE":"unreviewed"}`)
	if environmentGitOpsJobBindingIntentMatches(job, run, accountID, intent) {
		t.Fatal("unreviewed run environment was accepted for scoped bindings")
	}
}

func TestAppendEnvironmentGitOpsJobServiceBindingsRejectsEnvironmentCollision(t *testing.T) {
	intent := state.EnvironmentWorkloadIntent{ServiceBindings: map[string]state.EnvironmentScopedServiceBinding{
		"database": {Workload: "postgres", EnvKey: "DATABASE_URL", TargetAppID: "target-id"},
	}}
	if _, err := appendEnvironmentGitOpsJobServiceBindings(map[string]string{"DATABASE_URL": "user-value"}, intent); err == nil {
		t.Fatal("service binding replaced a preexisting job environment value")
	}
}

func TestValidEnvironmentGitOpsJobVariablesRejectsInvalidAndSensitiveValues(t *testing.T) {
	for name, values := range map[string]map[string]string{
		"invalid key":     {"bad-key": "value"},
		"oversized value": {"MODE": strings.Repeat("x", api.MustLimitsFor(api.PlanScale).EnvValueMaxBytes+1)},
		"sensitive key":   {"API_TOKEN": "must-not-leak"},
	} {
		t.Run(name, func(t *testing.T) {
			if validEnvironmentGitOpsJobVariables(values) {
				t.Fatal("accepted invalid or sensitive environment variables")
			}
		})
	}
	tooMany := make(map[string]string, api.MustLimitsFor(api.PlanScale).EnvVarsMax+1)
	for i := 0; i <= api.MustLimitsFor(api.PlanScale).EnvVarsMax; i++ {
		tooMany[fmt.Sprintf("MODE_%d", i)] = "safe"
	}
	if validEnvironmentGitOpsJobVariables(tooMany) {
		t.Fatal("accepted more environment variables than the platform allows")
	}
}
