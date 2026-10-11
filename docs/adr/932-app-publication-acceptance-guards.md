# ADR-932: Acceptance guards for application publication reconciliation

Date: 2026-10-10
Status: Accepted

## Context

An app producer key can be newly accepted after receipt pruning. Matching its current retained content or reading successful consumers does not prove the same acceptance as a previously saved receipt.

## Decision

Add optional expected_accepted_at to the existing status GET and verification POST query strings. Preserve the existing publication body and unguarded behavior. Both read APIs return acceptance=same_acceptance, replacement_acceptance or unavailable only when guarded, plus the expected instant normalized to UTC. Keep routing/content status independent: a replacement can still have matching content. Return the current retained receipt/evidence even for replacement, clearly marked as belonging to the currently observed acceptance. Do not infer the expected acceptance's outcomes from a replacement.

Compare exact time.Time instants with the accepted_at obtained in the existing receipt/comparison snapshot. Do not truncate, round or apply a tolerance. Parse nonzero RFC3339 timestamps with up to nine fractional digits and valid timezone offsets; reject empty explicit query values, repeats, unknown keys, malformed dates, unsupported precision and UTC years outside 1..9999. Different offset spellings of the same instant compare equal. Preserve original precision from the saved receipt.

Expose ExpectedAcceptedAt on status query types and a variadic optional AppEventAcceptanceGuard on Go verification methods, preserving existing calls. Reject more than one option. Generate Node/Python query support and optional result fields. Query guards are RFC3339 strings so Python can preserve nanosecond input precision rather than converting the parameter to a microsecond datetime. Add --expected-accepted-at RFC3339 to both CLI commands. They continue printing the full JSON observation; replacement returns exit 3 ahead of ordinary match/conflict/status exits. Unavailable remains exit 2. Unguarded calls omit acceptance and expected_accepted_at and retain previous exit behavior.

Authorization, MFA, rate limits, five-second deadlines, no-store, body bounds, cursor binding and read-only storage behavior remain unchanged. Status cursors still bind the retained acceptance identity: a stale cursor remains a validation error, and a fresh guarded first page can identify a replacement. No additional state, SQL query, migration, fanout, replay or retention refresh is introduced.

This guard compares a saved timestamp, not a permanent unique acceptance token. It cannot distinguish hypothetical acceptances with identical timestamps, recover pruned original evidence, or establish continuity when the caller has no saved receipt. Missing receipts remain uncertainty. No tests are added or run and spec-compliance registries remain unchanged under the user's standing instruction.

## Consequences

Callers with a saved acceptance timestamp can distinguish current retained evidence from a replacement acceptance while independently checking content. Reconciliation remains safe to repeat and does not submit replacement events or silently reinterpret a newer acceptance as the expected one.
