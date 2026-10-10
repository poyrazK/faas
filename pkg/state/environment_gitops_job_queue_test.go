package state

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

func TestGitOpsBlocksEnabledJobQueueBindingsBeforeApply(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled *bool
		blocked bool
	}{
		{name: "enabled", enabled: boolPointer(true), blocked: true},
		{name: "disabled reservation", enabled: boolPointer(false)},
		{name: "missing enablement defaults safely", blocked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resource := "workload/batch"
			out := &EnvironmentGitOpsObservation{}
			snapshot := gitOpsIntentSnapshot{EnvironmentID: uuid.NewString(), Environment: "staging", Plan: api.PlanHobby}
			desired := environmentsync.DesiredState{Definition: api.EnvironmentDefinition{Workloads: map[string]api.EnvironmentWorkload{
				"batch": {QueueBindings: map[string]api.EnvironmentQueueBinding{
					"events": {QueueName: "events", Mode: "pull", WorkloadClass: "job", Enabled: tc.enabled, MaxConcurrency: 1},
				}},
			}}}
			app := gitOpsIntentApp{ID: uuid.NewString(), Type: AppTypeApp, WorkloadClass: WorkloadClassJob}

			validateGitOpsDesiredQueues(out, snapshot, desired, resource, app)

			blocker := resource + "#queue_bindings/events: environment_job_queue_binding_execution_unsupported"
			if tc.blocked && !slices.Contains(out.State.Unsupported, blocker) {
				t.Fatalf("enabled Job queue binding did not block before apply: %+v", out.State.Unsupported)
			}
			if !tc.blocked && slices.Contains(out.State.Unsupported, blocker) {
				t.Fatalf("disabled Job queue reservation was blocked: %+v", out.State.Unsupported)
			}
		})
	}
}

func boolPointer(value bool) *bool { return &value }

func TestGitOpsDoesNotRetainAnEnabledQueueOnAJobWorkload(t *testing.T) {
	enabled, disabled := true, false
	resource := "workload/batch"
	snapshot := gitOpsIntentSnapshot{EnvironmentID: uuid.NewString(), Environment: "staging", Plan: api.PlanHobby}
	app := gitOpsIntentApp{ID: uuid.NewString(), Type: AppTypeApp, WorkloadClass: WorkloadClassJob,
		QueueBindings: []gitOpsQueueIntent{{ID: uuid.NewString(), Name: "events", Intent: api.EnvironmentQueueBinding{
			QueueName: "events", Mode: "pull", WorkloadClass: "job", Enabled: &enabled, MaxConcurrency: 1,
		}}}}

	t.Run("omitted binding remains blocked", func(t *testing.T) {
		out := &EnvironmentGitOpsObservation{}
		desired := environmentsync.DesiredState{Definition: api.EnvironmentDefinition{Workloads: map[string]api.EnvironmentWorkload{"batch": {}}}}
		validateGitOpsDesiredQueues(out, snapshot, desired, resource, app)
		blocker := resource + "#queue_bindings/events: environment_job_queue_binding_execution_unsupported"
		if !slices.Contains(out.State.Unsupported, blocker) {
			t.Fatalf("enabled retained Job queue did not block before apply: %+v", out.State.Unsupported)
		}
	})

	t.Run("reviewed disable permits retirement", func(t *testing.T) {
		out := &EnvironmentGitOpsObservation{}
		desired := environmentsync.DesiredState{Definition: api.EnvironmentDefinition{Workloads: map[string]api.EnvironmentWorkload{
			"batch": {QueueBindings: map[string]api.EnvironmentQueueBinding{"events": {
				QueueName: "events", Mode: "pull", WorkloadClass: "job", Enabled: &disabled, MaxConcurrency: 1,
			}}},
		}}}
		validateGitOpsDesiredQueues(out, snapshot, desired, resource, app)
		blocker := resource + "#queue_bindings/events: environment_job_queue_binding_execution_unsupported"
		if slices.Contains(out.State.Unsupported, blocker) {
			t.Fatalf("reviewed queue disable was blocked: %+v", out.State.Unsupported)
		}
	})
}
