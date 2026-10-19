package sched

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// scheduleRealtimeLanes submits this tick's lanes for one realtime trigger
// (ADR-1322). Each lane carries one message; the bound group serializes
// messages that share an ordering key, so lanes only run different keys in
// parallel. The lane allowance grows after each lane that reaches the
// gateway and halves after a gateway failure, as for push queues.
func (l *Loop) scheduleRealtimeLanes(ctx context.Context, t sqlc.Trigger, store storeLike) {
	triggerID := t.ID.String()
	var cfg api.RealtimeTriggerConfig
	_ = json.Unmarshal(t.Config, &cfg)
	ceiling := min(cfg.Concurrency(), api.QueuePushDispatchSlotsPerNode)
	if limits, ok := api.LimitsFor(l.triggerPlanResolver(ctx)(t.AppID.String())); ok && limits.MaxConcurrency > 0 {
		ceiling = min(ceiling, limits.MaxConcurrency)
	}
	lanes := max(min(l.queuePushLanes.current(triggerID), ceiling), 1)
	for lane := range lanes {
		l.submitWork(workQueuePushDispatch, fmt.Sprintf("%s#%d", triggerID, lane), func() {
			err := l.dispatchOneTrigger(ctx, t, store, l.triggerPlanResolver(ctx))
			gatewayFailed := errors.Is(err, errTriggerGatewayDispatch)
			l.queuePushLanes.observe(triggerID, ceiling, gatewayFailed)
			if err != nil && !gatewayFailed {
				l.log.Warn("sched trigger tick: realtime lane", "trigger_id", triggerID, "lane", lane, "err", err)
			}
		})
	}
}

// finishRealtimeDispatch applies the gateway's per-record results for a
// realtime trigger. Successes complete the ledger claim and the group
// message. Every other outcome goes back to the group with the gateway's
// code; the group's retry policy decides whether and when it is retried.
func (l *Loop) finishRealtimeDispatch(ctx context.Context, t sqlc.Trigger, rt *realtimePoller,
	claimed []sqlc.TriggerRecord, statusByID map[string]triggerDispatchResult, store storeLike) error {
	succeeded := make([]string, 0, len(claimed))
	failed := make(map[string]string)
	for _, c := range claimed {
		result, found := statusByID[c.ItemIdentifier]
		switch {
		case !found:
			failed[c.ItemIdentifier] = "missing_dispatch_result"
		case result.Status == "succeeded":
			// The handler ran, so the group message completes even if a
			// recovered ledger claim makes this transition fail.
			if err := completeTriggerClaim(ctx, store, c); err != nil {
				l.log.Warn("sched realtime trigger: mark succeeded", "id", c.ID.String(), "err", err)
			}
			succeeded = append(succeeded, c.ItemIdentifier)
			l.emitRealtimeFired(ctx, t, c.ID.String())
		default:
			failed[c.ItemIdentifier] = realtimeResultCode(result)
		}
	}
	l.observeESMRecordOutcome(t.Kind, wire.ESMRecordOutcomeSucceeded, len(succeeded))
	l.observeESMRecordOutcome(t.Kind, wire.ESMRecordOutcomeRetry, len(failed))
	l.observeESMRecordProcessingBatch(t.Kind, claimed)
	if err := rt.Ack(ctx, t, succeeded); err != nil {
		l.log.Warn("sched realtime trigger: complete", "trigger_id", t.ID.String(), "err", err)
	}
	if len(failed) > 0 {
		if err := rt.NackWithCodes(ctx, t, failed); err != nil {
			l.log.Warn("sched realtime trigger: return to group", "trigger_id", t.ID.String(), "err", err)
		}
	}
	l.emitAudit(ctx, events.TriggerFiredBatchEvent{
		TriggerID: t.ID.String(), BatchSize: len(claimed), AttemptTotal: len(claimed),
		SucceededTotal: len(succeeded), FailedTotal: len(failed),
	})
	return nil
}

// returnRealtimeBatch hands a whole claimed batch back to the group with one
// code, for the transport and malformed-response paths.
func (l *Loop) returnRealtimeBatch(ctx context.Context, t sqlc.Trigger, rt *realtimePoller, claimed []sqlc.TriggerRecord, code string) {
	codes := make(map[string]string, len(claimed))
	for _, c := range claimed {
		codes[c.ItemIdentifier] = code
	}
	l.observeESMRecordOutcome(t.Kind, wire.ESMRecordOutcomeRetry, len(claimed))
	l.observeESMRecordProcessingBatch(t.Kind, claimed)
	if err := rt.NackWithCodes(ctx, t, codes); err != nil {
		l.log.Warn("sched realtime trigger: return batch to group", "trigger_id", t.ID.String(), "code", code, "err", err)
	}
}

func realtimeResultCode(result triggerDispatchResult) string {
	if result.Code != "" {
		return result.Code
	}
	if result.Status != "" {
		return "result_" + result.Status
	}
	return "handler_failed"
}

func (l *Loop) emitRealtimeFired(ctx context.Context, t sqlc.Trigger, recordID string) {
	now := time.Now()
	l.emitAuditDual(ctx,
		events.TriggerFiredEvent{TriggerID: t.ID.String(), RecordID: recordID, AppID: t.AppID.String(), FiredAt: now},
		events.ESMSourceCreatedEvent{TriggerID: t.ID.String(), AppID: t.AppID.String(), SourceKind: t.Kind, EmitAt: now},
		t.ID.String(), t.AppID.String(), t.Kind,
	)
}
