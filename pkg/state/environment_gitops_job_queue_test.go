package state_test

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
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
			out := &state.EnvironmentGitOpsObservation{}
			snapshot := state.GitOpsIntentSnapshot{EnvironmentID: uuid.NewString(), Environment: "staging", Plan: api.PlanHobby}
			desired := environmentsync.DesiredState{Definition: api.EnvironmentDefinition{Workloads: map[string]api.EnvironmentWorkload{
				"batch": {QueueBindings: map[string]api.EnvironmentQueueBinding{
					"events": {QueueName: "events", Mode: "pull", WorkloadClass: "job", Enabled: tc.enabled, MaxConcurrency: 1},
				}},
			}}}
			app := state.GitOpsIntentApp{ID: uuid.NewString(), Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassJob}

			state.ValidateGitOpsDesiredQueues(out, snapshot, desired, resource, app)

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
	snapshot := state.GitOpsIntentSnapshot{EnvironmentID: uuid.NewString(), Environment: "staging", Plan: api.PlanHobby}
	app := state.GitOpsIntentApp{ID: uuid.NewString(), Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassJob,
		QueueBindings: []state.GitOpsQueueIntent{{ID: uuid.NewString(), Name: "events", Intent: api.EnvironmentQueueBinding{
			QueueName: "events", Mode: "pull", WorkloadClass: "job", Enabled: &enabled, MaxConcurrency: 1,
		}}}}

	t.Run("omitted binding remains blocked", func(t *testing.T) {
		out := &state.EnvironmentGitOpsObservation{}
		desired := environmentsync.DesiredState{Definition: api.EnvironmentDefinition{Workloads: map[string]api.EnvironmentWorkload{"batch": {}}}}
		state.ValidateGitOpsDesiredQueues(out, snapshot, desired, resource, app)
		blocker := resource + "#queue_bindings/events: environment_job_queue_binding_execution_unsupported"
		if !slices.Contains(out.State.Unsupported, blocker) {
			t.Fatalf("enabled retained Job queue did not block before apply: %+v", out.State.Unsupported)
		}
	})

	t.Run("reviewed disable permits retirement", func(t *testing.T) {
		out := &state.EnvironmentGitOpsObservation{}
		desired := environmentsync.DesiredState{Definition: api.EnvironmentDefinition{Workloads: map[string]api.EnvironmentWorkload{
			"batch": {QueueBindings: map[string]api.EnvironmentQueueBinding{"events": {
				QueueName: "events", Mode: "pull", WorkloadClass: "job", Enabled: &disabled, MaxConcurrency: 1,
			}}},
		}}}
		state.ValidateGitOpsDesiredQueues(out, snapshot, desired, resource, app)
		blocker := resource + "#queue_bindings/events: environment_job_queue_binding_execution_unsupported"
		if slices.Contains(out.State.Unsupported, blocker) {
			t.Fatalf("reviewed queue disable was blocked: %+v", out.State.Unsupported)
		}
	})
}
