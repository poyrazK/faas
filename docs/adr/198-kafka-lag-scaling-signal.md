# ADR-198 · Kafka consumer lag as a scaling signal

- **Status:** accepted
- **Date:** 2026-09-21

## Context

ADR-194 closed the scaling-metric set at four signals and installed a test
that fails the build if a fifth is declared without a source. Its closing
section listed Kafka lag among the signals deliberately left out, on the
grounds that each "needs a source that does not exist yet (a consumer-group
reader…)".

That claim was wrong, and this ADR corrects it. `pkg/sched/poller_kafka.go`
is a consumer-group reader that has been running in production since issue
#757. It stamps every fetched record with the broker's `offset` and
`high_water_mark`, and `dispatch_triggers.go` already turns that pair into a
message count:

```go
func consumerLagFor(rec SourceRecord, kind string) (int64, bool) {
	...
	return high - offset - 1, true
}
```

The number the scheduler needs therefore already exists, is already computed
on every dispatch tick, and is already exported — as
`schedd_esm_consumer_lag_messages`. It reaches a Grafana panel and stops
there. An operator can watch a Kafka backlog grow on a dashboard while the
scheduler that could act on it never sees the number.

`queue_depth` does not cover this. The poller pulls at most `batchMax`
messages per tick (further bounded by the trigger's `BatchSizeMax`), so
Gregale's own queue reflects what has already been *pulled*, not what is
waiting. A topic with a million-message backlog and a batch size of 64
presents a queue depth of 64. Scaling on `queue_depth` for a Kafka-backed app
scales on the batch size, which is a constant.

## Decision

`kafka_lag` becomes the fifth metric in the ADR-194 closed set: the number of
messages behind the consumer group, across every partition of the app's Kafka
triggers.

```yaml
scaling:
  targets:
    - metric: kafka_lag
      value: 500      # messages of backlog per instance
```

It classifies as `ClassBacklog`, alongside `queue_depth`: the reading is
fleet-total rather than per-instance, so `desired = ceil(lag / target)` and
the fleet is hot when `lag > target × instances`. Every other property — the
arbitration against other declared signals, the cooldowns, the burst bound,
the plan cap — is inherited unchanged from ADR-194.

### Lag is observed per partition, and only while messages flow

segmentio/kafka-go's `Reader.Lag()` is documented to return **-1 when the
reader is backed by a consumer group**, and this poller always sets a
`GroupID`. `ReaderStats.Lag` is the same field. Neither is usable, so the only
available source is the `high_water_mark` carried on each fetched message —
which means lag can only be learned by consuming.

Two consequences follow, and both shape the design:

- **Lag is per-partition.** A fetched batch may span partitions, and each
  message's high-water-mark describes only its own. The freshest sample per
  partition is that partition's remaining backlog; the topic's backlog is the
  sum across partitions.
- **A silent partition reports nothing.** That is the correct reading rather
  than a gap: a partition stops producing records precisely because it has
  been drained, and a drained partition contributes zero to the backlog.
  Dropping it from the sum is the accurate answer.

### A stale reading is no reading

Samples carry the time they were taken, and the tracker reports no value once
the newest sample for an app passes `LagFreshness`. This is the distinction
ADR-194's arbiter already depends on: an observation with `Have=false` never
scales the app and never suppresses a sibling signal that does have a reading.

The failure this prevents is specific. If a schedd stops dispatching a trigger
— a broker outage, a crashed poller, a revoked partition assignment — the last
lag it saw is frozen. Treating a frozen number as current would pin the fleet
at whatever the backlog was when the world stopped, indefinitely, and bill for
it. Expiring to "no signal" lets the app scale down on its other signals, or
park.

### The tracker lives beside the arbiter, not inside either trigger

`pkg/sched/kafkalag` is a leaf package holding the per-app, per-partition
samples. It imports nothing from `pkg/sched`, for the same reason
`pkg/sched/scalesignal` does not: `pkg/sched/targets` and `pkg/sched/scaleup`
cannot import each other, and the schedd loop that writes the samples is in
`pkg/sched` itself. A leaf is the only place all three can reach.

## Consequences

A Kafka-backed worker scales on the backlog it actually has rather than on
the batch size its poller happens to use. Combined with ADR-194 an app can
declare `kafka_lag` alongside `cpu`, and the platform provisions for whichever
demands more — the consumer that is both behind and saturated gets capacity
for the worse of the two conditions.

The signal is only as live as the dispatch loop. An app whose Kafka trigger is
disabled, or whose schedd is not dispatching, reports no lag and scales on its
other declared signals. This is a deliberate trade: the alternative is an
independent metadata client per trigger, which buys a lag reading for a parked
app at the cost of a second broker connection, a second set of credentials in
memory, and a second failure mode. If a cold-start-on-backlog requirement
appears, that client is the way to serve it and it can be added behind the
same `kafka_lag` metric without changing the contract.

ADR-194's "out of scope" section is amended by this decision: its
parenthetical about a missing consumer-group reader was inaccurate. The two
signals still genuinely out of scope — custom application metrics and
schedule-based `max_instances` — remain so for the reasons given there and in
ADR-195.
