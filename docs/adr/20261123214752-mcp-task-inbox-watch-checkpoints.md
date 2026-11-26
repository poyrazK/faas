# ADR-20261123214752: Resume Task inbox watches from a local checkpoint

Date: 2026-11-24
Status: Accepted

## Decision

The shared CLI supports an optional private checkpoint for an inbox watch.
The checkpoint binds an endpoint and status filter to the fingerprints of
reported entries still visible in the last complete scan. The current bearer
credential keys the fingerprints.
A renewed credential sees a fresh history. The file stores the format
version, endpoint hash, status filter, and opaque fingerprints.

A watch holds an exclusive lock for the checkpoint path. Every scan starts
on the first page and the server applies current authorization and retention.
After writing events the CLI atomically saves the new fingerprints. An
interruption between stdout and the save can replay an event. Incomplete
scans leave the previous checkpoint available for restart.

## One-shot watches

A one-shot watch emits the first new or changed observation from a complete
scan. It saves that event and any previously reported observations still
visible in the scan. The other new observations stay eligible for later
invocations. An optional overall deadline bounds endpoint resolution,
scans, retries, and polling delays. Expiry before publication returns exit
code 124 and leaves stdout empty.
