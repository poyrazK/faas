# ADR-20261115145412: Best-effort progress for CLI list exports

- Status: accepted
- Date: 2026-11-15

## Context

Private list exports expose their completion receipt only after all pages and
publication succeed. A long walk gives little feedback. Progress must preserve
atomic publication, stdout receipts, existing Problems, cancellation and the
new overall read budget without exposing inventory contents or connection data.

## Decision

Add local --progress off|plain|ndjson to build list and invocation list. Default
off remains silent and accepts existing invocations. Active progress requires
--all and output-flag presence, validated before authentication, API access or
private-file creation. Register that requires rule in the same registry used
by help and validation. Its active predicate means a validated non-off mode.
Keep the original two rules and their code/order contracts unchanged when the
new flag is absent. Expose optional file-mode progress metadata in JSON help.

Progress is best-effort stderr diagnostics with no execution authority, following
existing CLI conventions. A short/failed write disables further progress and
never changes primary reads, errors or publication. Keep native SIGPIPE behavior.
There is no background worker, polling request, automatic retry or resume.

Observe page completion through an optional callback in the existing shared
streamer; its public/default wrapper passes nil. Preserve cursor/page guards,
record order and short-write/cancellation behavior. Callbacks occur after all
rows in a validated page are written; empty pages still count. Error snapshots
use returned complete-record counts plus actual private bytes, including a torn
record's bytes. Never treat those private bytes as partial stdout.

Emit version-1 NDJSON records with fixed type list_export_progress, event,
pages_completed, items_emitted, bytes_written, elapsed_ms and published. Include
meaningful zeros/false and no rows, cursors, paths, filters, profiles, URLs,
credentials or error text. Plain mode prints the same counters. Start is immediate;
updates are limited to one per 250ms at page boundaries; terminal records bypass
the throttle. Elapsed time is monotonic and invocation-local. This is not a
heartbeat during a blocked API read or an interrupt for blocked local I/O.

Emit published=true only after the existing atomic link succeeds, before the
completion receipt. A receipt failure retains that completed artifact and its
publication event. A pre-publication failure emits `stopped` with `published: false`
before the existing Problem/human error, preserving code/category/exit/request ID.
Progress mode does not select JSON receipts or Problems; users select --json
when they need an entirely machine-readable diagnostic stream.

## Verification and consequences

The stdlib-only race suite exercises real private files, optional observers,
throttling/final snapshots, writer failures, fields/visibility and the expanded
32-state dependency matrix. Retain the original pagination/stream guard tests
and large-integer/receipt/exit contracts. Public CLI tests cover all receipt modes,
default silence, API Problems after stopping, broken progress sinks, receipt
failure after publication, pre-auth refusal and offline discovery. Update only
the prior discovery's expected rule count and extend its journey for the new
predicate/requirements; keep every original assertion for existing rules.

A built-CLI isolated journey observes progress before publication and checks
completion, API refusal, deadline and SIGINT. Full hosted exact-final-head CI
is mandatory: local core tests do not qualify dispatch, manifest lookup or the
journey. Regenerate only the two reference sections from production renderers.
