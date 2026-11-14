# ADR-20261105102920: Exact environment rollback acceptance recovery

## Status

Accepted — 2026-11-05. Extends ADR-20261022192200 and ADR-20261105082446.

## Context

Project-level environment rollback history retains an original promotion and a
stable retry key, but cannot prove acceptance of that exact attempt after its
plan expires. A promotion's later rollback status can describe another caller
or automatic recovery. Environment slugs can be reused after deletion.

## Decision

An optional Environment-Rollback-Attempt-ID and paired original request hash
bind admission to the promotion ID/hash and immutable project/source/target
environment IDs. The bridge persists these before its first POST. The native
store locks the promotion, validates the current scope and request binding, and
commits one immutable receipt atomically with rollback admission. The fingerprint
is SHA-256 over compact UTF-8 JSON of ["environment_rollback", project UUID,
source UUID, target UUID, promotion UUID, promotion hash], using canonical
lowercase hyphenated UUIDs and lowercase hexadecimal output. A previously
accepted attempt-bearing POST cannot admit work again, including after failure;
different bindings conflict. Explicit legacy retries retain the original-key
checkpoint continuation contract and cannot mint or modify an exact receipt.
This preserves recovery of classic synchronous rollbacks after interruption.
Existing legacy or automatic rollbacks cannot acquire a receipt retroactively.
Previously storable retry keys retain their exact spelling. Company prefixes
that exceed the ledger character limit use a deterministic domain-tagged digest;
these overlength keys could not previously commit in PostgreSQL.

Receipts live in an additive table bounded to one per retained promotion, and
are removed with that promotion/account/project. Source/target UUIDs remain
historical evidence after environment deletion; scoped reads refuse replacement
identities. A read-only personal/org project route returns minimal acceptance
metadata without credentials, idempotency keys, configuration or workloads.
Original release checks, target CAS, binding checks, protection and MFA remain
authoritative. Admission proves intent only, not restoration or native progress.

The bridge keeps a separate encrypted, grant-local lookup namespace atomically
with replay/history state. Existing history formats and recovery URI templates
are unchanged. The read-only gregale_get_environment_action_outcome tool verifies
the exact receipt after restart/plan expiry and returns a redacted acceptance
plus an explicit promotion/rollback observation call. Current source/target
consent, project/workload identities, storage, retention and the original
reference are checked again before release. Missing or mismatched evidence stays
unresolved. Legacy attempts never receive new attempt identities.

History, saved-plan prompts and resources expose recovery passively. Lookup does
not run restoration, fetch workload health, rewrite recorded uncertainty, renew
expiry, replan, approve or grant write authority. Rollback progress and each
workload's handoff/health remain independent explicit observations. Environment
promotion acceptance recovery remains separate work.

## Validation

Cover concurrent Mem/Pg admission, receipt-insertion rollback, terminal replay,
changed hashes/scopes, legacy non-adoption, owner isolation and replacement
identities. HTTP checks cover lost replies, malformed headers and read-only
receipt lookup. Both MCP protocols cover encrypted restart, expiry, passive
recipes, final consent/identity/storage/retention guards and namespace cleanup.
Typed SDK transports and pinned real Codex exercise acceptance independently of
native completion. Portable fixtures do not qualify provider deployment.
