# ADR-1027: Object storage compute clients and write canaries

Status: Accepted

## Context

Managed storage bindings inject six settings and existing release checks fence
probe evidence against binding/runtime revisions. The legacy canary only lists
a bucket, which cannot validate object transfer or write-only credentials.
Applications also need a consistent way to rebuild clients after secret reload.

## Decision

Add factory-based runtime binding helpers to Go, Node and Python. Factories use
explicit managed credentials and path-style S3 configuration without imposing a
new SDK dependency. Reload publishes a complete client/bucket pair only after
the candidate succeeds. Failure disables acquisition. Applications read atomic
secret envelopes and ACK their revision/generation after all consumers apply;
helpers do not manufacture ACKs or claim credential validation from construction.

Keep the v1 read-only canary. An explicit CLI write opt-in uses reserved v2 argv
with a managed prefix and exact current permission. The API refuses unbound,
unauthorized, wrong-scope, mismatched and known Object Lock requests. Persist
the mode in the private admission pin; enforce mode/command correspondence in
memory and PostgreSQL. Rollback removes v2 evidence pins before restoring the
old constraint.

Write probes use one random, create-only tiny object. Read/write probes verify
its bytes and post-delete absence; write-only probes never read and record only
deletion acknowledgement. Delete the returned version to avoid historical
residue. Bound I/O and cleanup separately, disable retries/redirects and sanitize
reports. An ambiguous PUT, failed cleanup or report/mode mismatch cannot pass.
The normalized operation stages feed existing binding checks and promotion
gates. Read evidence does not become write evidence; existing promotion policy
does not silently require mutations. Batch verification never opts into writes.

## Consequences

Applications control client lifetimes, SDK versions and readiness policy.
Acquire the current pair per request, serialize reloads and retain old clients
while requests finish. API/migrations and guest-init must support v2 before
using the flag. A task-guest canary remains separate from resident application
ACK evidence. Write-only cleanup is acknowledged, not independently read back.
Cleanup is bounded best effort after ambiguous writes or process termination;
no crash-proof/durable cleanup guarantee is introduced.
