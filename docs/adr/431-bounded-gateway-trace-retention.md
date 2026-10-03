# ADR-431 · Bound gateway diagnostic trace memory

- **Status:** accepted
- **Date:** 2026-10-02
- **Amends:** ADR-055 and ADR-070

Production load testing OOM-killed gatewayd-public at its 512 MiB cgroup cap.
The host still had free RAM. Its trace ring allowed 100,000 trace trees but
neither bounded their retained bytes nor the spans merged into one trace.
The original 25 MB estimate used JSON size instead of resident Go objects.
A local two-span HTTP reproduction retained approximately 200 MiB of live
heap at 100,000 traces, dominated by attribute maps. This establishes a large
retention contributor; a production heap profile from before the kill was
not available, so it does not attribute every byte of that incident.

Retain at most 64 MiB of conservatively accounted trace data, 100,000 trees
(the existing configurable count default), and 4096 spans per tree. All
platform bounds live in pkg/api/limits.go. Account string bytes and Go
object/map/index overhead, and own string backing storage and attribute maps
so borrowed buffers and later caller mutations cannot bypass accounting.
Growing trees evict older trees; a single oversized tree/batch is rejected
without changing the previously retained tree. Per-trace overflow omits new
spans. Export retained bytes/count, evictions, rejections and span losses on
the existing private metrics listener, with no trace or customer labels.

The canonical public gateway unit sets GOMEMLIMIT=384MiB beneath the existing
512 MiB hard cap, leaving cgroup headroom. This Go runtime soft limit assists
collection; the independent retention bounds control live heap. Operator
overrides remain possible. No public profiling endpoint is introduced.

The 24-hour diagnostic window is an upper retention age, subject to the
count/byte/span bounds; sustained traffic can evict traces sooner. Durable
request telemetry, customer OTLP export, request routing, VM admission and
billing are unchanged. Tests cover byte/count bounds, repeated-trace growth,
oversized updates, copy ownership, retention loss metrics and randomized
accounting sequences. Compare the same HTTP trace reproduction before/after
and qualify the public gateway rollout with restart/memory/latency evidence.

The standalone reproduction is `go run ./scripts/experiments/gateway-trace-retention`.
It emits live heap samples after collection at every 10,000 requests; this is
retention evidence, not a production throughput benchmark. `--heap-profile`
also writes `trace-retention.pprof` in the working directory. The same
100,000-request fixture measured retained heap growth of approximately
200 MiB before and 42 MiB after, with eviction beginning around 17,000 trees.
