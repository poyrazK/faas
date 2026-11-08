// adr: 20261102091500
package apphealth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func workerEvidence() Evidence {
	e := healthEvidence()
	e.App.Manifest.ExecutionMode = api.ExecutionModeWorker
	e.App.Manifest.WorkerReplicas = &state.WorkerScaling{Min: 1, Max: 3}
	e.AssessWorkers = true
	e.Worker = WorkerQueue{Known: true, Triggers: 1}
	return e
}

func checkStatus(out api.AppHealthResponse, code string) string {
	for _, c := range out.Checks {
		if c.Code == code {
			return c.Status
		}
	}
	return ""
}

// A worker is assessed on its releases, its running replicas against the
// pool minimum and the queue that feeds it (ADR-20261102091500).
func TestWorkerHealth(t *testing.T) {
	for _, tt := range []struct {
		name         string
		change       func(*Evidence)
		status       string
		phase        string
		check, state string
	}{
		{"healthy pool", func(*Evidence) {}, Healthy, "serving", "replicas", Pass},
		{"below minimum", func(e *Evidence) { e.App.Manifest.WorkerReplicas.Min = 2 }, Degraded, "serving", "replicas", Warning},
		{"none running", func(e *Evidence) { e.Instances = nil }, Unhealthy, "unknown", "replicas", Fail},
		{"starting", func(e *Evidence) { e.Instances[0].State = string(state.StateColdBooting) }, Degraded, "starting", "replicas", Warning},
		{"scaled to zero", func(e *Evidence) { e.Instances = nil; e.App.Manifest.WorkerReplicas.Min = 0 }, Healthy, "idle", "replicas", Pass},
		{"no scaling policy", func(e *Evidence) { e.Instances = nil; e.App.Manifest.WorkerReplicas = nil }, Healthy, "idle", "replicas", Pass},
		{"instances unread", func(e *Evidence) { e.InstancesKnown = false }, Unknown, "unknown", "replicas", Unknown},
		{"old backlog", func(e *Evidence) {
			e.Worker.Waiting, e.Worker.OldestWaiting = 40, e.Now.Add(-api.AppHealthWorkerBacklogAgeWarn-time.Minute)
		}, Degraded, "serving", "backlog", Warning},
		{"fresh backlog", func(e *Evidence) { e.Worker.Waiting, e.Worker.OldestWaiting = 40, e.Now.Add(-time.Minute) }, Healthy, "serving", "backlog", Pass},
		{"recent dead letters", func(e *Evidence) { e.Worker.DeadLetters, e.Worker.RecentDeadLetters = 3, 2 }, Degraded, "serving", "dead_letters", Warning},
		{"old dead letters", func(e *Evidence) { e.Worker.DeadLetters = 3 }, Healthy, "serving", "dead_letters", Pass},
		{"no trigger", func(e *Evidence) { e.Worker.Triggers = 0 }, Healthy, "serving", "queue", NotApplicable},
		{"queue unread", func(e *Evidence) { e.Worker = WorkerQueue{} }, Unknown, "serving", "queue", Unknown},
		{"failed latest release", func(e *Evidence) {
			failed := state.Deployment{ID: "failed", AppID: "app", Status: state.DeployFailed, Scope: "default"}
			e.Latest = &failed
		}, Degraded, "serving", "latest_deployment", Warning},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := workerEvidence()
			tt.change(&e)
			out := Evaluate(e)
			if out.Status != tt.status || out.Phase != tt.phase || checkStatus(out, tt.check) != tt.state {
				t.Fatalf("got %s/%s %s=%s, want %s/%s %s=%s: %+v", out.Status, out.Phase, tt.check, checkStatus(out, tt.check),
					tt.status, tt.phase, tt.check, tt.state, out.Checks)
			}
			if checkStatus(out, "readiness") != "" || out.Requests != nil {
				t.Fatalf("a worker was assessed on HTTP readiness or requests: %+v", out)
			}
		})
	}
}

type workerQueueFake struct {
	triggers    []sqlc.Trigger
	records     map[string][]sqlc.TriggerRecord
	triggersErr error
}

func (f *workerQueueFake) LiveDeployments(context.Context, string) ([]state.Deployment, error) {
	return nil, nil
}

func (f *workerQueueFake) ListDeploymentsForApp(context.Context, string, int, int) ([]state.Deployment, error) {
	return nil, nil
}

func (f *workerQueueFake) ComputeNodeByID(context.Context, string) (state.ComputeNode, error) {
	return state.ComputeNode{}, nil
}

func (f *workerQueueFake) ListTriggersForApp(context.Context, string) ([]sqlc.Trigger, error) {
	return f.triggers, f.triggersErr
}

func (f *workerQueueFake) ListTriggerRecordsForTrigger(_ context.Context, id string, limit int32) ([]sqlc.TriggerRecord, error) {
	if limit != api.AppHealthWorkerRecordScan {
		return nil, errors.New("unbounded scan")
	}
	return f.records[id], nil
}

func at(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func TestCollectWorkerQueue(t *testing.T) {
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	trigger := sqlc.Trigger{ID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}}
	f := &workerQueueFake{triggers: []sqlc.Trigger{trigger}, records: map[string][]sqlc.TriggerRecord{trigger.ID.String(): {
		{State: recordPending, ReceivedAt: at(now.Add(-time.Minute))},
		{State: recordRetry, ReceivedAt: at(now.Add(-20 * time.Minute))},
		{State: "succeeded", ReceivedAt: at(now.Add(-2 * time.Hour))},
		{State: recordDeadLetter, LastDispatchedAt: at(now.Add(-10 * time.Minute))},
		{State: recordDeadLetter, LastDispatchedAt: at(now.Add(-3 * time.Hour))},
	}}}
	var e Evidence
	collectWorkerQueue(t.Context(), f, &e, now)
	want := WorkerQueue{Known: true, Triggers: 1, Waiting: 2, OldestWaiting: now.Add(-20 * time.Minute), DeadLetters: 2, RecentDeadLetters: 1}
	if e.Worker != want {
		t.Fatalf("queue = %+v, want %+v", e.Worker, want)
	}

	f.records[trigger.ID.String()] = make([]sqlc.TriggerRecord, api.AppHealthWorkerRecordScan)
	e = Evidence{}
	collectWorkerQueue(t.Context(), f, &e, now)
	if !e.Worker.Truncated {
		t.Fatal("a full scan was not marked as a lower bound")
	}

	f.triggersErr = errors.New("down")
	e = Evidence{}
	collectWorkerQueue(t.Context(), f, &e, now)
	if e.Worker.Known {
		t.Fatal("a failed read looked like an empty queue")
	}
}

func TestWorkerHealthWording(t *testing.T) {
	e := workerEvidence()
	e.Worker.Truncated, e.Worker.RecentDeadLetters, e.Worker.DeadLetters = true, 1000, 1000
	out := Evaluate(e)
	if !strings.Contains(out.Summary, "at least 1000 messages were dead-lettered in the last hour") {
		t.Fatalf("summary = %q", out.Summary)
	}
	if got := spoken(10 * time.Minute); got != "10 minutes" {
		t.Fatalf("spoken = %q", got)
	}
}
