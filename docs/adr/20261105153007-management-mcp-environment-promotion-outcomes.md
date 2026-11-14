# ADR-20261105153007: Exact environment promotion acceptance recovery

## Status

Accepted — 2026-11-05. Extends ADR-20261105102920 and ADR-20261022192200.

## Context

A project-level promotion may commit while its response is lost. Its original
plan eventually expires, and its native promotion ID was assigned after POST.
A recent promotion or a reused environment slug cannot prove that original
admission. Rollback outcome lookup does not cover promotion creation.

## Decision

An optional paired Environment-Promotion-Attempt-ID and
Environment-Promotion-Request-Hash bind original admission. The fingerprint is
SHA-256 over compact UTF-8 JSON of ["environment_promotion", project UUID,
source environment UUID, target environment UUID, reviewed promotion hash],
with canonical lowercase hyphenated UUIDs and lowercase hexadecimal output.
The native ID is intentionally absent until admission. The bridge stores this
original reference before POST, atomically with history/replay state, in an
independent encrypted environment-promotion-outcome-lookups namespace.
Older lookup formats remain unchanged. Already-attempted legacy plans cannot
acquire identities or receipts retroactively.

Native promotion creation, workloads, approval consumption and its immutable
receipt share the same transaction/lock. Modern accepted attempts cannot admit
or dispatch work again, including after a later failure. Account/attempt
serialization and uniqueness prevent concurrent duplicates. Different bindings
conflict. Explicit legacy original-key continuation retains its existing guarded
checkpoint contract without creating/changing receipts. Previously storable keys
retain their spelling; overlength company prefixes use a domain-tagged digest.
Admission still enforces the existing release, qualification, configuration,
protection, MFA, quota and approval guards.

A scoped personal/company read at environments/{environment}/promotion-attempts/
{attempt_id} returns minimal original acceptance, including the native ID.
Receipts are bounded to one per retained promotion and cascade with their
account/project/promotion. Historical source/target IDs survive environment
deletion; current reads refuse replacement identities. No credential, approval,
retry key, workload payload or configuration is returned.

The existing gregale_get_environment_action_outcome tool also covers promotions.
It verifies the original receipt and returns only the native promotion reference
and an explicit gregale_get_environment_promotion observation. Native completion,
workload handoff and health require separate observations. Missing/mismatched
evidence stays unresolved. Accepted local history must agree with the native ID.

History, resources and prompts expose read-only recovery after restart/expiry.
An unknown promotion recipe cannot invent its observation ID; the receipt lookup
supplies it. Passive discovery never fetches receipts. Lookup preserves recorded
uncertainty and original expiry; it cannot replan, replay, approve or renew write
authority. Current account/project/source/target/workload identities, consent,
storage, reference integrity and retention are rechecked before release.
Catalog, prompt and resource-template counts remain unchanged.

## Validation

Cover Mem/Pg concurrent admission, receipt fault rollback, changed nonce/hash,
legacy non-adoption, owner isolation, replaced environments and terminal retries.
Exercise protected HTTP admission/lost replies, scoped typed SDK reads, populated
history and replay, both MCP protocols, encrypted restart/expiry, malformed
snapshots, final guards and revocation. Pinned Codex fixtures use explicit reads
with zero model requests and zero native writes; native/provider qualification
remains separate from portable fixtures.
