# ADR: Prometheus exposition for Node preparation metrics

- Status: Accepted
- Date: 2026-10-17

## Context

The Node preparation collector provides bounded cumulative counters and duration
histograms. Each application currently has to map these snapshots to a monitoring
format, including unit conversion, cumulative buckets, fixed labels and clipping
flags. Inconsistent adapters can double-count snapshots or misreport durations.

## Decision

Add the pure `formatEventAcknowledgmentPreparationMetrics` function and a public
Prometheus text 0.0.4 content-type constant. Emit six counter families, two classic
histograms in seconds and a saturation gauge with three fixed scopes. Include all
zero-valued series, one HELP/TYPE pair per family and a final newline. Use a stable
default namespace with an optional bounded ASCII prefix chosen by the application.

Share the collector's exhaustive typed event/outcome/reason/stage vocabulary with
the formatter. Copy and validate complete snapshots and options before formatting:
own data properties, fixed keys and histogram bounds, safe nonnegative integer
values, cumulative buckets, matching infinity/count values and boolean flags.
Reject accessors, unexpected fields and malformed values with a generic TypeError.
Only fixed labels and help strings reach the output. No timestamps, arbitrary
labels, OpenMetrics EOF, delta conversion or runtime dependency are added.

## Consequences

Applications call the formatter on a fresh snapshot from their scrape path. It
does not mutate/reset the collector, register metrics globally, start a server or
publish over the network. Repeated reads expose absolute observations without
incrementing a second counter. Application hosts own scrape access, process/target
identity, lifecycle and any aggregation across collectors or registries.

An explicit loopback server example exposes the same long-lived collector used
by prepared jobs. No listener starts on import. Export errors yield a generic 503
before metrics headers are sent. Remote collection requires an application-owned
authenticated proxy or separate protected transport.

These remain process-local observations with possible gaps, duplicates, resets
and clipping. Saturation gauges do not measure coverage or callback success.
Recovery and deduplication continue to use receipts, claims and durable intents.
No platform schema, route, migration, registry or business protocol changes.
See the [format and scrape contract](../event-acknowledgment-node-prometheus.md).

## Validation

Use Node generation, compilation, package/declaration inclusion, example syntax
and static documentation checks. No tests or assertions are added or changed and
no local tests are run. Hosted acceptance must cover text parsing, seconds and
infinity conversion, cumulative buckets, fixed labels, invalid inputs, repeat
formatting and scrape responses before release.
