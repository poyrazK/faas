# ADR-1403 · Deadline-aware application outbox retry policy

Date: 2026-10-15
Status: Accepted

## Context

Fixed-delay recovery can synchronize many application workers during an outage.
Rate-limit guidance must delay publication, and operators need a bounded number
of recovery claims without losing the existing uncertainty and retention fences.

## Decision

Add an opt-in worker retry policy with capped exponential jitter and support for
Retry-After on 429/503. Derive randomized delay from domain-separated claim entropy
before scheduling; avoid a new random-source failure after an uncertain send.
Use database-clock persisted due times and clamp all guidance to the immutable
retry deadline. Pass HTTP-date guidance directly to PostgreSQL.

Use the existing durable claim count for an optional lifetime attempt limit.
Check it under the publication row lock and block final uncertain settlement with
an explicit attempts_exhausted code. Confirmation and permanent-rejection handling
retain priority over attempt exhaustion; expired work still cannot publish.

For policy-bearing outbox requests, bypass the SDK transport retry loop so the
outbox owns send scheduling and can observe guidance. Preserve legacy fixed
backoff and other SDK requests. Policy configuration remains application-owned
and must agree across workers; no per-row immutable policy is introduced.

Extend the fresh application schema's last_code CHECK and provide an explicit
transactional upgrade fragment for existing installs. No platform migration or
clone registry entry is needed. Parameterized local SQL follows the existing
outbox static SQLC validation exception.

## Consequences

Applications can spread retries and honor server delays without extending
publication retention or minting keys. Crash-before-send claims consume attempt
budget, and an exhausted row may already have been accepted remotely. Reconciliation
and explicit policy changes remain operator decisions; resume does not reset
counts. Custom transports remain responsible for hidden retries and cancellation.

See [policy semantics and upgrade](../event-outbox-retry-policy.md).
