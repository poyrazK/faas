// dispatch_triggers.go — trigger dispatch tick
// (issue #757 / ADR-0NN, commit #14 of feat-triggers-mega).
//
// runTriggerTick is the sibling of runCronTick (loop.go:1808):
// 1-second cadence, walks every enabled trigger, polls its broker
// adapter, evaluates per-record FilterCriteria (ADR-118 / commit 6
// of the issue #757 mega-PR), claims the per-record FSM rows via
// ClaimTriggerRecords (FOR UPDATE SKIP LOCKED), batches the records
// (size + 6MB cap), posts the batch envelope to the gateway,
// parses per-record status, and transitions trigger_records rows
// through the FSM.
//
// FSM per record:
//
//	pending ── claim ─▶ claimed ── succeeded
//	                              ── retry ─▶ retry (next_fire_at=future)
//	                                            └▶ attempts >= max ─▶ dead_letter
//	                              ── poison_record ─▶ dead_letter
//
// FilterCriteria evaluation (commit 6):
//
//	For each polled SourceRecord, decode the trigger's
//	triggers.filter_criteria JSONB column into a *FilterCriteria
//	(closed-vocab per pkg/gregalemanifest + pkg/api) and call
//	FilterCriteria.Match(payload, headers). Three outcomes:
//
//	  - match == true:  record flows through InsertTriggerRecord
//	                    + ClaimTriggerRecords as today.
//	  - match == false: record is Ack'd at the broker (so the
//	                    offset is committed and the message is
//	                    NOT redelivered) and DROPPED — no
//	                    trigger_records row, no audit row. The
//	                    skip is silent by design (ADR-118
//	                    §"Skip audit policy"); the customer
//	                    author of the filter chose this path.
//	  - match error:    record is Ack'd (the parse error is in
//	                    the filter AUTHOR not the record;
//	                    redelivery would loop on the same error)
//	                    and a single trigger.filter_error audit
//	                    row is emitted (operator-debug,
//	                    NOT customer-facing).
//
// Why Ack + drop (not Nack + retry): a filter that
// always-rejects the same record is a customer-side misconfig,
// not a transient failure. Nack would loop forever; Ack commits
// the offset and lets the customer fix the filter without the
// broker replaying the record.
//
// Concurrency: the Loop's mutex protects the cron tick. We use
// the same mutex here so two ticks don't race on the same
// trigger's pollers (the broker library is goroutine-safe but the
// per-trigger in-flight map is not).
//
// Rate-limit gate: AllowWakeApp + AllowWakeAccount, AND semantics
// per pkg/sched/rate_limit.go:113-150. Deny path lifts to
// trigger.dlq audit + dead_letter row per record (NOT a transient
// 429 like the wake path — triggers retry on next dispatch tick).
//
// Dual-emit (ADR-118 §"Audit vocabulary bridging"):
//
//	Every trigger.* event emitted from this file is paired with
//	the corresponding esm.* operator alias. The two rows land
//	in the events table with identical payload; consumers
//	that want one timeline can join on (trigger_id, record_id,
//	at). Adding a new dual-emit pair requires (a) a
//	trigger.* event + (b) an esm.* event in pkg/events, both
//	emitted from the same call site.

package sched

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dispatch"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/triggerconfig"
	"github.com/onebox-faas/faas/pkg/wire"
)

const gatewayDispatchResponseMaxBytes = 1 << 20

// trigger DLQ reason constants (match the CHECK on
// trigger_dead_letter.reason in migrations/00297_triggers.sql).
// CI lint rule goconst would otherwise flag the per-reason
// comparisons in classifyDLQReason() + the path-through call
// sites as duplicates.
const (
	triggerReasonPoisonRecord    = "poison_record"
	triggerReasonMaxAttempts     = "max_attempts"
	triggerReasonBrokerError     = "broker_error"
	triggerReasonRateLimited     = "rate_limited"
	triggerReasonPayloadTooLarge = "payload_too_large"
)

// triggerDispatchRecord is one broker-delivered record the
// dispatch tick packages for the gateway batch envelope.
type triggerDispatchRecord struct {
	ItemIdentifier string            `json:"item_identifier"`
	PayloadB64     string            `json:"payload_b64"`
	Headers        map[string]string `json:"headers"`
	Metadata       map[string]any    `json:"metadata"`
}

// triggerDispatchRequest is the JSON body posted to
// /v1/invocations:dispatch_batch on the gateway (commit #13).
type triggerDispatchRequest struct {
	InvocationID string                  `json:"invocation_id"`
	AppID        string                  `json:"app_id"`
	Source       string                  `json:"source"`
	TriggerID    string                  `json:"trigger_id"`
	Records      []triggerDispatchRecord `json:"records"`
}

// triggerDispatchResponse mirrors pkg/gateway/synth.go's batch
// response shape.
type triggerDispatchResponse struct {
	Results []triggerDispatchResult `json:"results"`
}

// triggerDispatchResult mirrors one batch record's outcome.
type triggerDispatchResult struct {
	ItemIdentifier string `json:"item_identifier"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	// Code (audit #8) is the structured counterpart to Error —
	// a stable machine-readable string the dispatch tick
	// switches on instead of substring-matching Error. Mirrors
	// pkg/gateway/synth.go::batchDispatchResult.Code.
	Code string `json:"code,omitempty"`
}

// loopStoreAccessor returns the store the Loop uses. The store
// lives on Loop.engine.store; for commit #14 we go through a
// tiny helper so callers in this file don't reach into engine
// internals.
func (l *Loop) loopStore() storeLike {
	if l.engine != nil {
		return l.engine.store
	}
	return nil
}

// storeLike is the trimmed interface this file needs from the
// store. Defined inline to keep dispatch_triggers.go free of a
// hard dependency on pkg/state.
type storeLike interface {
	ListEnabledTriggers(ctx context.Context) ([]sqlc.Trigger, error)
	ClaimTriggerRecords(ctx context.Context, triggerID string, limit int32) ([]sqlc.TriggerRecord, error)
	// InsertTriggerRecord bridges broker-delivered records into
	// the trigger_records FSM queue (review finding #1, PR #910).
	InsertTriggerRecord(ctx context.Context, triggerID, itemIdentifier string, payload, headers, metadata []byte) (string, error)
	MarkTriggerRecordSucceeded(ctx context.Context, id string) error
	MarkTriggerRecordRetry(ctx context.Context, id, lastError string, nextFireAt time.Time) error
	MarkTriggerRecordDeadLetter(ctx context.Context, id, lastError string) error
	InsertTriggerDeadLetter(ctx context.Context, recordID, triggerID, reason, routedTo string, detail []byte) error
	// TriggerRecordIDByItemIdentifier (audit round 2 finding #1)
	// bridges the broker-handle namespace (kafka offset, NATS
	// seq, SQS receipt handle, Redis entry-id, queue invocation_id)
	// to the trigger_records.id UUID the dead_letter FK expects.
	TriggerRecordIDByItemIdentifier(ctx context.Context, triggerID, itemIdentifier string) (string, error)
}

type terminalNacker interface {
	NackTerminal(context.Context, sqlc.Trigger, []string, string) error
}

// triggerWakeup is the channel-side wakeup signal the schedd's
// pg_notify subscriber delivers on trigger-record mutations, trigger
// mutations, and bound queue invocation_due payloads. The Loop's run()
// method selects on it alongside the 1s ticker.
//
// WakeupTriggers sends a single token; runTriggerTick reads at
// most one trigger per ticker arm so a burst of broker messages
// doesn't cause a stampede.

// WakeupTriggers nudges the dispatch tick. Idempotent; safe to
// call from any goroutine.
func (l *Loop) WakeupTriggers() {
	if l == nil {
		return
	}
	l.triggerWakeupOnce.Do(func() {
		l.triggerWakeup = make(chan struct{}, 1)
	})
	select {
	case l.triggerWakeup <- struct{}{}:
	default:
		// Channel full — a wake is already pending.
	}
}

// runTriggerTick is the entry point. One tick handles ALL
// enabled triggers.
func (l *Loop) runTriggerTick(ctx context.Context) {
	if l == nil || l.engine == nil {
		return
	}
	store := l.loopStore()
	if store == nil {
		return
	}
	// Without an HTTP transport for the batch dispatch path
	// (gatewayd-internal was not wired at boot — production
	// schedd on a single-box deploy, or the schedd-only e2e
	// harness like meterd_dunning_e2e_test / meterd_quota_e2e_test
	// that don't run gatewayd-internal), skip the tick entirely.
	// Otherwise we walk every trigger, poll records from each
	// broker, claim the trigger_records rows, fail on postBatch
	// (typed error "gateway http client not configured"), and
	// bounce the records to retry — every 1s. That tight retry
	// storm will also mask the real failure (the dial error from
	// boot), so the warn emitted at boot by cmd/schedd/main.go is
	// the single source of operator visibility. PR #910 audit #5
	// required boot-fatal for the dial; the boot warn + tick
	// short-circuit preserves that signal without crashing the
	// daemon when the trigger primitive is unconfigured.
	if l.gatewayHTTPClient == nil {
		return
	}
	triggers, err := store.ListEnabledTriggers(ctx)
	if err != nil {
		l.log.Warn("sched trigger tick: list", "err", err)
		return
	}
	if len(triggers) == 0 {
		return
	}
	// Cache per-app+plan lookup so the per-record rate-limit gate
	// (review finding #4: was hardcoded to api.PlanFree) can
	// re-use the AccountPlan across the loop. The whole batch
	// for one trigger is one wake plan; the cache halves the
	// per-tick Postgres load when many triggers share an app.
	planCache := map[string]api.Plan{}
	resolvePlan := func(appID string) api.Plan {
		if p, ok := planCache[appID]; ok {
			return p
		}
		// Look up app → account to read the actual plan.
		app, appErr := l.engine.Store().AppByID(ctx, appID)
		if appErr != nil {
			return api.PlanFree
		}
		acct, acctErr := l.engine.Store().AccountByID(ctx, app.AccountID)
		if acctErr != nil {
			return api.PlanFree
		}
		if acct.Plan == "" {
			return api.PlanFree
		}
		planCache[appID] = acct.Plan
		return acct.Plan
	}
	for i := range triggers {
		t := triggers[i]
		if err := l.dispatchOneTrigger(ctx, t, store, resolvePlan); err != nil {
			l.log.Warn("sched trigger tick: dispatch",
				"trigger_id", t.ID.String(),
				"kind", t.Kind,
				"err", err)
		}
	}
}

// dispatchOneTrigger runs one trigger's per-tick work. The planFor
// resolver is consulted at the rate-limit gate so Hobby/Pro/Scale
// apps get their per-plan WakeBurstPerApp + TriggerRecordsPerSecondPerApp
// caps rather than collapsing to Free.
func (l *Loop) dispatchOneTrigger(ctx context.Context, t sqlc.Trigger, store storeLike, planFor func(string) api.Plan) error {
	// 1. Poller lookup. Cached on the Loop.
	if l.triggerPollers == nil {
		l.triggerPollers = map[string]triggerSource{}
	}
	poller, ok := l.triggerPollers[t.ID.String()]
	if !ok {
		pollerTrigger := t
		openedConfig, err := triggerconfig.Open(api.TriggerKind(t.Kind), t.Config, l.triggerSecretIdentities)
		if err != nil {
			return fmt.Errorf("open %s trigger credentials: %w", t.Kind, err)
		}
		pollerTrigger.Config = openedConfig
		src, registered, err := l.newPollerForTrigger(pollerTrigger)
		if !registered {
			l.log.Debug("sched trigger tick: no poller for kind",
				"trigger_id", t.ID.String(),
				"kind", t.Kind)
			return nil
		}
		if err != nil {
			return fmt.Errorf("construct %s trigger poller: %w", t.Kind, err)
		}
		poller = src
		l.triggerPollers[t.ID.String()] = poller
	}
	if poller == nil {
		return nil
	}

	// 2. Poll.
	res := poller.Poll(ctx, t)
	if res.Error != nil {
		// MED-2 (PR #993 / issue #757 review): a poll error is
		// still a tick outcome the dashboard wants to count.
		// Without this the schedd_esm_polls_total{outcome="error"}
		// series stays at zero and a broker outage is invisible
		// until something downstream (gateway 5xx, missing
		// invocations) trips an unrelated alert.
		l.observeESMPoll(t.Kind, wire.ESMPollOutcomeError)
		l.log.Warn("sched trigger tick: poll",
			"trigger_id", t.ID.String(),
			"kind", t.Kind,
			"err", res.Error)
		return fmt.Errorf("poll trigger %s: %w", t.ID, res.Error)
	}
	if len(res.Records) == 0 {
		// MED-2: count empty polls too. A trigger that's stuck
		// with no data should show steady "empty" traffic so a
		// rate(success)=0 alert fires, not "the metric vanished".
		l.observeESMPoll(t.Kind, wire.ESMPollOutcomeEmpty)
		return nil
	}

	// 3. Batch close: size / 6MB.
	batch := closeBatch(res.Records, int(t.BatchSizeMax), int(t.PayloadMaxBytes))
	if len(batch) == 0 {
		return nil
	}

	// 3.1. Broker egress accounting (ADR-118 / commit 8 of the
	// issue #757 mega-PR). Sum the bytes the dispatcher is about
	// to push to the broker and hand off to the BrokerAccountor
	// (nil = noop). The actual cap is enforced at the kernel qdisc
	// on the brokerq host interface; this counter is
	// observability-only and feeds the schedd_esm_egress_bytes
	// metric in commit 9. Called on the post-batch path so the
	// filter step (3.5) hasn't yet dropped the records — the byte
	// count reflects what the dispatcher fetched from the broker,
	// not what survived the filter.
	if l.brokerAccountor != nil {
		var bytes int64
		for _, r := range batch {
			bytes += int64(len(r.Payload))
		}
		if bytes > 0 {
			l.brokerAccountor.Account(ctx, t.ID.String(), bytes)
		}
	}

	// 3.5. Filter evaluation (ADR-118 / commit 6 of the issue
	// #757 mega-PR). For each polled record, evaluate the
	// trigger's filter_criteria against (payload, headers). A
	// record that doesn't match is Ack'd at the broker (so the
	// offset is committed and the message is NOT redelivered)
	// and dropped from the batch — no trigger_records row, no
	// audit row. A record whose filter errors out (parse error
	// on a malformed path) is also Ack'd but emits a single
	// trigger.filter_error audit row so operators can see the
	// author-side misconfig.
	//
	// Why before the rate-limit gate: a record that the customer
	// explicitly excluded via filter should NOT consume a wake
	// slot from the rate-limit bucket. The gate sees only the
	// filtered subset.
	if len(t.FilterCriteria) > 0 {
		filtered, filterErrCount, filterErr := l.filterBatch(ctx, t, batch)
		if filterErr != nil {
			// Catastrophic filter decode error (the JSONB on
			// the trigger row is malformed). Don't dispatch
			// any records for this trigger this tick — a
			// half-wired filter would dispatch some records
			// and skip others, which is silently confusing.
			// Emit one audit row + return; the next tick
			// retries the decode (the customer can fix the
			// column in apid's updateTrigger path).
			l.log.Warn("sched trigger tick: filter decode",
				"trigger_id", t.ID.String(),
				"err", filterErr)
			l.emitAudit(ctx, events.TriggerFilterErrorEvent{
				TriggerID: t.ID.String(),
				AppID:     t.AppID.String(),
				Error:     filterErr.Error(),
			})
			_ = poller.Ack(ctx, t, batchItemIDs(batch))
			return nil
		}
		if filterErrCount > 0 {
			// Per-record filter parse error (one of the paths
			// in the tree is malformed — not the whole tree).
			// One audit row summarises the count; the
			// dispatcher already Ack'd the offending records
			// in filterBatch.
			l.emitAudit(ctx, events.TriggerFilterErrorEvent{
				TriggerID: t.ID.String(),
				AppID:     t.AppID.String(),
				Error:     fmt.Sprintf("%d record(s) had a malformed filter clause; see dispatch logs", filterErrCount),
			})
		}
		batch = filtered
		if len(batch) == 0 {
			return nil
		}
	}

	// 3.7. ESM metric emission (MED-2 / PR #993 review).
	//
	// Three signals, all fed by the dispatcher not the poller:
	//
	//   - schedd_esm_polls_total{outcome="success"}: a tick that
	//     produced ≥1 post-filter record. Counted here (NOT at the
	//     Poll() call site) so an "empty" or "all-filtered-out"
	//     tick doesn't inflate the success series.
	//   - schedd_esm_records_consumed_total{source="kafka"}: the
	//     post-filter record count. Filter step (3.5) may have
	//     dropped some, and the gauge/dashboard reader cares about
	//     what actually left the broker toward the gateway — the
	//     filtered count is the authoritative one.
	//   - schedd_esm_lag_seconds{source="kafka",shard="..."}: per-
	//     record lag from ReceivedAt (stamped by the poller at the
	//     moment of broker fetch — segmentio/kafka-go's
	//     Reader.FetchMessage return) to "about to insert into
	//     trigger_records". A spike here is the only signal that
	//     warns schedd is falling behind the broker commit log.
	//
	// shard label is source-specific; shardKeyFor collapses to
	// "_agg" past the 32-bucket cap (the closed-set pre-instantiation
	// only populates `_agg` for out-of-vocab shards).
	l.observeESMPoll(t.Kind, wire.ESMPollOutcomeSuccess)
	l.observeESMRecords(t.Kind, len(batch))
	for _, rec := range batch {
		shard := shardKeyFor(rec, t.Kind)
		ageSeconds := time.Since(rec.ReceivedAt).Seconds()
		l.observeESMLag(t.Kind, shard, ageSeconds)
		if lagMessages, ok := consumerLagFor(rec, t.Kind); ok {
			l.observeESMConsumerLag(t.Kind, shard, lagMessages, ageSeconds)
		}
	}

	// 4. Persist polled records before applying terminal policy. This ordering
	// gives rate-limit and poison paths a durable record to transition and link
	// to the DLQ. ON CONFLICT makes a re-poll reuse the same record.
	for _, rec := range batch {
		payload := rec.Payload
		if payload == nil {
			payload = []byte("{}")
		}
		headers := marshalJSON(rec.Headers)
		metadata := marshalJSON(rec.Metadata)
		if _, err := store.InsertTriggerRecord(ctx, t.ID.String(), rec.ItemIdentifier, payload, headers, metadata); err != nil {
			l.log.Warn("sched trigger tick: insert record",
				"trigger_id", t.ID.String(),
				"item_identifier", rec.ItemIdentifier,
				"err", err)
		}
	}

	// 5. Rate-limit gate. Deny → dead_letter(reason='rate_limited').
	// review finding #4: the plan argument was hardcoded to api.PlanFree,
	// which collapsed Hobby/Pro/Scale customers to the Free bucket's
	// 1-wake-per-minute ceiling. Resolve the actual account plan via
	// the per-tick plan cache (constructed in runTriggerTick).
	appPlan := api.PlanFree
	if planFor != nil {
		appPlan = planFor(t.AppID.String())
	}
	if l.rateLimiter != nil && !l.rateLimiter.AllowWakeApp(t.AppID.String(), appPlan) {
		l.handleRateLimitedBatch(ctx, poller, t, batch, store)
		return nil
	}
	// Review finding #8: also consult the per-account bucket so a
	// runaway broker fan-out across many apps under one account
	// is rejected even when each app stays within its per-app
	// cap. The lookup walks app → account inline because the
	// per-tick plan cache above only stores AccountPlan, not the
	// account_id; an extra round-trip per trigger (one per tick)
	// is acceptable because the deny path is the hot path we
	// want to keep fast.
	if l.rateLimiter != nil {
		app, appErr := l.engine.Store().AppByID(ctx, t.AppID.String())
		if appErr == nil {
			if !l.rateLimiter.AllowWakeAccount(app.AccountID, appPlan) {
				l.handleRateLimitedBatch(ctx, poller, t, batch, store)
				return nil
			}
		}
	}

	// 6. Claim trigger_records rows.
	claimed, claimErr := store.ClaimTriggerRecords(ctx, t.ID.String(), int32(len(batch)))
	if claimErr != nil {
		l.log.Warn("sched trigger tick: claim",
			"trigger_id", t.ID.String(),
			"err", claimErr)
		// Queue pollers durably claim invocation rows before this
		// trigger-record claim. Put the whole batch back when the
		// audit-row claim fails; otherwise those rows would remain in
		// dispatching until the lease reaper runs.
		if err := poller.Nack(ctx, t, batchItemIDs(batch), triggerReasonBrokerError); err != nil {
			l.log.Warn("sched trigger tick: poller nack after claim failure",
				"trigger_id", t.ID.String(), "err", err)
		}
		return fmt.Errorf("dispatchOneTrigger claim: %w", claimErr)
	}
	if len(claimed) == 0 {
		// The poller has already claimed these records. A zero-row
		// trigger claim can happen after an insert conflict or while a
		// prior trigger-record retry is still cooling down; do not leave
		// the underlying queue rows leased indefinitely.
		if err := poller.Nack(ctx, t, batchItemIDs(batch), triggerReasonBrokerError); err != nil {
			l.log.Warn("sched trigger tick: poller nack after empty claim",
				"trigger_id", t.ID.String(), "err", err)
		}
		return nil
	}
	// InsertTriggerRecord is intentionally best-effort per record, so a
	// partial claim is possible. Release only the queue records without a
	// corresponding claimed trigger row; the claimed subset continues
	// through the gateway path below.
	if unclaimed := unclaimedItemIDs(batch, claimed); len(unclaimed) > 0 {
		if err := poller.Nack(ctx, t, unclaimed, triggerReasonBrokerError); err != nil {
			l.log.Warn("sched trigger tick: poller nack for unclaimed records",
				"trigger_id", t.ID.String(), "err", err)
		}
	}
	claimedBatch := recordsForClaimed(batch, claimed)
	if len(claimedBatch) == 0 {
		// A stale trigger-record claim cannot safely be posted with a
		// different broker batch. Release the claimed broker handles and
		// let the next poll reconcile the two durable queues.
		_ = poller.Nack(ctx, t, claimedItemIDs(claimed), triggerReasonBrokerError)
		return nil
	}

	// 6. Post the batch envelope to the gateway.
	envelope := buildDispatchEnvelope(t, claimedBatch)
	respBody, postErr := l.postBatch(ctx, envelope)
	if postErr != nil {
		l.log.Warn("sched trigger tick: gateway post",
			"trigger_id", t.ID.String(),
			"err", postErr)
		l.markRetryAll(ctx, claimed, postErr.Error(), store)
		l.observeESMRecordOutcome(t.Kind, wire.ESMRecordOutcomeRetry, len(claimed))
		l.observeESMRecordProcessingBatch(t.Kind, claimed)
		// Audit finding #6: SKIP LOCKED may return fewer rows
		// than len(batch). Nack only the records we actually
		// claimed — the rest stay in poller.inFlight and re-poll
		// on the next tick. Without this guard the broker side
		// saw Ack/Nack on records that had no trigger_records row
		// to retry, and the poller's in-flight bookkeeping
		// dropped those entries on the floor.
		_ = poller.Nack(ctx, t, claimedItemIDs(claimed), triggerReasonBrokerError)
		return nil
	}

	var resp triggerDispatchResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		l.log.Warn("sched trigger tick: response parse",
			"trigger_id", t.ID.String(),
			"err", err)
		ids := make([]string, 0, len(claimed))
		for _, c := range claimed {
			ids = append(ids, c.ID.String())
		}
		l.observeESMRecordOutcome(t.Kind, wire.ESMRecordOutcomeDeadLetter, len(claimed))
		l.observeESMRecordProcessingBatch(t.Kind, claimed)
		// Audit finding #6 (paired with the post-error path
		// above): same partial-claim guard — only Nack the
		// item_identifiers that have a corresponding
		// trigger_records row. The other batch entries stay
		// in poller.inFlight until the next poll cycle.
		items := claimedItemIDs(claimed)
		if _, isQueue := poller.(*queuePoller); isQueue {
			if err := poller.Nack(ctx, t, items, triggerReasonPoisonRecord); err != nil {
				l.log.Warn("sched trigger tick: finalize malformed queue response", "trigger_id", t.ID.String(), "err", err)
				return nil
			}
			l.deadLetterAllWithMark(ctx, t.ID.String(), items, triggerReasonPoisonRecord, "gateway response malformed", store, false)
		} else {
			l.deadLetterAll(ctx, t.ID.String(), ids, triggerReasonPoisonRecord, "gateway response malformed", store)
			_ = poller.Nack(ctx, t, items, triggerReasonPoisonRecord)
		}
		return nil
	}

	statusByID := make(map[string]triggerDispatchResult, len(resp.Results))
	for _, r := range resp.Results {
		statusByID[r.ItemIdentifier] = r
	}

	succeedIDs := []string{}
	retryIDs := []string{}
	dlqIDs := []string{}
	dlqReasons := []string{}   // parallel to dlqIDs; review finding #8
	dlqErrors := []string{}    // parallel to dlqIDs; review finding #8
	dlqAttempts := []int32{}   // parallel to dlqIDs; review finding #10
	retryAttempts := []int32{} // parallel to retryIDs; review finding #10
	succeedItems := []string{}
	retryItems := []string{}
	dlqItems := []string{}
	_, isQueuePoller := poller.(*queuePoller)

	for _, c := range claimed {
		itemID := c.ItemIdentifier
		status, found := statusByID[itemID]
		if !found {
			retryIDs = append(retryIDs, c.ID.String())
			retryItems = append(retryItems, itemID)
			// Review finding #10: report the post-increment
			// Attempts value.
			retryAttempts = append(retryAttempts, c.Attempts+1)
			continue
		}
		switch status.Status {
		case "succeeded":
			succeedIDs = append(succeedIDs, c.ID.String())
			succeedItems = append(succeedItems, itemID)
		case "retry", "broker_error":
			retryIDs = append(retryIDs, c.ID.String())
			retryItems = append(retryItems, itemID)
			retryAttempts = append(retryAttempts, c.Attempts+1)
		case "dead_letter":
			dlqIDs = append(dlqIDs, c.ID.String())
			dlqItems = append(dlqItems, itemID)
			dlqAttempts = append(dlqAttempts, c.Attempts+1)
			// Review finding #8: the old code dropped
			// status.Error on the floor and stampped the
			// audit Reason='max_attempts' regardless of
			// cause. Map the gateway-supplied status.Error
			// onto one of the trigger_dead_letter.reason
			// CHECK values; fall through to 'poison_record'
			// when the gateway didn't classify.
			reason := classifyDLQReason(status.Code, status.Error)
			dlqReasons = append(dlqReasons, reason)
			dlqErrors = append(dlqErrors, status.Error)
		default:
			retryIDs = append(retryIDs, c.ID.String())
			retryItems = append(retryItems, itemID)
			retryAttempts = append(retryAttempts, c.Attempts+1)
		}
	}
	// Record terminal dispositions and end-to-end consumer latency before
	// applying the durable state transitions below. This keeps queue retry
	// and dead-letter pressure visible even when the subsequent broker ack
	// call fails and the lease reaper must recover the row.
	l.observeESMRecordOutcome(t.Kind, wire.ESMRecordOutcomeSucceeded, len(succeedIDs))
	l.observeESMRecordOutcome(t.Kind, wire.ESMRecordOutcomeRetry, len(retryIDs))
	l.observeESMRecordOutcome(t.Kind, wire.ESMRecordOutcomeDeadLetter, len(dlqIDs))
	l.observeESMRecordProcessingBatch(t.Kind, claimed)

	if len(succeedIDs) > 0 {
		if isQueuePoller {
			if err := poller.Ack(ctx, t, succeedItems); err != nil {
				l.log.Warn("sched trigger tick: finalize successful queue invocations",
					"trigger_id", t.ID.String(), "err", err)
				return nil
			}
		}
		for _, id := range succeedIDs {
			if !isQueuePoller {
				if err := store.MarkTriggerRecordSucceeded(ctx, id); err != nil {
					l.log.Warn("sched trigger tick: mark succeeded",
						"id", id, "err", err)
				}
			}
			// Audit: trigger.fired per succeeded record,
			// dual-emitted as esm.source.created for
			// operators on the ESM panel selector
			// (kind_prefix=esm.*). The two events land in
			// the events table with identical payload;
			// consumers JOIN on (trigger_id, record_id, At).
			l.emitAuditDual(
				ctx,
				events.TriggerFiredEvent{
					TriggerID: t.ID.String(),
					RecordID:  id,
					AppID:     t.AppID.String(),
					FiredAt:   time.Now(),
				},
				events.ESMSourceCreatedEvent{
					TriggerID:  t.ID.String(),
					AppID:      t.AppID.String(),
					SourceKind: t.Kind,
					EmitAt:     time.Now(),
				},
				t.ID.String(), t.AppID.String(), t.Kind,
			)
		}
		if !isQueuePoller {
			if err := poller.Ack(ctx, t, succeedItems); err != nil {
				l.log.Warn("sched trigger tick: poller ack",
					"trigger_id", t.ID.String(), "err", err)
			}
		}
	}
	if len(retryIDs) > 0 {
		for i, id := range retryIDs {
			attempts := retryAttempts[i]
			// Review finding #9: exponential backoff + ±20%
			// jitter replaces the prior hardcoded 2s.
			backoff := computeTriggerRetryBackoff(t, attempts)
			nextFireAt := time.Now().Add(backoff)
			if err := store.MarkTriggerRecordRetry(ctx, id, "", nextFireAt); err != nil {
				l.log.Warn("sched trigger tick: mark retry",
					"id", id, "err", err)
			}
			// Audit: trigger.retry, dual-emitted as
			// esm.poll.failed for the operator alias.
			l.emitAuditDual(
				ctx,
				events.TriggerRetryEvent{
					TriggerID:  t.ID.String(),
					RecordID:   id,
					AppID:      t.AppID.String(),
					Attempt:    int(attempts),
					NextFireAt: nextFireAt,
				},
				events.ESMPollFailedEvent{
					TriggerID:  t.ID.String(),
					AppID:      t.AppID.String(),
					SourceKind: t.Kind,
					Error:      fmt.Sprintf("retry: attempt=%d next_fire_at=%s", attempts, nextFireAt.Format(time.RFC3339)),
					EmitAt:     time.Now(),
				},
				t.ID.String(), t.AppID.String(), t.Kind,
			)
		}
		if err := poller.Nack(ctx, t, retryItems, triggerReasonBrokerError); err != nil {
			l.log.Warn("sched trigger tick: poller nack",
				"trigger_id", t.ID.String(), "err", err)
		}
	}
	if len(dlqIDs) > 0 {
		if isQueuePoller {
			if err := poller.Nack(ctx, t, dlqItems, triggerReasonPoisonRecord); err != nil {
				l.log.Warn("sched trigger tick: finalize queue dead letters",
					"trigger_id", t.ID.String(), "err", err)
				return nil
			}
		}
		for i, id := range dlqIDs {
			reason := dlqReasons[i]
			lastErr := dlqErrors[i]
			attempts := dlqAttempts[i]
			if err := store.InsertTriggerDeadLetter(ctx, id, t.ID.String(), reason, "drop", []byte(lastErr)); err != nil {
				l.log.Warn("sched trigger tick: insert dlq", "id", id, "err", err)
			}
			if !isQueuePoller {
				if err := store.MarkTriggerRecordDeadLetter(ctx, id, lastErr); err != nil {
					l.log.Warn("sched trigger tick: mark dlq",
						"id", id, "err", err)
				}
			}
			// Audit: trigger.dlq, dual-emitted as
			// esm.drain.dlq for the operator alias.
			l.emitAuditDual(
				ctx,
				events.TriggerDLQEvent{
					TriggerID: t.ID.String(),
					RecordID:  id,
					AppID:     t.AppID.String(),
					Reason:    reason,
					Attempts:  int(attempts),
					LastError: lastErr,
				},
				events.ESMDrainDLQEvent{
					TriggerID: t.ID.String(),
					RecordID:  id,
					AppID:     t.AppID.String(),
					Reason:    reason,
					EmitAt:    time.Now(),
				},
				t.ID.String(), t.AppID.String(), t.Kind,
			)
		}
		if !isQueuePoller {
			if err := poller.Nack(ctx, t, dlqItems, triggerReasonPoisonRecord); err != nil {
				l.log.Warn("sched trigger tick: poller nack (dlq)",
					"trigger_id", t.ID.String(), "err", err)
			}
		}
	}
	// Audit: trigger.fired.batch — aggregated counts. No ESM
	// alias: the per-batch aggregate is a trigger.*-only
	// concept (operators reading the ESM timeline filter by
	// record_id, not by batch_id). ADR-118 §"Asymmetric kind
	// mapping" pins this exception explicitly.
	l.emitAudit(ctx, events.TriggerFiredBatchEvent{
		TriggerID:      t.ID.String(),
		BatchSize:      len(batch),
		AttemptTotal:   len(batch),
		SucceededTotal: len(succeedIDs),
		FailedTotal:    len(retryIDs) + len(dlqIDs),
	})

	l.log.Debug("sched trigger tick: batch complete",
		"trigger_id", t.ID.String(),
		"kind", t.Kind,
		"records", len(batch),
		"succeeded", len(succeedIDs),
		"retry", len(retryIDs),
		"dead_letter", len(dlqIDs))
	return nil
}

// closeBatch truncates the polled slice to honour the per-trigger
// batch_size_max + 6MB payload cap.
//
// Per-record semantics at the gateway (audit round 2 finding #4,
// PR #910): the gateway's handleInvocationDispatchBatch loops
// Invoke() once per record — the handler is single-threaded,
// the schedd's HTTP client is held for the entire batch
// duration, and each record pays its own wake gate + admission
// cost. The doc-comment at pkg/gateway/synth.go:420 pins this
// explicitly because the original wording ("one function
// invocation serves N records", Lambda ESM) was misleading.
// A future PR can batch-encode and short-circuit Invoke() if a
// target VM is already RUNNING; that change is out of scope
// here.
func closeBatch(records []SourceRecord, sizeMax, byteCap int) []SourceRecord {
	if sizeMax <= 0 {
		sizeMax = len(records)
	}
	out := make([]SourceRecord, 0, len(records))
	total := 0
	for _, r := range records {
		if len(out) >= sizeMax {
			break
		}
		if total+len(r.Payload) > byteCap {
			break
		}
		total += len(r.Payload)
		out = append(out, r)
	}
	return out
}

// buildDispatchEnvelope packages the records into the JSON shape
// the gateway expects.
func buildDispatchEnvelope(t sqlc.Trigger, batch []SourceRecord) triggerDispatchRequest {
	recs := make([]triggerDispatchRecord, 0, len(batch))
	for _, r := range batch {
		recs = append(recs, triggerDispatchRecord{
			ItemIdentifier: r.ItemIdentifier,
			PayloadB64:     base64.StdEncoding.EncodeToString(r.Payload),
			Headers:        r.Headers,
			Metadata:       r.Metadata,
		})
	}
	return triggerDispatchRequest{
		InvocationID: "trigger-" + t.ID.String(),
		AppID:        t.AppID.String(),
		Source:       "esm",
		TriggerID:    t.ID.String(),
		Records:      recs,
	}
}

// postBatch hits the gateway's batch endpoint via the existing
// GatewaySynth HTTP transport.
func (l *Loop) postBatch(ctx context.Context, env triggerDispatchRequest) ([]byte, error) {
	body, err := json.Marshal(env)
	if err != nil {
		return nil, fmt.Errorf("marshal envelope: %w", err)
	}
	if l.gatewayHTTPClient == nil {
		return nil, fmt.Errorf("sched: gateway http client not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		l.gatewayBaseURL+"/v1/invocations:dispatch_batch",
		&byteReadCloser{b: body})
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = int64(len(body))
	// ADR-119 — attach an Authorization: Bearer JWT when the
	// target app is in 'internal_only' mode. The trigger batch
	// envelope carries ONE appID across all records, so the
	// lookup is per-batch (not per-record). Same nil-safe +
	// fail-closed posture as SynthesizeRequest + Invoke. The
	// lookup carries the parent ctx so a shutdown signal
	// cancels the store round-trip. Without this attachment,
	// the gate at synth.go::handleInvocationDispatchBatch would
	// 403 every internal_only batch the schedd posts.
	if l.appPublicAuthModeLookup != nil {
		if res, lookupErr := l.appPublicAuthModeLookup(ctx, env.AppID); lookupErr != nil || res.Mode == "internal_only" {
			if l.mintInternalSvcToken == nil {
				l.log.Warn("sched: batch path: app in internal_only mode (or lookup failed) but no minter wired; gate will 403",
					"app_id", env.AppID, "lookup_err", lookupErrStr(lookupErr))
			} else {
				tok, mErr := l.mintInternalSvcToken(env.AppID)
				if mErr != nil {
					return nil, fmt.Errorf("sched: batch mint: %w", mErr)
				}
				req.Header.Set("Authorization", "Bearer "+tok)
			}
		}
	}
	resp, err := l.gatewayHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		raw, readErr := readGatewayDispatchResponse(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("status %d: %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, raw)
	}
	return readGatewayDispatchResponse(resp.Body)
}

func readGatewayDispatchResponse(body io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(body, gatewayDispatchResponseMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > gatewayDispatchResponseMaxBytes {
		return nil, fmt.Errorf("gateway response exceeds %d bytes", gatewayDispatchResponseMaxBytes)
	}
	return raw, nil
}

// deadLetterAll marks every record as dead_letter + inserts a
// trigger_dead_letter row.
//
// The `ids` slice is the list of item_identifiers the broker
// delivered — broker handles (kafka offset, NATS seq, SQS receipt
// handle, Redis entry-id, queue invocation_id), NOT trigger_records
// row UUIDs. The trigger_dead_letter.record_id column is a UUID FK
// into trigger_records.id, so we resolve each item_identifier to
// its row UUID via TriggerRecordIDByItemIdentifier before the
// InsertTriggerDeadLetter call.
//
// Audit round 2 finding #1 (PR #910): without this lookup every
// rate-limit denial tripped SQLSTATE 23503, the dead_letter row
// was silently dropped, MarkTriggerRecordDeadLetter updated 0
// rows, the record stayed in poller.inFlight forever, and the
// broker offset never advanced.
//
// If the row hasn't been inserted yet (rate-limit fires before
// the InsertTriggerRecord loop on the next dispatch step), the
// lookup returns an empty UUID — we skip the DLQ insert + skip
// the MarkTriggerRecordDeadLetter call. The caller (the
// rate-limit-deny branch above) MUST then ack the broker offset
// so the records don't re-poll forever. Pre-CRIT-1 the deny
// branches returned without ack'ing; CRIT-1 closes that hole
// — see dispatch_triggers.go:394-419.
//
// The poisoned-response path at dispatch_triggers.go:354 calls
// this helper with row UUIDs already (it walks the `claimed`
// result-set and reads c.ID.String()). That works because the
// UUID lookup self-resolves: the row is by construction present,
// so the lookup returns the same UUID we passed in.
// handleRateLimitedBatch is the CRIT-1 (PR #993 / issue #757
// closure) seam for the rate-limit-deny branches. Pre-CRIT-1 each
// deny branch returned immediately after deadLetterAll WITHOUT
// ack'ing the broker, which pinned the broker offset at the front
// of the rate-limited batch and re-delivered the same records on
// every subsequent tick. The new semantic is:
//
//  1. deadLetterAll inserts the trigger_dead_letter audit row
//     (the disposition is preserved).
//  2. poller.Ack advances the broker offset so the records don't
//     re-poll.
//
// Extracted so the test surface (TestRateLimitDeny_AcksBrokerOffset)
// can pin the dual-call sequence without driving the full dispatch
// tick.
func (l *Loop) handleRateLimitedBatch(ctx context.Context, poller triggerSource, t sqlc.Trigger, batch []SourceRecord, store storeLike) {
	items := batchItemIDs(batch)
	l.observeESMRecordOutcome(t.Kind, wire.ESMRecordOutcomeDeadLetter, len(batch))
	for _, record := range batch {
		if !record.ReceivedAt.IsZero() {
			l.observeESMRecordProcessing(t.Kind, time.Since(record.ReceivedAt).Seconds())
		}
	}
	if queue, ok := poller.(*queuePoller); ok {
		if err := queue.finishInvocations(ctx, t, items, "dead_letter", "dead_letter", "rate_limited", triggerReasonRateLimited, `{"trigger_dispatch":"rate_limited"}`); err != nil {
			l.log.Warn("sched trigger tick: finalize rate-limited queue invocations",
				"trigger_id", t.ID.String(),
				"err", err)
			return
		}
		l.deadLetterAllWithMark(ctx, t.ID.String(), items, triggerReasonRateLimited, "wake rate limit exceeded", store, false)
		return
	}
	l.deadLetterAll(ctx, t.ID.String(), items, triggerReasonRateLimited, "wake rate limit exceeded", store)
	if terminal, ok := poller.(terminalNacker); ok {
		// Queue rows have not yet produced trigger_records, so Ack would
		// incorrectly mark the invocation successful. Move them directly
		// to the queue's terminal state; external brokers keep the legacy
		// Ack-after-DLQ offset advance.
		_ = terminal.NackTerminal(ctx, t, items, triggerReasonRateLimited)
		return
	}
	_ = poller.Ack(ctx, t, items)
}

func (l *Loop) deadLetterAll(ctx context.Context, triggerID string, ids []string, reason, detail string, store storeLike) {
	l.deadLetterAllWithMark(ctx, triggerID, ids, reason, detail, store, true)
}

// deadLetterAllWithMark writes the durable DLQ rows and optionally performs
// the trigger-record transition. Queue pollers set mark=false because they
// transition trigger_records and invocations together in one SQL statement.
func (l *Loop) deadLetterAllWithMark(ctx context.Context, triggerID string, ids []string, reason, detail string, store storeLike, mark bool) {
	if store == nil || len(ids) == 0 {
		return
	}
	for _, id := range ids {
		uuid, lookupErr := store.TriggerRecordIDByItemIdentifier(ctx, triggerID, id)
		if lookupErr != nil {
			l.log.Warn("sched trigger tick: dlq record_id lookup",
				"trigger_id", triggerID,
				"item_identifier", id,
				"err", lookupErr)
			continue
		}
		// Empty UUID = row not yet in trigger_records (rate-limit
		// fired before InsertTriggerRecord). Skip — the broker
		// offset stays where it is and the next tick retries.
		if uuid == "" {
			l.log.Warn("sched trigger tick: dlq row missing (rate-limit before insert; record will be retried)",
				"trigger_id", triggerID,
				"item_identifier", id,
				"reason", reason)
			continue
		}
		if err := store.InsertTriggerDeadLetter(ctx, uuid, triggerID, reason, "drop", []byte(detail)); err != nil {
			l.log.Warn("sched trigger tick: insert dlq", "id", uuid, "err", err)
			continue
		}
		if mark {
			if err := store.MarkTriggerRecordDeadLetter(ctx, uuid, reason); err != nil {
				l.log.Warn("sched trigger tick: mark dlq", "id", uuid, "err", err)
			}
		}
	}
}

// markRetryAll marks every record as state='retry'.
func (l *Loop) markRetryAll(ctx context.Context, claimed []sqlc.TriggerRecord, errMsg string, store storeLike) {
	if store == nil || len(claimed) == 0 {
		return
	}
	nextFireAt := time.Now().Add(2 * time.Second)
	for _, c := range claimed {
		if err := store.MarkTriggerRecordRetry(ctx, c.ID.String(), errMsg, nextFireAt); err != nil {
			l.log.Warn("sched trigger tick: mark retry", "id", c.ID.String(), "err", err)
		}
	}
}

// batchItemIDs walks the batch and returns the item identifiers
// for Ack/Nack on the poller.
func batchItemIDs(batch []SourceRecord) []string {
	if len(batch) == 0 {
		return nil
	}
	out := make([]string, 0, len(batch))
	for _, r := range batch {
		out = append(out, r.ItemIdentifier)
	}
	return out
}

// claimedItemIDs returns the item_identifiers from the ClaimTriggerRecords
// result set. Distinct from batchItemIDs because SKIP LOCKED may return
// fewer rows than the polled batch — Ack/Nack must operate only on
// records that have a corresponding trigger_records row (audit #6).
// Records that didn't get claimed stay in poller.inFlight and re-poll
// naturally on the next tick.
func claimedItemIDs(claimed []sqlc.TriggerRecord) []string {
	if len(claimed) == 0 {
		return nil
	}
	out := make([]string, 0, len(claimed))
	for _, c := range claimed {
		out = append(out, c.ItemIdentifier)
	}
	return out
}

// unclaimedItemIDs returns the broker identifiers from batch that are not
// represented by ClaimTriggerRecords. Queue pollers claim the invocation row
// before the trigger-record insert/claim sequence, so these identifiers must
// be released when a partial claim occurs.
func unclaimedItemIDs(batch []SourceRecord, claimed []sqlc.TriggerRecord) []string {
	if len(batch) == 0 {
		return nil
	}
	claimedSet := make(map[string]struct{}, len(claimed))
	for _, c := range claimed {
		claimedSet[c.ItemIdentifier] = struct{}{}
	}
	out := make([]string, 0, len(batch))
	for _, r := range batch {
		if _, ok := claimedSet[r.ItemIdentifier]; !ok {
			out = append(out, r.ItemIdentifier)
		}
	}
	return out
}

func recordsForClaimed(batch []SourceRecord, claimed []sqlc.TriggerRecord) []SourceRecord {
	if len(batch) == 0 || len(claimed) == 0 {
		return nil
	}
	claimedSet := make(map[string]struct{}, len(claimed))
	for _, c := range claimed {
		claimedSet[c.ItemIdentifier] = struct{}{}
	}
	out := make([]SourceRecord, 0, len(claimed))
	for _, r := range batch {
		if _, ok := claimedSet[r.ItemIdentifier]; ok {
			out = append(out, r)
		}
	}
	return out
}

// byteReadCloser is a tiny io.ReadCloser for a []byte.
type byteReadCloser struct {
	b   []byte
	pos int
}

func (r *byteReadCloser) Read(p []byte) (int, error) {
	if r.pos >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.pos:])
	r.pos += n
	return n, nil
}

func (r *byteReadCloser) Close() error { return nil }

// marshalJSON turns a generic map into a JSONB-ready byte slice.
// Returns "{}" when the input is nil so the ON CONFLICT DO NOTHING
// branch on InsertTriggerRecord stays deterministic across the
// broker adapters (kafka / nats / redis_streams all produce
// slightly different header / metadata shapes — every nil map
// must serialise to the same empty-object payload).
func marshalJSON(v any) []byte {
	if v == nil {
		return []byte("{}")
	}
	out, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return out
}

// computeRetryBackoff returns the retry delay for an attempt at
// the post-increment `attempts` count. Review finding #9: replaces
// the hardcoded 2s previously used on every retry. Shape: 1s
// base, doubling each attempt up to a 5-minute ceiling, with
// ±20% jitter so a burst of synchronous broker failures doesn't
// re-fire on the same tick.
//
//	attempts=1 → ~1s  (range 0.8s..1.2s)
//	attempts=2 → ~2s  (range 1.6s..2.4s)
//	attempts=3 → ~4s  (range 3.2s..4.8s)
//	attempts=4 → ~8s
//	attempts=N → min(2^(N-1), 300)s
func computeRetryBackoff(attempts int32) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	exp := attempts - 1
	if exp > 9 {
		exp = 9
	}
	base := time.Second << exp
	if base > 5*time.Minute {
		base = 5 * time.Minute
	}
	//nolint:gosec // G404: jitter on retry backoff has no security value — the
	// worst a malicious caller can do is nudge our delay by ±20%, which
	// the dispatch tick absorbs. math/rand/v2 is correct here.
	jitter := time.Duration(float64(base) * (0.8 + 0.01*float64(rand.Uint64()%41)))
	return jitter
}

// computeTriggerRetryBackoff selects a trigger's configured retry curve,
// falling back to the historical inline curve for rows created before
// retry_policy was introduced. The policy lives in the trigger config JSONB
// so this path remains compatible with existing sqlc models and migrations.
func computeTriggerRetryBackoff(t sqlc.Trigger, attempts int32) time.Duration {
	var envelope struct {
		RetryPolicy struct {
			MaxAttempts   int     `json:"max_attempts"`
			BaseSeconds   float64 `json:"base_seconds"`
			MaxSeconds    float64 `json:"max_seconds"`
			JitterSeconds float64 `json:"jitter_seconds"`
		} `json:"retry_policy"`
	}
	if len(t.Config) > 0 && json.Unmarshal(t.Config, &envelope) == nil {
		policy := dispatch.RetryPolicy{
			MaxAttempts:   envelope.RetryPolicy.MaxAttempts,
			BaseSeconds:   envelope.RetryPolicy.BaseSeconds,
			MaxSeconds:    envelope.RetryPolicy.MaxSeconds,
			JitterSeconds: envelope.RetryPolicy.JitterSeconds,
		}
		if !policy.Zero() {
			return policy.Backoff(int(attempts))
		}
	}
	return computeRetryBackoff(attempts)
}

// Compile-time guarantee the helpers we use are wired.
var _ = slog.Default

// Compile-time guarantee the trigger dispatcher depends on the
// pkg/dispatch contract (ADR-134 §6.7). The dispatcher consumes
// RetryPolicy (PR-C migrates computeRetryBackoff onto
// dispatch.RetryPolicy.Backoff), DeadlinePolicy, and dispatch.Job
// once per-row fields land on trigger_records.
var _ dispatch.JobKind = dispatch.JobKindTriggerRecord

// observeESMPoll is the Loop-side wrapper around
// OpsMetrics.ObserveESMPoll that nil-checks the loop's *wire.OpsMetrics
// before forwarding. dispatchOneTrigger calls this at three sites
// (poll-error, poll-empty, post-filter success) so the dashboard
// rate(success)+rate(empty)+rate(error) sums to the per-tick total.
//
// MED-2 (PR #993 / issue #757 review): the original 11 commits
// shipped the OpsMetrics helper but never wired any callsite, so
// schedd_esm_polls_total was structurally zero in production. This
// wrapper exists so dispatchOneTrigger has a one-symbol callsite and
// the nil-check lives next to the metric vocabulary rather than at
// every insertion point.
func (l *Loop) observeESMPoll(source, outcome string) {
	if l.ops == nil {
		return
	}
	l.ops.ObserveESMPoll(source, outcome)
}

// observeESMRecords is the loop-side nil-checked wrapper around
// OpsMetrics.ObserveESMRecords. The count is the post-filter batch
// length — see dispatchOneTrigger step 3.7 for the rationale.
func (l *Loop) observeESMRecords(source string, n int) {
	if l.ops == nil {
		return
	}
	l.ops.ObserveESMRecords(source, n)
}

// observeESMLag is the loop-side nil-checked wrapper around
// OpsMetrics.ObserveESMLag. Per-record lag from broker fetch to
// the moment the dispatcher is about to insert into
// trigger_records.
func (l *Loop) observeESMLag(source, shard string, lagSeconds float64) {
	if l.ops == nil {
		return
	}
	l.ops.ObserveESMLag(source, shard, lagSeconds)
}

// observeESMRecordOutcome forwards the terminal disposition counters while
// keeping the dispatch path nil-safe for lightweight schedd test loops.
func (l *Loop) observeESMRecordOutcome(source, outcome string, n int) {
	if l.ops == nil {
		return
	}
	l.ops.ObserveESMRecordOutcome(source, outcome, n)
}

// observeESMRecordProcessing forwards the end-to-end consumer latency
// observation. The wire helper owns the finite-value and closed-label guards.
func (l *Loop) observeESMRecordProcessing(source string, seconds float64) {
	if l.ops == nil {
		return
	}
	l.ops.ObserveESMRecordProcessing(source, seconds)
}

func (l *Loop) observeESMConsumerLag(source, shard string, messages int64, ageSeconds float64) {
	if l.ops == nil {
		return
	}
	l.ops.ObserveESMConsumerLag(source, shard, messages, ageSeconds)
}

func (l *Loop) observeESMRecordProcessingBatch(source string, records []sqlc.TriggerRecord) {
	for _, record := range records {
		if record.ReceivedAt.Valid {
			l.observeESMRecordProcessing(source, time.Since(record.ReceivedAt.Time).Seconds())
		}
	}
}

// shardKeyFor derives the shard label for the lag metric from
// SourceRecord.Metadata. The dispatcher collapses shards past the
// 32-bucket cap to "_agg" — the closed-set pre-instantiation in
// wire.NewOpsMetrics only populates that series for out-of-vocab
// shard keys, so an unbounded label here would silently inflate
// Prometheus cardinality.
//
// Source-specific extraction:
//
//	"kafka"  → Metadata["partition"] (int, stamped by poller_kafka)
//	"nats"   → Metadata["stream"]    (added in a follow-up; empty
//	                                  today → "_agg")
//	default  → "_agg"
//
// The 32-bucket cap is enforced here (rather than inside ObserveESMLag)
// because that's where the source vocabulary lives — keeping the
// closed-set semantics next to the dispatch logic means a new
// source added later only has to extend this switch.
func shardKeyFor(rec SourceRecord, kind string) string {
	var key string
	switch kind {
	case string(api.TriggerKindKafka):
		if rec.Metadata != nil {
			if v, ok := rec.Metadata["partition"]; ok {
				key = fmt.Sprintf("%v", v)
			}
		}
	case string(api.TriggerKindNATS):
		if rec.Metadata != nil {
			if v, ok := rec.Metadata["stream"]; ok {
				key = fmt.Sprintf("%v", v)
			}
		}
	}
	if key == "" {
		return "_agg"
	}
	// Hard cap on label cardinality. Prometheus closed-set pre-
	// instantiation only guards labels it knows at boot; a runaway
	// partition id (or a malicious Metadata injection) would
	// otherwise create new series on every record. The 32-bucket
	// cap mirrors wire/metrics.go's pre-instantiation (see
	// NewOpsMetrics: only "_agg" + the first 31 known series ship).
	if len(key) > 32 {
		return "_agg"
	}
	return key
}

// consumerLagFor extracts a broker-native lag sample when the poller exposes
// a high-water mark. Kafka is the first source with that information; other
// brokers continue to use the existing per-record age histogram until their
// native watermark APIs are wired.
func consumerLagFor(rec SourceRecord, kind string) (int64, bool) {
	if kind != string(api.TriggerKindKafka) || rec.Metadata == nil {
		return 0, false
	}
	offset, okOffset := numericMetadataInt64(rec.Metadata["offset"])
	high, okHigh := numericMetadataInt64(rec.Metadata["high_water_mark"])
	if !okOffset || !okHigh || high <= offset {
		return 0, false
	}
	return high - offset - 1, true
}

func numericMetadataInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), n >= 0 && n == float64(int64(n))
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}

// classifyDLQReason maps the gateway's per-record outcome onto
// one of trigger_dead_letter.reason's CHECK values
// (migrations/00297_triggers.sql::trigger_dead_letter.reason_check).
//
// Audit finding #8: the prior code hardcoded 'max_attempts' for
// every Status='dead_letter' record AND substring-matched the
// Error field — both brittle. Substring matching broke the moment
// the gateway added a new error path with overlapping words
// (e.g. "timeout" matching "broker timeout" but also "request
// timeout from client").
//
// The Code field (added in audit #8) is the structured counterpart
// the gateway emits — a stable machine-readable string the dispatch
// tick switches on. Real causes the gateway distinguishes today
// (pkg/gateway/synth.go::handleInvocationDispatchBatch):
//
//	"payload_b64_invalid"   → poison_record
//	"response_malformed"    → poison_record
//	"invoke_error"          → broker_error
//	"function_failed"       → max_attempts (the function decided
//	                                   to give up — it called
//	                                   back into the report with
//	                                   the same id which means it
//	                                   has exhausted its own
//	                                   retries)
//	"function_state_<X>"    → max_attempts (function returned
//	                                   non-succeeded state)
//	"" (empty, legacy gw)   → fall through to substring match
//	                            on Error for back-compat
//
// The substring fallback only fires for older gateway versions
// that pre-date the Code field. New deployments always go through
// the structured switch.
func classifyDLQReason(code, gatewayErr string) string {
	if code != "" {
		switch code {
		case "function_failed":
			return triggerReasonMaxAttempts
		case "function_state_timeout",
			"function_state_failed",
			"function_state_killed",
			"function_state_lost":
			return triggerReasonMaxAttempts
		case "payload_b64_invalid",
			"response_malformed":
			return triggerReasonPoisonRecord
		case "invoke_error":
			return triggerReasonBrokerError
		default:
			return triggerReasonPoisonRecord
		}
	}
	// Substring fallback for older gateways without a Code field.
	if gatewayErr == "" {
		return triggerReasonPoisonRecord
	}
	switch {
	case strings.Contains(gatewayErr, "batchItemFailures"):
		return triggerReasonMaxAttempts
	case strings.Contains(gatewayErr, "timeout"):
		return triggerReasonMaxAttempts
	case strings.Contains(gatewayErr, "malformed"),
		strings.Contains(gatewayErr, "broker_error"),
		strings.Contains(gatewayErr, "payload_b64"):
		return triggerReasonPoisonRecord
	default:
		return triggerReasonPoisonRecord
	}
}

// emitAudit writes an audit row via the loop's audit.Auditor.
// Nil-safe (no-ops if the Loop has no Auditor wired — keeps tests
// quiet).
//
// Trigger audit kinds are emitted as generic events via the
// Auditor's typed path. The Auditor writes a single events row
// per call (pkg/audit/audit.go mirrors pkg/events.Platform's
// best-effort semantics).
//
// ctx is threaded through so the call honours the dispatch tick's
// lifecycle (CI lint: contextcheck). pkg/audit.Auditor.Emit
// already accepts context.
func (l *Loop) emitAudit(ctx context.Context, ev events.WakeEvent) {
	if l == nil || l.audit == nil || ev == nil {
		return
	}
	l.audit.Emit(ctx, ev.Kind(), nil, ev.Payload())
}

// emitAuditDual emits BOTH a trigger.* event AND its esm.*
// operator alias (ADR-118 §"Audit vocabulary bridging"). The
// pair is fixed at compile time; adding a new pair requires (a)
// a trigger.* event + (b) an esm.* event in pkg/events/trigger.go
// + (c) a new helper here that calls Emit for both. The two
// rows are NOT wrapped in a Postgres transaction — a crash
// between the two emits leaves one row behind. Operators can
// detect this by joining on (trigger_id, record_id, At) and
// looking for rows whose pair is missing; the events table
// doesn't enforce uniqueness on the pair.
//
// Callers pass the trigger_id / app_id / source_kind via the
// typed args; the trigger.* event carries the canonical payload,
// the esm.* event carries the operator-alias payload. The
// SourceKind field is the trigger Kind string ("kafka", "nats",
// ...) — the dispatch tick already has it on `t.Kind`.
func (l *Loop) emitAuditDual(
	ctx context.Context,
	triggerEv, esmEv events.WakeEvent,
	triggerID, appID, sourceKind string,
) {
	l.emitAudit(ctx, triggerEv)
	l.emitAudit(ctx, esmEv)
}

// filterBatch walks the batch, evaluates FilterCriteria.Match
// for each SourceRecord, and returns:
//
//   - the subset of records that MATCH (so the rest of the
//     dispatch tick sees only the filtered set),
//   - the count of per-record filter errors encountered
//     (the caller emits one audit row summarising this),
//   - a non-nil error iff the WHOLE filter tree failed to
//     decode (the caller emits one audit row + drops the
//     entire tick for this trigger — a half-wired filter would
//     dispatch some records and skip others, which is silently
//     confusing).
//
// Records that don't match OR hit a per-record parse error are
// Ack'd at the broker HERE so the offset is committed and the
// message is NOT redelivered. The dispatch tick never sees
// them again. This is the "Ack-only-after-row-exists" guarantee
// extended to "Ack-only-after-filter-evaluated" — a record whose
// filter rejects it should not pile up in poller.inFlight until
// the next tick re-polls it from the broker.
//
// Returns (filtered, errorCount, nil) on the common path. The
// only path that returns a non-nil error is when the WHOLE tree
// decode fails (the column is malformed JSON); per-record parse
// errors are counted but do NOT propagate (they're per-record
// and don't poison the rest of the batch).
func (l *Loop) filterBatch(
	ctx context.Context,
	t sqlc.Trigger,
	batch []SourceRecord,
) ([]SourceRecord, int, error) {
	if len(t.FilterCriteria) == 0 {
		return batch, 0, nil
	}
	// Decode the JSONB column once per tick. The filter tree is
	// small (< 1 KB even for rich filters); the per-record
	// evaluation dominates the cost.
	var fc FilterCriteria
	if err := json.Unmarshal(t.FilterCriteria, &fc); err != nil {
		// Catastrophic — the whole tree is malformed. Return
		// the error so the caller drops the tick. The records
		// are still Ack'd by the caller so the broker doesn't
		// replay them on the next poll.
		return nil, 0, fmt.Errorf("decode filter_criteria: %w", err)
	}
	if l == nil {
		return batch, 0, nil
	}
	out := make([]SourceRecord, 0, len(batch))
	errCount := 0
	for _, rec := range batch {
		matched, ce := fc.MatchCount(rec.Payload, rec.Headers)
		// CRIT-2 (PR #993 / issue #757 closure): MatchCount
		// surfaces the per-clause error count so the dispatcher
		// can audit per-record. A clause error no longer aborts
		// the match — the record is dropped (Ack'd, no DLQ) and
		// counted toward the trigger.filter_error audit row
		// emitted at the call site.
		if ce > 0 {
			errCount += ce
			_ = ackSingle(ctx, t, rec, l)
			continue
		}
		if !matched {
			// Filter rejected the record. Ack so the
			// broker doesn't replay it; no audit row
			// (the skip is silent by design — ADR-118
			// §"Skip audit policy").
			_ = ackSingle(ctx, t, rec, l)
			continue
		}
		out = append(out, rec)
	}
	return out, errCount, nil
}

// ackSingle is a thin wrapper that finds the poller for `t`
// and Ack's `rec.ItemIdentifier`. Pulled out of filterBatch so
// the per-record error path is one line in the hot loop. The
// poller lookup uses the same map the dispatch tick maintains;
// we don't cache it locally because the map mutation lives on
// the Loop and a per-tick snapshot would race the cache
// invalidation on enable/disable transitions.
func ackSingle(ctx context.Context, t sqlc.Trigger, rec SourceRecord, l *Loop) error {
	if l == nil || l.triggerPollers == nil {
		return nil
	}
	poller, ok := l.triggerPollers[t.ID.String()]
	if !ok {
		// No poller registered yet — shouldn't happen on the
		// first filterBatch call (dispatchOneTrigger just
		// registered one), but a defensive nil-guard is
		// cheap.
		return nil
	}
	if poller == nil {
		return nil
	}
	return poller.Ack(ctx, t, []string{rec.ItemIdentifier})
}
