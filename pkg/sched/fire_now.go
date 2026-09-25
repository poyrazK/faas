// pkg/sched/fire_now.go — schedd's fire-now dispatcher (ADR-090 PR-C/D).
//
// Producer: apid's POST /v1/crons/{id}/run handler inserts a row into
// cron_fire_now_requests (migrations/00193, status='pending') and emits
// db.NotifyCronRunNow on the wire.
//
// Consumer: loop.go's Run() multiplexes db.NotifyCronRunNow onto its
// existing LISTEN (pkg/sched/loop.go:348-352) plus a 60s safety ticker
// (pkg/sched/loop.go:fireNowT). Both wakeup sources call
// drainPendingFireNowRequests, the row-level dispatcher. This avoids
// a dedicated long-term pool connection (the original PR-C design
// added one — it tipped schedd's pool.MaxConns=8 over the edge and
// starved the async-invoke drain under the e2e harness query burst).
//
// Per-row dispatch flow:
//
//   - drainPendingFireNowRequests calls ClaimPendingFireNowRequest
//     which atomically transitions one pending row to `running` via
//     FOR UPDATE SKIP LOCKED LIMIT 1.
//   - Calls (*Loop).RunCronNow for HTTP crons or creates a durable
//     deployment-attached task for command crons.
//   - Stamps the row's terminal state via MarkFireNowRequestSucceeded
//     / MarkFireNowRequestFailed.

package sched

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// fireNowSafetyTick is the recovery cadence for missed-notify scenarios.
// When a NotifyCronRunNow delivery is dropped (Postgres bounce, network
// blip, schedd restart), the pending rows in the table survive — the
// safety tick re-claims them. 60s matches cronT's cadence; a customer
// waiting on a fire-now that dropped during the blip waits up to 60s
// for recovery. Acceptable for an operator-initiated action.
const fireNowSafetyTick = 60 * time.Second

// Histogram label values for sched_cron_fire_now_dispatch_duration_seconds.
// Pre-declared so goconst stops flagging the repeated literals across
// the switch arms in processFireNowRequest (the file has 4 resultLabel
// assignments to "failed" and one to "succeeded").
const (
	fireNowResultLabelSucceeded = "succeeded"
	fireNowResultLabelFailed    = "failed"
)

// drainPendingFireNowRequests claims + dispatches every pending row,
// one at a time. A single drain call claims all currently-pending
// rows sequentially; a follow-up notify or tick will pick up anything
// that arrives mid-drain. The single-row LIMIT is the contention
// primitive — N schedd instances process N rows in parallel without
// colliding.
//
// The function does not return an error; per-row failures are logged
// and the row is stamped to `failed` so a poison row cannot wedge the
// queue forever.
func (l *Loop) drainPendingFireNowRequests(ctx context.Context) {
	for {
		req, err := l.engine.Store().ClaimPendingFireNowRequest(ctx)
		if errors.Is(err, state.ErrFireNowRequestNotFound) {
			return // empty queue — caller exits cleanly
		}
		if err != nil {
			l.log.Warn("sched: fire_now: claim failed", "err", err)
			return // transient error; the safety tick retries
		}
		l.processFireNowRequest(ctx, req)
	}
}

// processFireNowRequest is the per-row dispatch. Loads the cron
// (defence in depth — the cron may have been disabled/deleted between
// INSERT and claim), dispatches HTTP crons via RunCronNow and command
// crons via the app-task queue, then stamps the terminal state.
// Errors are mapped to status: ErrCronDisabled / ErrAccountSuspended /
// ErrNoCapacity → failed; anything else → failed with the err.Error()
// text capped at 1 KB.
func (l *Loop) processFireNowRequest(ctx context.Context, req state.FireNowRequest) {
	if cron, err := l.engine.Store().CronByID(ctx, req.CronID); err == nil && len(cron.Command) > 0 {
		l.processCommandCronFireNow(ctx, req, cron)
		return
	}

	run, err := l.RunCronNow(ctx, req.CronID, req.AccountID)

	// fireNowDispatchDuration: issue #791 PR-D / ADR-090 §"Sub-decision
	// 7". One observation per terminal row, sized in seconds since
	// the row was inserted at apid (req.RequestedAt). The observer is
	// nil-safe — schedd's existing /metrics surface catches the
	// nil-ops path silently. We compute elapsed BEFORE the mark*
	// call so the histogram captures the wall-clock latency the
	// customer actually waited, not the time to stamp the row.
	elapsed := time.Since(req.RequestedAt).Seconds()
	resultLabel := fireNowResultLabelSucceeded

	switch {
	case err == nil && run.Success:
		// Best-effort: RunCronNow returns CronRun{Success: true} with
		// InvocationID="" when the dispatch path took the
		// "enqueued" route (the cron tick + invoke are decoupled). We
		// still mark succeeded because the fire was accepted — the
		// invocation_id, when present, lets the customer correlate
		// the audit row with /v1/invocations/{id}.
		if err := l.engine.Store().MarkFireNowRequestSucceeded(ctx, req.ID, run.InvocationID); err != nil {
			l.log.Warn("sched: fire_now: mark succeeded failed",
				"request_id", req.ID, "err", err)
		}
		l.log.Info("sched: fire_now: succeeded", "request_id", req.ID, "cron_id", req.CronID)

	case err == nil && !run.Success:
		// Issue #791 PR-D code-review: previously collapsed into the
		// `err == nil` branch which stamped the row succeeded even
		// when the audit row recorded status="err" (e.g. Wake failed
		// downstream). RunCronNow now propagates dispatchCronLocked's
		// fireSucceeded, so a successful *admit* that ends in a
		// failed *invoke* lands here. Stamp the row failed with a
		// bounded message so the GET /v1/cron-fire-now-requests/{id}
		// surface agrees with the audit row.
		const dispatchFailedMsg = "dispatch: invocation did not complete"
		if mErr := l.engine.Store().MarkFireNowRequestFailed(ctx, req.ID, dispatchFailedMsg); mErr != nil {
			l.log.Warn("sched: fire_now: mark failed", "request_id", req.ID, "err", mErr)
		}
		l.log.Warn("sched: fire_now: dispatch admitted but invoke failed",
			"request_id", req.ID, "cron_id", req.CronID)
		resultLabel = fireNowResultLabelFailed

	case errors.Is(err, ErrCronDisabled):
		if mErr := l.engine.Store().MarkFireNowRequestFailed(ctx, req.ID, "cron disabled"); mErr != nil {
			l.log.Warn("sched: fire_now: mark failed", "request_id", req.ID, "err", mErr)
		}
		l.log.Info("sched: fire_now: cron disabled", "request_id", req.ID)
		resultLabel = fireNowResultLabelFailed

	case errors.Is(err, ErrAccountSuspended):
		if mErr := l.engine.Store().MarkFireNowRequestFailed(ctx, req.ID, "account suspended"); mErr != nil {
			l.log.Warn("sched: fire_now: mark failed", "request_id", req.ID, "err", mErr)
		}
		l.log.Info("sched: fire_now: account suspended", "request_id", req.ID)
		resultLabel = fireNowResultLabelFailed

	default:
		// Unknown error. Stamp as failed with the err string so the
		// customer's `GET /v1/crons/{id}/runs` shows the failure
		// reason. MarkFireNowRequestFailed caps the string at 1 KB.
		msg := err.Error()
		if mErr := l.engine.Store().MarkFireNowRequestFailed(ctx, req.ID, msg); mErr != nil {
			l.log.Warn("sched: fire_now: mark failed", "request_id", req.ID, "err", mErr)
		}
		l.log.Warn("sched: fire_now: dispatch error",
			"request_id", req.ID, "cron_id", req.CronID, "err", err)
		resultLabel = fireNowResultLabelFailed
	}

	// Emit the histogram observation in a defer-safe location: the
	// resultLabel is captured by the switch above; observing here
	// means the histogram ALWAYS records one entry per row processed
	// (matching the slog.Info / slog.Warn invariant above — observability
	// pins that the lifecycle emits exactly one metric per row).
	if obs := l.ops.CronFireNowDispatchDuration(resultLabel); obs != nil {
		obs.Observe(elapsed)
	}
}

// processCommandCronFireNow queues a deployment-attached task for a manual
// fire. The state store atomically writes the task and marks the fire-now
// request succeeded with its task id; the task itself runs through the
// existing app-task coordinator and command-cron retry lifecycle.
func (l *Loop) processCommandCronFireNow(ctx context.Context, req state.FireNowRequest, cron state.Cron) {
	elapsed := time.Since(req.RequestedAt).Seconds()
	resultLabel := fireNowResultLabelFailed
	defer func() {
		if obs := l.ops.CronFireNowDispatchDuration(resultLabel); obs != nil {
			obs.Observe(elapsed)
		}
	}()

	markFailed := func(err error) {
		if markErr := l.engine.Store().MarkFireNowRequestFailed(ctx, req.ID, err.Error()); markErr != nil {
			l.log.Warn("sched: fire_now: mark command cron failed", "request_id", req.ID, "err", markErr)
		}
		l.log.Warn("sched: fire_now: command cron dispatch failed",
			"request_id", req.ID, "cron_id", req.CronID, "err", err)
	}
	if !cron.Enabled {
		markFailed(ErrCronDisabled)
		return
	}
	if cron.SuspendedReason != "" {
		markFailed(state.ErrAppTaskCronSuspended)
		return
	}
	app, err := l.engine.Store().AppByID(ctx, cron.AppID)
	if err != nil {
		markFailed(err)
		return
	}
	if app.AccountID != req.AccountID {
		markFailed(state.ErrNotFound)
		return
	}
	account, err := l.engine.Store().AccountByID(ctx, app.AccountID)
	if err != nil {
		markFailed(err)
		return
	}
	if !account.Active() {
		markFailed(ErrAccountSuspended)
		return
	}
	taskStore, ok := l.engine.Store().(state.AppTaskStore)
	if !ok {
		markFailed(errors.New("app task store is unavailable"))
		return
	}
	firedAt := l.now()
	task, err := taskStore.CreateManualCronAppTaskForFireNow(ctx, req.ID, firedAt)
	if err != nil {
		if errors.Is(err, state.ErrAppTaskDeploymentUnavailable) {
			if suspender, ok := l.engine.Store().(state.CronSuspensionStore); ok {
				if _, suspendErr := suspender.SuspendCronsForApp(ctx, cron.AppID, state.CronSuspendedNoLiveDeployment); suspendErr != nil {
					l.log.Warn("sched: fire_now: suspend command cron without live deployment", "cron_id", cron.ID, "err", suspendErr)
				}
			}
		}
		l.emitCommandCronFired(ctx, cron, account.ID, firedAt, "err", "", TriggerManual)
		markFailed(err)
		return
	}
	l.emitCommandCronFired(ctx, cron, account.ID, firedAt, "ok", task.ID, TriggerManual)
	l.log.Info("sched: fire_now: command cron task queued",
		"request_id", req.ID, "cron_id", cron.ID, "task_id", task.ID, "deployment_id", task.DeploymentID)
	resultLabel = fireNowResultLabelSucceeded
}

// _ = slog.Default // keep the slog import in case future logging
// helpers want a default logger; remove on next edit if unused.
var _ = slog.Default
