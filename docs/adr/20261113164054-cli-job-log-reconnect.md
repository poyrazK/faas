# ADR-20261113164054: Recover live CLI job logs after a disconnect

Status: Accepted
Date: 2026-11-13
Related: ADR-1163 (live job task output)

## Decision

The CLI's shared job-task log follower reconnects after an unexpected EOF,
network failure, or the stream's `timeout` or `node_unreachable` end reason.
One invocation permits three reconnects, after 1, 2 and 4 seconds; receiving
more output does not replenish that budget. Cancellation interrupts both an
active response and its backoff. HTTP refusals and malformed end frames are
final. Reconnecting reads logs only: it never submits or retries a Job.

On every connection, replay the current attempt's retained ring from zero.
Record the last successfully printed sequence per `instance`, suppress already
printed sequences, and preserve stdout/stderr routing. A numeric `since_seq`
alone is unsafe after a disconnect: the gateway applies it to the first
current VM, whose attempt and sequence numbers may have changed. Replaying
the bounded retained ring avoids that race and requires no gateway upgrade.

An attempt change across a disconnect marks output possibly incomplete, even
when the new attempt starts at sequence 1. A reopened stream that ends without
identifying its VM also cannot prove completeness. Existing gap frames and
sequence jumps remain visible. A replay gap covering only already printed
lines is ignored once the first retained line proves the overlap; an
unresolved gap remains incomplete. These cases preserve the existing warning
and stored-output recovery command rather than printing the stored copy again.

Older streams without instance/sequence metadata still print normally. Once
such output has been printed, automatic reconnect is disabled because it
cannot prevent duplicates. If metadata disappears on a resumed stream, stop
before printing unidentifiable output. Output-write failures are final and do
not advance the cursor. Close and drain each decoder before reconnecting so a
buffered parser cannot be left blocked after cancellation or a write failure.
Reconnect failures use the CLI's credential redactor before appearing in an
incomplete-output warning.

## Scope and verification

The shared follower serves `job logs --follow` and the live-output phase of
`run --image`. Existing exit codes, stored-output fallback, JSON restrictions,
server retention, stream backstop, execution budgets and Job retry policies
remain unchanged. There are no new flags, API fields, quotas or migrations.

Tests cover overlap, lost retention, VM changes and sequence resets, absent
metadata, malformed frames, HTTP refusals, exhausted reconnect budgets,
cancellation and failed output writes. Built-CLI journeys inject a disconnect
and verify exact output and SIGINT exit 130; a read-only live smoke journey
can follow an existing successful task when one is available.
