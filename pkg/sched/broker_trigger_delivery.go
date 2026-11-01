// broker_trigger_delivery.go — delivery bookkeeping for external broker
// triggers (Kafka, NATS JetStream, Redis Streams, SQS-compatible, AMQP):
// what happens to records a batch leaves out, how a redelivery of a settled
// record settles, how a cooling retry is held at the broker, how a long batch
// keeps its claims, and how an earlier release's item identifier maps onto
// its record (blocker hunt 2026-11-01 #6, ADR-20261101093512).

package sched

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// triggerDeferrer hands records back to the poller unsettled: the next Poll
// serves them again and they spend no delivery attempt. A poller without it
// gets a retryable Nack instead.
type triggerDeferrer interface {
	Defer(ctx context.Context, t sqlc.Trigger, ids []string) error
}

// triggerRetryScheduler holds a retried record at the broker until its
// stored next_fire_at (until[i] for ids[i]) instead of handing it back on
// every poll while it cools. A poller without it gets a retryable Nack.
type triggerRetryScheduler interface {
	NackUntil(ctx context.Context, t sqlc.Trigger, ids []string, until []time.Time) error
}

// externalBrokerPoller reports whether poller reads an external broker. The
// in-platform queue and realtime pollers keep their own lease recovery and
// terminal semantics, so the batch-cap and dead-letter redelivery handling
// here leaves them alone.
func externalBrokerPoller(poller triggerSource) bool {
	switch poller.(type) {
	case *queuePoller, *realtimePoller:
		return false
	}
	return true
}

// triggerBatchSplit divides one poll by the trigger's batch caps.
type triggerBatchSplit struct {
	// batch is dispatched now, in poll order.
	batch []SourceRecord
	// carried did not fit this batch; they go back to the poller and come
	// first in a later one, keeping their attempts.
	carried []SourceRecord
	// oversized each carry a payload larger than payload_max_bytes, so no
	// batch can take them; they are dead-lettered as payload_too_large.
	oversized []SourceRecord
}

// closeBatch splits the polled records by the per-trigger batch_size_max and
// payload byte cap. A sizeMax or byteCap of zero or less means no cap (rows
// always carry both; the SQL CHECK admits payload_max_bytes 1 KiB–64 MiB).
// Once one record is carried, every later one is carried too, so a batch
// never runs a later record ahead of an earlier one that did not fit.
func closeBatch(records []SourceRecord, sizeMax, byteCap int) triggerBatchSplit {
	if sizeMax <= 0 {
		sizeMax = len(records)
	}
	var split triggerBatchSplit
	total, full := 0, false
	for _, r := range records {
		switch {
		case byteCap > 0 && len(r.Payload) > byteCap:
			split.oversized = append(split.oversized, r)
		case full || len(split.batch) >= sizeMax || (byteCap > 0 && total+len(r.Payload) > byteCap):
			full = true
			split.carried = append(split.carried, r)
		default:
			total += len(r.Payload)
			split.batch = append(split.batch, r)
		}
	}
	return split
}

// settleUnbatchedTriggerRecords hands the records a batch left out back to
// the broker and dead-letters the oversized ones. Before this, both stayed
// with the poller unsettled: Kafka stopped committing the partition, Redis
// kept the entry at the head of the pending list, and NATS redelivered it
// until MaxDeliver dropped it.
func (l *Loop) settleUnbatchedTriggerRecords(ctx context.Context, store storeLike, t sqlc.Trigger, poller triggerSource, split triggerBatchSplit) {
	if !externalBrokerPoller(poller) {
		return
	}
	if len(split.carried) > 0 {
		l.deferTriggerItems(ctx, t, poller, batchItemIDs(split.carried))
	}
	if len(split.oversized) > 0 {
		l.deadLetterOversizedTriggerRecords(ctx, store, t, poller, split.oversized)
	}
}

// deferTriggerItems hands items back for a later batch without spending an
// attempt.
func (l *Loop) deferTriggerItems(ctx context.Context, t sqlc.Trigger, poller triggerSource, items []string) {
	var err error
	if deferrer, ok := poller.(triggerDeferrer); ok {
		err = deferrer.Defer(ctx, t, items)
	} else {
		err = poller.Nack(ctx, t, items, triggerReasonBrokerError)
	}
	if err != nil {
		l.log.Warn("sched trigger tick: hand back records left out of the batch",
			"trigger_id", t.ID.String(), "records", len(items), "err", err)
	}
}

// deadLetterOversizedTriggerRecords records each oversized record without
// its payload, dead-letters it as payload_too_large and settles the broker
// delivery as a poison record: Kafka commits it, or holds it at the head of
// its partition under the seek-to-offset strategy; NATS terminates it; Redis
// and SQS remove it. A redelivery of one already dead-lettered settles from
// its record, and one another dispatcher still holds goes back to the broker.
func (l *Loop) deadLetterOversizedTriggerRecords(ctx context.Context, store storeLike, t sqlc.Trigger, poller triggerSource, oversized []SourceRecord) {
	admitted := make([]SourceRecord, 0, len(oversized))
	var unstored []string
	for _, rec := range oversized {
		if _, err := store.InsertTriggerRecord(ctx, t.ID.String(), rec.ItemIdentifier, []byte("{}"),
			marshalJSON(rec.Headers), oversizedRecordMetadata(rec)); err != nil {
			l.log.Warn("sched trigger tick: insert oversized record",
				"trigger_id", t.ID.String(), "item_identifier", rec.ItemIdentifier, "err", err)
			unstored = append(unstored, rec.ItemIdentifier)
			continue
		}
		admitted = append(admitted, rec)
	}
	if len(unstored) > 0 {
		l.releaseTriggerItems(ctx, t, poller, unstored, nil)
	}
	remaining, _, err := ackTerminalTriggerRedeliveries(ctx, store, poller, t, admitted)
	if err != nil {
		l.log.Warn("sched trigger tick: oversized record redelivery", "trigger_id", t.ID.String(), "err", err)
		return
	}
	items := batchItemIDs(remaining)
	if len(items) == 0 {
		return
	}
	detail := fmt.Sprintf(`{"payload_max_bytes":%d}`, t.PayloadMaxBytes)
	finalized := l.deadLetterAllForApp(ctx, t.AppID.String(), t.AccountID.String(), t.ID.String(),
		items, triggerReasonPayloadTooLarge, detail, store)
	l.observeESMRecordOutcome(t.Kind, wire.ESMRecordOutcomeDeadLetter, len(finalized))
	reasons := make([]string, len(finalized))
	for i := range reasons {
		reasons[i] = triggerReasonPayloadTooLarge
	}
	l.nackTerminalItems(ctx, t, poller, finalized, reasons, nil)
	if unsettled := missingItemIDs(items, finalized); len(unsettled) > 0 {
		l.releaseTriggerItems(ctx, t, poller, unsettled, nil)
	}
}

// oversizedRecordMetadata is the broker metadata of an oversized record plus
// payload_omitted_bytes: the record keeps no payload, so a replay cannot run
// it from storage and the dashboard says why.
func oversizedRecordMetadata(rec SourceRecord) []byte {
	meta := make(map[string]any, len(rec.Metadata)+1)
	for k, v := range rec.Metadata {
		meta[k] = v
	}
	meta["payload_omitted_bytes"] = len(rec.Payload)
	return marshalJSON(meta)
}

// releaseTriggerItems hands items back to the broker for a later delivery.
// An item with a known due time goes to a poller that can hold it until then;
// every other item gets a retryable Nack.
func (l *Loop) releaseTriggerItems(ctx context.Context, t sqlc.Trigger, poller triggerSource, items []string, due map[string]time.Time) {
	if len(items) == 0 {
		return
	}
	if scheduler, ok := poller.(triggerRetryScheduler); ok && len(due) > 0 {
		var timed, rest []string
		var until []time.Time
		for _, item := range items {
			if at, ok := due[item]; ok {
				timed, until = append(timed, item), append(until, at)
			} else {
				rest = append(rest, item)
			}
		}
		if len(timed) > 0 {
			if err := scheduler.NackUntil(ctx, t, timed, until); err != nil {
				l.log.Warn("sched trigger tick: hold retried records", "trigger_id", t.ID.String(), "err", err)
			}
		}
		items = rest
	}
	if len(items) == 0 {
		return
	}
	if err := poller.Nack(ctx, t, items, triggerReasonBrokerError); err != nil {
		l.log.Warn("sched trigger tick: poller nack", "trigger_id", t.ID.String(), "err", err)
	}
}

// settleDeadLetterRedeliveries settles redeliveries of dead-lettered records
// as their dead letter settled them: an exhausted record as max_attempts
// (Kafka commits it under every strategy), a rate-limited one with an Ack,
// and every other reason as a poison record, which Kafka's seek-to-offset
// strategy keeps holding until an operator acts.
func settleDeadLetterRedeliveries(ctx context.Context, t sqlc.Trigger, poller triggerSource, items, reasons []string) error {
	var acked, exhausted, poison []string
	for i, item := range items {
		switch reasons[i] {
		case triggerReasonMaxAttempts:
			exhausted = append(exhausted, item)
		case triggerReasonRateLimited:
			acked = append(acked, item)
		default:
			poison = append(poison, item)
		}
	}
	var errs []error
	if len(acked) > 0 {
		errs = append(errs, poller.Ack(ctx, t, acked))
	}
	if len(exhausted) > 0 {
		errs = append(errs, poller.Nack(ctx, t, exhausted, triggerReasonMaxAttempts))
	}
	if len(poison) > 0 {
		errs = append(errs, poller.Nack(ctx, t, poison, triggerReasonPoisonRecord))
	}
	return errors.Join(errs...)
}

// writeTriggerClaimOutcome writes a claimed record's outcome, retrying
// transient store errors so a database blip does not leave the broker
// delivery unsettled. ErrNotFound is final: the claim lapsed or moved on.
func writeTriggerClaimOutcome(ctx context.Context, write func() error) error {
	backoff := api.TriggerClaimSettleBackoff
	var err error
	for attempt := 1; ; attempt++ {
		if err = write(); err == nil || errors.Is(err, state.ErrNotFound) || attempt >= api.TriggerClaimSettleAttempts {
			return err
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(err, ctx.Err())
		case <-timer.C:
		}
		backoff *= 2
	}
}

// renewTriggerClaims keeps claimed's claims alive while their dispatch runs:
// a long-timeout app's batch may wait 16.5 minutes, past the ten-minute
// claim. Without renewal the outcome could not be recorded, the broker
// delivery was never settled, and the record ran again. The returned stop
// ends the renewal and waits for it; calling it again does nothing.
func (l *Loop) renewTriggerClaims(ctx context.Context, store storeLike, t sqlc.Trigger, claimed []sqlc.TriggerRecord) func() {
	renewer, ok := store.(state.TriggerClaimRenewer)
	if !ok || len(claimed) == 0 {
		return func() {}
	}
	ids := make([]string, 0, len(claimed))
	generations := make([]int64, 0, len(claimed))
	for _, record := range claimed {
		if record.ClaimGeneration > 0 {
			ids, generations = append(ids, record.ID.String()), append(generations, record.ClaimGeneration)
		}
	}
	if len(ids) == 0 {
		return func() {}
	}
	interval := l.triggerClaimRenewInterval
	if interval <= 0 {
		interval = api.TriggerRecordClaimRenewInterval
	}
	renewCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
			}
			n, err := renewer.RenewTriggerRecordClaims(renewCtx, t.ID.String(), ids, generations, api.TriggerRecordClaimLease)
			switch {
			case err != nil && renewCtx.Err() == nil:
				l.log.Warn("sched trigger tick: renew record claims", "trigger_id", t.ID.String(), "err", err)
			case err == nil && n < int64(len(ids)):
				l.log.Warn("sched trigger tick: record claims lapsed during dispatch",
					"trigger_id", t.ID.String(), "held", len(ids), "renewed", n)
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}

// resolveLegacyTriggerItems maps records onto rows an earlier release stored
// under its own item identifier. Kafka identifiers were partition-offset and
// NATS ones the stream sequence, so a new topic or a recreated stream reused
// them and new records were acknowledged as already done
// (ADR-20261101093512). A legacy row is used while it is still in progress,
// or when the Kafka topic stored with it matches; otherwise the record keeps
// its new identifier. The returned poller translates the legacy identifiers
// back to the broker handles. The next release can drop this once rows from
// before it have aged out.
func resolveLegacyTriggerItems(ctx context.Context, store storeLike, t sqlc.Trigger, poller triggerSource, batch []SourceRecord) ([]SourceRecord, triggerSource, error) {
	reader, ok := store.(state.TriggerRecordStateReader)
	if !ok {
		return batch, poller, nil
	}
	var lookup []string
	for _, rec := range batch {
		if len(rec.LegacyIdentifiers) > 0 {
			lookup = append(append(lookup, rec.ItemIdentifier), rec.LegacyIdentifiers...)
		}
	}
	if len(lookup) == 0 {
		return batch, poller, nil
	}
	states, err := reader.TriggerRecordStatesByItems(ctx, t.ID.String(), lookup)
	if err != nil {
		return nil, poller, errors.Join(fmt.Errorf("dispatch trigger legacy record lookup: %w", err),
			poller.Nack(ctx, t, batchItemIDs(batch), triggerReasonBrokerError))
	}
	stored := make(map[string]state.TriggerRecordItemState, len(states))
	for _, s := range states {
		stored[s.Item] = s
	}
	alias := map[string]string{}
	out := make([]SourceRecord, 0, len(batch))
	for _, rec := range batch {
		if _, current := stored[rec.ItemIdentifier]; !current {
			if legacy, ok := legacyRecordFor(rec, stored); ok {
				alias[legacy] = rec.ItemIdentifier
				rec.ItemIdentifier, rec.StableIdentifier = legacy, legacy
			}
		}
		out = append(out, rec)
	}
	if len(alias) == 0 {
		return out, poller, nil
	}
	return out, &itemAliasPoller{triggerSource: poller, alias: alias}, nil
}

// legacyRecordFor returns the legacy identifier of rec's stored row, if one
// belongs to rec.
func legacyRecordFor(rec SourceRecord, stored map[string]state.TriggerRecordItemState) (string, bool) {
	topic, _ := rec.Metadata["topic"].(string)
	for _, legacy := range rec.LegacyIdentifiers {
		row, ok := stored[legacy]
		if !ok {
			continue
		}
		switch row.State {
		case "pending", "claimed", "retry":
			return legacy, true
		}
		if topic != "" && row.Topic == topic {
			return legacy, true
		}
	}
	return "", false
}

// itemAliasPoller maps the item identifiers a record is stored under back to
// the broker handles the wrapped poller knows; other identifiers pass
// through.
type itemAliasPoller struct {
	triggerSource
	alias map[string]string
}

func (p *itemAliasPoller) handles(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if handle, ok := p.alias[id]; ok {
			id = handle
		}
		out = append(out, id)
	}
	return out
}

func (p *itemAliasPoller) Ack(ctx context.Context, t sqlc.Trigger, ids []string) error {
	return p.triggerSource.Ack(ctx, t, p.handles(ids))
}

func (p *itemAliasPoller) Nack(ctx context.Context, t sqlc.Trigger, ids []string, reason string) error {
	return p.triggerSource.Nack(ctx, t, p.handles(ids), reason)
}

func (p *itemAliasPoller) Defer(ctx context.Context, t sqlc.Trigger, ids []string) error {
	if deferrer, ok := p.triggerSource.(triggerDeferrer); ok {
		return deferrer.Defer(ctx, t, p.handles(ids))
	}
	return p.triggerSource.Nack(ctx, t, p.handles(ids), triggerReasonBrokerError)
}

func (p *itemAliasPoller) NackUntil(ctx context.Context, t sqlc.Trigger, ids []string, until []time.Time) error {
	if scheduler, ok := p.triggerSource.(triggerRetryScheduler); ok {
		return scheduler.NackUntil(ctx, t, p.handles(ids), until)
	}
	return p.triggerSource.Nack(ctx, t, p.handles(ids), triggerReasonBrokerError)
}
