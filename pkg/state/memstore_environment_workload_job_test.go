package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemEnvironmentGitOpsJobBindingIsPausedAndRetiredWithSchedule(t *testing.T) {
	store := &MemStore{jobs: map[string]Job{}, jobRuns: map[string]JobRun{}, jobTasks: map[string]map[int]JobTask{}}
	image := "registry.example/report@sha256:" + strings.Repeat("a", 64)
	schedule := &api.EnvironmentJobSchedule{Cron: "*/5 * * * *", Timezone: "UTC"}
	workload := api.EnvironmentWorkload{Source: &api.EnvironmentWorkloadSource{Kind: "image", Image: image},
		Runtime: json.RawMessage(`{"execution_mode":"job"}`), Variables: map[string]string{"MODE": "safe"}, Schedule: schedule}
	app := gitOpsIntentApp{ID: "app-1", WorkloadClass: WorkloadClassJob, Variables: map[string]string{"MODE": "safe"}}
	row := EnvironmentWorkloadIntent{AccountID: "account-1", AppID: app.ID, EnvironmentID: "environment-1",
		Source: workload.Source, Runtime: map[string]json.RawMessage{"execution_mode": json.RawMessage(`"job"`)},
		Variables: map[string]string{"MODE": "safe"}, Schedule: schedule}

	linked, err := store.syncEnvironmentGitOpsJobLocked(row, app, workload, api.PlanHobby)
	if err != nil {
		t.Fatalf("create paused GitOps Job: %v", err)
	}
	if linked.JobID == "" {
		t.Fatal("scheduled workload did not receive a durable Job ID")
	}
	job := store.jobs[linked.JobID]
	if job.Status != "paused" || job.Kind != "recurring" || job.ImageRef != image || job.CronSchedule != schedule.Cron || job.CronTimezone != schedule.Timezone {
		t.Fatalf("GitOps Job was not created with the held reviewed contract: %+v", job)
	}
	if string(job.EnvOverrides) != `{"MODE":"safe"}` {
		t.Fatalf("GitOps Job environment = %s, want reviewed variable snapshot", job.EnvOverrides)
	}

	workload.Variables = map[string]string{"MODE": "strict"}
	linked.Variables = map[string]string{"MODE": "strict"}
	updated, err := store.syncEnvironmentGitOpsJobLocked(linked, app, workload, api.PlanHobby)
	if err != nil {
		t.Fatalf("update reviewed Job variables: %v", err)
	}
	if string(store.jobs[updated.JobID].EnvOverrides) != `{"MODE":"strict"}` || store.jobs[updated.JobID].Status != "paused" {
		t.Fatalf("updated Job did not retain the reviewed paused environment: %+v", store.jobs[updated.JobID])
	}
	linked = updated

	linked.Schedule = nil
	removed, err := store.syncEnvironmentGitOpsJobLocked(linked, app, api.EnvironmentWorkload{}, api.PlanHobby)
	if err != nil {
		t.Fatalf("remove GitOps schedule: %v", err)
	}
	if removed.JobID != "" || store.jobs[job.ID].Status != "deleted" || store.jobs[job.ID].CronSchedule != "" {
		t.Fatalf("schedule removal left the Job dispatchable or still bound: intent=%+v job=%+v", removed, store.jobs[job.ID])
	}
}

func TestMemEnvironmentGitOpsManagedJobRejectsCustomerMutation(t *testing.T) {
	store := &MemStore{jobs: map[string]Job{}, jobRuns: map[string]JobRun{}, jobTasks: map[string]map[int]JobTask{},
		appEnvironmentWorkloadIntents: map[environmentWorkloadIntentKey]EnvironmentWorkloadIntent{}}
	job, err := store.jobCreateLocked("account-1", environmentGitOpsJobName("environment-1", "app-1"), "recurring",
		"registry.example/report@sha256:"+strings.Repeat("b", 64), []string{}, 512, 300, 1, 0, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("create fixture Job: %v", err)
	}
	store.appEnvironmentWorkloadIntents[environmentWorkloadIntentKey{AppID: "app-1", EnvironmentID: "environment-1"}] = EnvironmentWorkloadIntent{JobID: job.ID}
	status := "active"
	if _, err := store.JobUpdate(t.Context(), job.ID, nil, nil, nil, nil, nil, nil, nil, &status); !errors.Is(err, ErrEnvironmentGitManaged) {
		t.Fatalf("JobUpdate error = %v, want GitOps-managed error", err)
	}
	if deleted, _, err := store.JobSoftDelete(t.Context(), job.ID); !errors.Is(err, ErrEnvironmentGitManaged) || deleted {
		t.Fatalf("JobSoftDelete = (%t, %v), want GitOps-managed error", deleted, err)
	}
}

func TestEnvironmentGitOpsScheduledJobRejectsUnsupportedEnvironmentInputs(t *testing.T) {
	image := &api.EnvironmentWorkloadSource{Kind: "image", Image: "registry.example/report@sha256:" + strings.Repeat("c", 64)}
	app := gitOpsIntentApp{ID: "app-1", WorkloadClass: WorkloadClassJob}
	intent := EnvironmentWorkloadIntent{AppID: app.ID, Source: image}
	base := api.EnvironmentWorkload{Source: image, Schedule: &api.EnvironmentJobSchedule{Cron: "0 * * * *", Timezone: "UTC"}}
	tests := []struct {
		name     string
		app      gitOpsIntentApp
		workload api.EnvironmentWorkload
		want     string
	}{
		{name: "reviewed non-secret variables", app: app, workload: api.EnvironmentWorkload{Source: image, Schedule: base.Schedule,
			Variables: map[string]string{"MODE": "safe"}}},
		{name: "reviewed secret references", app: app, workload: api.EnvironmentWorkload{Source: image, Schedule: base.Schedule,
			SecretRefs: map[string]string{"TOKEN": "secret:TOKEN"}}},
		{name: "queue bindings remain unsupported", app: app, workload: api.EnvironmentWorkload{Source: image, Schedule: base.Schedule,
			QueueBindings: map[string]api.EnvironmentQueueBinding{"events": {QueueName: "events", WorkloadClass: "job"}}},
			want: "environment_scheduled_job_queue_bindings_unsupported"},
		{name: "legacy manifest environment remains unsupported", app: gitOpsIntentApp{ID: app.ID, WorkloadClass: WorkloadClassJob,
			Manifest: AppManifest{Env: map[string]string{"MODE": "safe"}}}, workload: base,
			want: "environment_scheduled_job_legacy_environment_unsupported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := environmentGitOpsScheduledJobUnsupported(tt.app, intent, tt.workload, api.PlanHobby); got != tt.want {
				t.Fatalf("unsupported reason = %q, want %q", got, tt.want)
			}
		})
	}
}
