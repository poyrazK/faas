# ADR: Node and Python stored acknowledgment policy parity

- Status: Accepted
- Date: 2026-10-17

## Context

Go can bind immutable version-1 retry settings to saved callbacks. Node/Python
currently ignore bindings, preventing mixed-language recovery for bound scopes.
Use the same columns, validation, atomic staging and recovery precedence across
all SDKs without another database upgrade.

## Decision

Node durable/receipt-only/negative staging accepts copied `retryPolicy` options;
prepared options carry the same field. Python exposes a copied `retry_policy`
keyword on these paths, including prepared completion. Forward snapshots through
the existing pre-commit hooks, including receipt replay, completed-lease
reconciliation and final processing-fenced completion. Immediate modes stay as
before. Bind only new intents; differing bindings and existing unbound duplicates
conflict. Omitted policy preserves existing bindings.

Lock the latest row during duplicate comparison. Read the binding with the saved
callback under the recovery claim lock and validate before claim COMMIT. All
three null values mean unbound; unknown/partial/malformed values fail closed.
Node decodes milliseconds from canonical decimal text; Python requires native
integers. Saved settings override validated fallback through settlement. Preserve
Node's safe-integer generation rejection and Python's full int64 generations.
Add frozen payload-free policy metadata to intent diagnostics.

Package/export identical one-time upgrade SQL from Go through the shared schema
generator. Register Python's SQL in regeneration and wheel/sdist metadata.
Current staging/listing/recovery requires upgraded columns, even for unbound rows.
Upgrade every host before enabling bound staging.

## Consequences

Updated Go, Node and Python workers can share bound scopes. Older workers ignore
policies and must be excluded before enabling them. The existing CHECK validates
values rather than compatibility. Adoption, updates, exhaustion and durable
Retry-After remain separate work. Receipt/callback/platform/claim fences,
cancellation, renewal and connection cleanup retain their contracts. Local
status proves neither remote confirmation nor ownership. See the
[shared contract](../event-acknowledgment-stored-retry-policy.md).

## Validation

Node generation and build/declarations passed. Isolated Python generation without
tests produced 6515 byte-identical source/schema files; Python 3.11 syntax and
Ruff passed. Node package and Python wheel/sdist source/schema/export inclusion
matched built/current bytes. SQLC statically compiled 13 statements per SDK
against both fresh and upgraded schemas, without a database. Shared upgrade
bytes, callback/settlement/lifecycle source comparisons, ADR and docs checks
passed. The Go embedded-schema/example build also passed. No tests/assertions
were added or changed and no local tests or SDK helper smoke checks were run.
Hosted mixed-SDK replay, upgrade, conflicts, precedence, cancellation and
uncertain-commit qualification remains pending.
