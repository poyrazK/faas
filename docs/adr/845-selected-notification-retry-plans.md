# ADR-845: Selected notification retry plans

Date: 2026-10-09
Status: Accepted

## Context

The app backlog identifies unresolved requests, but retrying selected failed receivers across jobs requires preserving each job's immutable request intent and identifying partial execution after transport failures.

## Decision

Add CLI-only `events notification-retry-plan --file SELECTION --output NEW_PLAN` and `events notification-retry-apply --file PLAN`. Compose existing Go SDK preview and idempotent retry APIs; no new server endpoint, storage, migration, or SDK wire model is needed.

Version 1 input identifies one canonical app UUID and one to ten distinct canonical job UUIDs. Each job carries an existing retry request with an operator-supplied stable request UUID and one to 100 distinct explicit delivery targets with generation guards. Strictly reject unknown fields, trailing JSON, invalid requests, and files over 1 MiB. Never discover or select receivers implicitly from backlog status. Keep operational limits in pkg/api/limits.go.

Preview validates all job/app identities with five-second reads and records observations and eligibility for exact selected targets. Generation and receiver mismatches are explicit. Save the prepared plan to a newly created private file without overwriting another plan. Preview eligibility is advisory and can change before application; it never authorizes changing generation guards or request IDs.

Apply requires a prepared plan, preflights all job/app identities before writes, then submits exact requests sequentially with five-second deadlines and interrupt cancellation. Do not block original request replays because the latest receiver is no longer eligible. Server decisions remain authoritative, and each job commits independently. Emit a JSON receipt even without --json, distinguishing decided, needs_reconciliation, and not_attempted. Queued is not delivery success.

Validate returned identity, target intent, and decision states. Treat any POST error or invalid response as uncertain, including HTTP errors, because a server may commit before its response is lost. Stop subsequent writes and return exit 2 (130 on interruption). Include a saved-history CLI command for each attempted request. Reapply the same plan to recover frozen decisions through server idempotency; never silently generate new request IDs or refresh guards. History can also reconcile an uncertain request. A missing receipt after pruning is not proof that execution never occurred.

The plan is an editable local artifact, not a signed approval token. Application checks structure and ownership again; edited requests remain subject to normal server validation and request-ID conflict detection. Plans should be reviewed before application. File/output errors do not erase durable server decisions: reconcile from the original plan IDs. No tests are added or executed per the user's standing instruction.

## Consequences

Operators can review exact selections before independently recovering failed deliveries across jobs. Partial batches and uncertain outcomes remain explicit. This workflow does not supply cross-job atomicity, automatic reconciliation, delivery completion waiting, or expanded retention.
