# ADR: Prometheus exposition for Python preparation metrics

- Status: Accepted
- Date: 2026-10-17

## Context

Python now collects bounded preparation counters and histograms. Applications
still need a consistent Prometheus mapping for fixed labels, cumulative buckets,
duration units and saturation. Node already defines that exposition contract.

## Decision

Add the pure `format_event_acknowledgment_preparation_metrics` function and a
public Prometheus text 0.0.4 content-type constant. Match Node's six counter
families, two classic histograms in seconds and three-scope saturation gauge,
including zero series, fixed HELP/TYPE metadata and a final newline. Provide a
keyword-only bounded ASCII namespace prefix with the same default. Convert whole
milliseconds with integer decimal formatting, avoiding intermediate float rounding.

Copy and validate complete current snapshots before rendering. Accept exact native
dictionaries/lists and primitive keys/values only, sharing the collector's fixed
vocabularies and bounds. Validate safe nonnegative integers, exact keys, cumulative
buckets, infinity/count equality, zero-count sums and saturation implication.
Reject malformed inputs/prefixes with a generic `ValueError` outside validation
exception handling, without forwarding a raw cause or rejected input.

Use fixed iteration order for repeatable text regardless of input key order.
Retain no snapshots; add no exporter dependency, global registry, listener, task,
timer, arbitrary labels, timestamps, OpenMetrics EOF or delta conversion.

Provide an explicit HTTP-only ASGI application factory in the protected-job
example. It reads the same collector on the processing event-loop thread before
any response await and emits generic 503 for validation errors. Its host supplies
a single-process ASGI server with lifespan disabled, loopback binding, listener
failure handling, protected remote access and shutdown. Importing starts nothing.

## Consequences

Applications expose absolute cumulative observations from an independent scrape
path. They own process identity, reset detection, collector access serialization
and aggregation. Metrics retain possible gaps, duplicates and clipping; they grant
no ownership or proof of callback success, deduplication or active work. Receipt,
claim and durable-intent protocols remain recovery evidence. No platform schema,
route, migration, registry or processing protocol changes are needed. See the
[format and scrape contract](../event-acknowledgment-python-prometheus.md).

## Validation

Use compilation/lint, Python 3.11 syntax, isolated generation, wheel/sdist inclusion,
static family/label/bucket/source comparisons and schema/ADR/documentation checks.
Add no tests, change no assertions and run no local tests. Hosted acceptance for
text parsing, seconds/infinity, invalid snapshots, repeated rendering, cumulative
counts and scrape responses remains pending before release.
