// adr: 20261102091500
package apphealth

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Trigger record states counted as queue evidence (pkg/sched dispatcher).
const (
	recordPending    = "pending"
	recordRetry      = "retry"
	recordDeadLetter = "dead_letter"
)

// WorkerQueue is a worker's queue evidence: the records of every trigger that
// feeds it, newest first and bounded per trigger. Known is false when a read
// failed, so a failed read never looks like an empty queue.
type WorkerQueue struct {
	Known    bool
	Triggers int
	// Waiting counts pending and retrying records; OldestWaiting is when the
	// oldest of them was received.
	Waiting       int
	OldestWaiting time.Time
	// DeadLetters counts dead-lettered records; RecentDeadLetters those last
	// dispatched within AppHealthWorkerDeadLetterWindow.
	DeadLetters       int
	RecentDeadLetters int
	// Truncated is set when a trigger had more records than the scan reads,
	// so the counts are lower bounds.
	Truncated bool
}

type workerQueueStore interface {
	ListTriggersForApp(context.Context, string) ([]sqlc.Trigger, error)
	ListTriggerRecordsForTrigger(context.Context, string, int32) ([]sqlc.TriggerRecord, error)
}

func collectWorkerQueue(ctx context.Context, store EvidenceStore, e *Evidence, now time.Time) {
	reader, ok := store.(workerQueueStore)
	if !ok {
		return
	}
	triggers, err := reader.ListTriggersForApp(ctx, e.App.ID)
	if err != nil {
		return
	}
	q := WorkerQueue{Triggers: len(triggers)}
	for _, t := range triggers {
		records, err := reader.ListTriggerRecordsForTrigger(ctx, t.ID.String(), api.AppHealthWorkerRecordScan)
		if err != nil {
			return
		}
		q.Truncated = q.Truncated || len(records) >= api.AppHealthWorkerRecordScan
		for _, r := range records {
			q.add(r, now)
		}
	}
	q.Known = true
	e.Worker = q
}

func (q *WorkerQueue) add(r sqlc.TriggerRecord, now time.Time) {
	switch r.State {
	case recordPending, recordRetry:
		q.Waiting++
		if received := r.ReceivedAt.Time; r.ReceivedAt.Valid && (q.OldestWaiting.IsZero() || received.Before(q.OldestWaiting)) {
			q.OldestWaiting = received
		}
	case recordDeadLetter:
		q.DeadLetters++
		if r.LastDispatchedAt.Valid && now.Sub(r.LastDispatchedAt.Time) <= api.AppHealthWorkerDeadLetterWindow {
			q.RecentDeadLetters++
		}
	}
}

// evaluateWorker assesses a worker pool: its releases, its running replicas
// against the pool minimum, and the queue that feeds it. A worker serves no
// HTTP requests, so readiness probes and request rates do not apply.
func evaluateWorker(out *api.AppHealthResponse, e Evidence) {
	serving := servingDeploymentsInScope(e.Live, e.Scope)
	for _, d := range serving {
		out.ServingDeploymentIDs = append(out.ServingDeploymentIDs, d.ID)
	}
	assessDeployments(out, e, serving)
	if len(serving) > 0 && e.DeploymentsKnown {
		assessWorkerReplicas(out, e, serving)
	}
	assessWorkerQueue(out, e)
}

func assessWorkerReplicas(out *api.AppHealthResponse, e Evidence, live []state.Deployment) {
	var scaling state.WorkerScaling
	if e.App.Manifest.WorkerReplicas != nil {
		scaling = *e.App.Manifest.WorkerReplicas
	}
	out.Capacity.Required = scaling.Min
	if !e.InstancesKnown {
		add(out, "replicas", Unknown, "Worker instance evidence is unavailable or exceeds the bounded scan.", "logs", "")
		return
	}
	out.Capacity.Known = true
	deployments := make(map[string]bool, len(live))
	for _, d := range live {
		deployments[d.ID] = true
	}
	for _, i := range e.Instances {
		if !deployments[i.DeploymentID] || i.Kind != "" {
			continue
		}
		switch state.State(i.State) {
		case state.StateRunning:
			out.Capacity.Ready++
		case state.StateWaking, state.StateColdBooting:
			out.Capacity.Starting++
		}
	}
	c := out.Capacity
	switch {
	case c.Ready >= scaling.Min && c.Ready > 0:
		out.Phase = "serving"
		add(out, "replicas", Pass, fmt.Sprintf("%d worker replicas are running (minimum %d).", c.Ready, scaling.Min), "", "")
	case scaling.Min == 0 && c.Starting == 0:
		out.Phase = "idle"
		add(out, "replicas", Pass, "No worker replica is running and none is required; the pool scales up when work arrives.", "", "")
	case c.Starting > 0:
		out.Phase = "starting"
		add(out, "replicas", Warning, fmt.Sprintf("%d of %d required worker replicas are running; %d starting.", c.Ready, scaling.Min, c.Starting), "logs", "")
	case c.Ready == 0:
		add(out, "replicas", Fail, fmt.Sprintf("No worker replica is running; the pool's minimum is %d.", scaling.Min), "logs", "")
		out.Checks[len(out.Checks)-1].Reason = "capacity_below_target"
	default:
		out.Phase = "serving"
		add(out, "replicas", Warning, fmt.Sprintf("%d of %d required worker replicas are running.", c.Ready, scaling.Min), "logs", "")
		out.Checks[len(out.Checks)-1].Reason = "capacity_below_target"
	}
}

func assessWorkerQueue(out *api.AppHealthResponse, e Evidence) {
	q := e.Worker
	switch {
	case !q.Known:
		add(out, "queue", Unknown, "Queue evidence for this worker is unavailable.", "logs", "")
		return
	case q.Triggers == 0:
		add(out, "queue", NotApplicable, "No trigger feeds this worker, so Gregale cannot see its backlog.", "", "")
		return
	}
	atLeast := ""
	if q.Truncated {
		atLeast = "at least "
	}
	if waited := e.Now.Sub(q.OldestWaiting); q.Waiting > 0 && !q.OldestWaiting.IsZero() && waited >= api.AppHealthWorkerBacklogAgeWarn {
		add(out, "backlog", Warning, fmt.Sprintf("%s%d messages are waiting; the oldest has waited %s.", atLeast, q.Waiting, waited.Round(time.Second)), "metrics", "")
	} else {
		add(out, "backlog", Pass, fmt.Sprintf("%s%d messages are waiting; none longer than %s.", atLeast, q.Waiting, spoken(api.AppHealthWorkerBacklogAgeWarn)), "", "")
	}
	window := spoken(api.AppHealthWorkerDeadLetterWindow)
	switch {
	case q.RecentDeadLetters > 0:
		add(out, "dead_letters", Warning, fmt.Sprintf("%s%d messages were dead-lettered in the last %s; inspect and replay them with gregale dlq.", atLeast, q.RecentDeadLetters, window), "errors", "")
	case q.DeadLetters > 0:
		add(out, "dead_letters", Pass, fmt.Sprintf("No message was dead-lettered in the last %s; %s%d older dead letters wait for replay or purge.", window, atLeast, q.DeadLetters), "", "")
	default:
		add(out, "dead_letters", Pass, fmt.Sprintf("No message was dead-lettered in the last %s.", window), "", "")
	}
}

// spoken renders a whole number of hours or minutes as a person says it.
func spoken(d time.Duration) string {
	switch {
	case d == time.Hour:
		return "hour"
	case d%time.Hour == 0:
		return fmt.Sprintf("%d hours", d/time.Hour)
	case d == time.Minute:
		return "minute"
	default:
		return fmt.Sprintf("%d minutes", d/time.Minute)
	}
}
