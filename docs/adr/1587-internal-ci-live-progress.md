# ADR-1587: Bounded internal CI live progress and log following

- Status: implemented behind existing default-off CI gates; native qualification pending
- Date: 2026-10-17
- Extends: ADR-1571, ADR-1574, ADR-1651 and ADR-1652

## Decision

Add live step snapshots and resumable logs to Jobs-backed CI. The guest reports
diagnostics through its current scoped runtime capability, and the controller
observes them while the task runs. Sealed final output and confirmed native stop
remain necessary for a terminal result. All flags remain off by default and the
release remains paused.

Runtime control advertises `progress_enabled` when the current task can append
progress. New runners require this capability before sending progress or optional
step timestamps. Against older control endpoints they retain the original final
output protocol; older runners ignore the new control field. Step times are
guest-reported diagnostics, never billing, lease or native deadline authority.
Start times cannot regress, completed step times cannot change, and durations
are bounded by the existing maximum job timeout plus finalization grace.

`PUT /v1/internal/ci/runtime/{run}/progress` accepts only:

```json
{"sequence":1,"log_offset":0,"logs":"aGVsbG8K","log_truncated":false,"steps":[{"name":"unit","status":"running"}]}
```

The route requires trusted HTTPS, the current task/instance capability, existing
internal/GitHub/dispatch/transport gates, current account/repository/template
policy, an unexpired native lease and a running instance. Cancelled, sealed,
stopped or replaced grants cannot append or replay. No grant issuance, admission
or native start exists here. Account Authorization headers, cookies, query
overrides, duplicate/unknown JSON fields and foreign or malformed proofs are
refused. Success returns `sequence` and `next_offset`, with no-store caching.

## Durability and bounds

Each existing `ci_job_attempts` row retains one `progress` snapshot plus
`progress_sequence` and `progress_digest`. The runtime transaction follows the
existing account -> execution -> attempt lock order and rechecks authority after
body decoding, with no outbound calls under locks. A sequence must equal the
previous sequence plus one and its offset must equal the retained prefix length.
Repeating the latest sequence requires the same canonical SHA-256 payload digest
and returns its original receipt. Historical, conflicting, regressing and
out-of-order writes cannot duplicate bytes or replace steps. No-ops are refused.

| Bound | Value |
|---|---|
| Retained prefix per attempt | Existing 64 KiB |
| Log bytes per update | Existing 16 KiB page |
| Encoded progress request | 32 KiB |
| Progress snapshot per attempt | 128 KiB |
| Accepted updates per attempt | 4,096 |
| Periodic guest publication | 1 second |
| Progress I/O deadline | 2 seconds |
| Transport attempts | Existing 3, within that deadline |

Logs are raw bytes encoded as JSON base64. Truncation is sticky and can only be
asserted at the prefix bound. Progress is bounded metadata under existing
execution/attempt lifetime limits, rather than a new object-storage stream.
The sender retains one pending update across lost responses, serializes
publication and coalesces snapshots. Command/log writes never wait on telemetry
I/O. Publication failure or exhaustion does not restart commands or replace the
final seal. Final progress flushing has a separate bounded deadline before
normal output finalization.

## Reconciliation and final output

`JobsBackend.Observe` feeds progress through the existing controller, preserving
ordered steps and serving only the requested log page. Even completed guest
steps produce a running attempt until the native owner confirms stop. Controller
and apid restart retain the same attempt, checkpoints and cursors.

Sealing checks known steps/times and log lengths. After native stop the existing
collector verifies final bytes/digests and requires an exact extension of the
live prefix. Corrupt or conflicting sealed output remains a repair condition and
cannot certify success.

If native cleanup finishes without a seal, freeze an explicit platform failure
(or native cancellation) with `output_incomplete=true`. Preserve the partial
prefix and completed steps; close unfinished steps as cancelled and append the
safe existing incomplete-output diagnostic where capacity permits. Clamp fallback
diagnostic durations to the existing bound so a skewed guest clock cannot block
cleanup. Partial progress cannot claim success or replace streamed bytes. Native
status and exit codes retain their existing precedence; late guest writes remain
revoked.

Progress fields are operational clone inventory. Archive compaction removes raw
progress together with other output, retaining sequence/digest evidence and
attempt identity. The replay-safe append-only migration refuses rollback while
progress or sequence evidence exists. Account deletion follows existing attempt
ownership and native cleanup requirements.

## Operator following and qualification

```sh
gregalectl ci execution logs --id EXECUTION_UUID --attempt ATTEMPT_UUID --follow
gregalectl ci execution logs --id EXECUTION_UUID --attempt ATTEMPT_UUID --follow --offset NEXT_OFFSET
```

The existing protected log API serves reconciled progress. Following validates
each page before emitting it, waits at the live end, retries bounded transient
failures from the same cursor, and stops retrying expired or unauthorized access.
Ctrl-C stops watching and prints the last committed cursor. Reconnect with that
offset; no command is resubmitted. JSON mode emits one page per line. Truncation
does not claim the task stopped: following waits for terminal completion or
operator cancellation. Existing account/admin/MFA/HTTPS controls apply; retained
diagnostics can be read with compute and publication disabled.

Portable qualification covers binary paging, step timing/order, concurrent
replay, lost acknowledgements, restart, strict HTTP requests, stale runtime
leases, gates, transaction rollback, explicit incomplete cleanup and following.
Native/provider verification remains with the separate native agent: build a new
pinned runner image, observe a long real step, restart apid/controller, lose an
acknowledgement and cancel/kill a guest. Confirm the same prefix, account
isolation and confirmed-stop final result. Keep the previously frozen
qualification bundle untouched. No enablement, image publication, deployment or
release is authorized by this work.

Before downgrading binaries that predate progress support, drain active
progress-enabled attempts using the current binaries. Schema retention alone
does not give old collectors the new live-prefix and timing contracts.
