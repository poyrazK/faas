# ADR-662: Resumable Customer Operations recovery decisions

## Status

Accepted — 2026-10-06.

## Context

Recovery already deduplicates a stable ID and canonical request fingerprint.
After losing an HTTP response, CLI operators still reconstruct evidence and
results manually. The existing API returns mutable operation status, so it
cannot serve as an immutable acknowledgement of an earlier recovery decision.

## Decision

Add account-only `POST /v1/apps/{slug}/operations/{id}/recover-receipt`, using
existing recovery input, MFA, deploy:write, ownership, evidence, generation,
inspection revision, quota and pinned execution checks. Keep `/recover` and its
mutable operation response compatible. Both paths atomically retain a small
immutable decision alongside recovery creation in memory and PostgreSQL, for
HTTP and native workflow execution. Receipt replay validates the same request
and returns the original decision without authorizing another execution.

A decision contains its request fingerprint, resolution, observed/resulting
generation, state at acceptance, execution identity and acceptance timestamp.
Its expiry is the operation retention deadline immediately before the decision;
later execution or recovery cannot extend this receipt's replay horizon. Raw
input, result, evidence, execution credentials and storage keys are omitted.
Legacy recovery rows without a decision continue to deduplicate `/recover`;
`/recover-receipt` returns a conflict without fabricating historical facts or
repeating the decision. The nullable decision column is forward-only.

Add optional `recover --receipt-file PATH`. Before mutation, verify the API and
account, read the operation retention deadline and publish a bounded, private,
immutable request file. It freezes the exact evidence text, JSON result, decision
ID, generation and optional inspection revision. Resume requires only the same
app, operation ID and receipt path; supplied selectors must match exactly.
Credentials are never saved. Publish `PATH.decided.json` atomically after a
validated server response, bound to the request file SHA-256. A saved
acknowledgement describes the historical decision, never current work status.
Unconfirmed receipts cannot be sent after their conservative replay deadline.
Inspect/preview remain read-only and cannot create or apply a receipt.

## Consequences

Terminal loss and concurrent CLI retries no longer require rebuilding recovery
intent. Tests must cover lost responses, operation advancement, conflicting
requests, private files, account/origin changes, retention, legacy rows and native
confirmed-step reuse. This changes no automatic retry or launch admission policy.
