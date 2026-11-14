# ADR-20261105161206: Durable MCP Task output contracts

## Status

Accepted; extends ADR-426 and the MCP starter Task schema and catalog decisions.

## Context

Node's SDK applied a tool's final output schema to an accepted Task handle,
returning an error after the Task was already persisted. Node also admitted
arguments before asynchronous task validators settled. All three starters
validated synchronous tool output but let durable workers mark schema-invalid
output completed. Dedicated workers and retained handler versions cannot recover
the original advertised contract from the current web process's catalog.

## Decision

1. Add a nullable `output_schema jsonb` column to the starter-owned Task table,
   constrained to JSON objects. The producer snapshots and compiles the
   advertised output schema before admission and persists it atomically with
   the Task. This is reviewed catalog metadata, stored in plaintext like tool
   names and handler versions. Existing encryption for arguments, results,
   errors and client input remains unchanged. Do not put secrets or caller
   values in catalog schemas.
2. All workers validate final successful `structuredContent` against that saved
   contract before committing completion. Support local references without
   fetching external schemas, coercing values or inserting defaults. Invalid
   results fail terminally with the existing generic encrypted error and no
   stored successful result. Business errors marked `isError: true` may omit
   structured output. Retry and input-pause transitions preserve the contract,
   including when another worker or retained handler version resumes the Task.
3. Node awaits asynchronous admission validators and rechecks shutdown state
   before persistence. A modern task-capable tool call registers its SDK tool
   without a final output schema for that request only, using request-local
   context. Its accepted Task handle is not a final tool result. Discovery and
   synchronous calls retain the advertised schema and SDK output validation.
4. Apply the nullable expansion through each explicit starter migration, with
   a five-second transaction-local lock timeout. Both version-1 migration paths
   remain idempotent and startup checks require the new column without DDL.
   Existing Tasks with SQL NULL retain their prior behavior; no current catalog
   is retroactively applied. Migrate before upgrading runtimes, then upgrade
   and drain all older workers before producers rely on the new check. An
   older worker ignores the metadata during release overlap.
5. Python recognizes empty `form` and `url` capability objects as supported,
   preserving Task input pauses for clients advertising those standard values.

No platform migration, public MCP method or plan limit changes are introduced.

## Validation

HTTP and real PostgreSQL regressions cover Node handle delivery, asynchronous
validator rejection without persistence, discovery and synchronous validation,
and retained workers after catalog replacement. PostgreSQL regressions in all
three starters cover invalid final output, valid and business-error results,
local references, schema snapshot isolation, retry and input resume, and explicit
version-1 migration with legacy NULL contracts. Scaffolding tests require every
new runtime and regression module in generated projects.
