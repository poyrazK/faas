# ADR-1014: Recovery queue visibility and operator pause/resume

## Decision

Expose an app-scoped read-only automatic recovery queue and operational pause/resume controls. Keep configuration version and frozen retry plans unchanged by controls. Full policy replacement retains its existing version increment and obsolete-intent suppression behavior.

`GET /v1/apps/{slug}/recovery-policy/queue` returns a consistent snapshot of the policy, total current-version pending intents, unresolved applied intents across versions, hourly execution charges, remaining budget and cooldown expiry. At most `EventRecoveryHistoryPageMax` entries are returned in review observation/identifier/position order; totals and `truncated` describe omitted entries. Charges remain independent of review retention. Outstanding means final proof has not been recorded, not that the workflow is running. Reasons describe the last worker evaluation. Read-only snapshots never authorize execution.

`POST /v1/apps/{slug}/recovery-policy/control` accepts pause/resume, a reason, and the observed configuration version, `updated_at`, and nullable pause timestamp. All three authority values must match atomically. The policy must be enabled and in automatic retry mode. A control changes `updated_at`, stores the trusted actor, and appends a durable audit record including the reason. Pause stores its operator reason and stops preparation and automatic application. Committed children continue. Resume clears the pause and consecutive failure streak, retaining pending intents and spent budgets. No cancellation or child creation occurs in the control request.

Postgres controls serialize on the same policy row as automatic execution. Once a pause commits, a worker cannot commit a new child using the prior unpaused state. A child committed before the pause remains admitted work. The memory implementation uses the shared mutex and rechecks control timestamps after releasing the mutex for result inspection. Failure pauses also participate in control compare-and-swap through their pause timestamp, although scheduler evaluation does not change `updated_at`.

Read access and MFA protect queue snapshots; deploy write access and MFA protect controls. Existing automatic failure-pause notifications remain historical, once per policy version. Operator controls do not emit that failure event. Audit records are retained for the lifetime of the policy/app; migration rollback refuses to discard existing control evidence.

## Consequences

Operators can halt preparation and automatic execution without reauthoring settings or suppressing pending retries. Resumption revalidates every frozen target against live evidence and charges. A stale or duplicate control returns conflict and must be reconciled by reading the current policy. Unknown or superseded evidence remains unresolved and can still block the serial execution slot; inspect the referenced review status for current execution evidence. Queue visibility does not suppress individual actions or alter existing manual review decisions.
