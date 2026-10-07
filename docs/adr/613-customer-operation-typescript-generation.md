# ADR-613: Generate TypeScript types for customer Operations

## Status

Implemented locally — 2026-10-07. Operations admission remains closed.

## Context

Customer Operations definitions already carry validated JSON Schemas for their
input and result. The browser-safe feature controller and session expose
separate TypeScript generics, but starter code still has to hand-maintain those
types or use `unknown`. Hand-maintained copies can drift from the selected
manifest and schemas.

## Decision

Add `gregale customer-operations types --app <slug> --plan <plan>` as an
offline source command. It reuses the manifest parser, plan checks and schema
compiler used by `validate`, then emits `<PascalCaseName>Input` and
`<PascalCaseName>Output` declarations to
`customer-operations.generated.d.ts` by default. `--name` selects one
operation and `--output` selects another source-local TypeScript declaration
path. The command refuses to replace a file without the generator marker and
publishes generated content through a temporary file and rename.

`--check` compares the expected declarations with an existing marked file
without writing. It reports `current`, `missing` or `stale`, and exits nonzero
for missing or stale output so CI can enforce regeneration after schema changes.

The generator projects JSON Schema types into TypeScript shapes. Constructs it
cannot represent precisely remain `unknown` or use a conservative shape. It
does not claim to encode every validation keyword; Gregale's server-side
schemas remain authoritative at submission and completion. Local JSON Pointer
references are emitted as aliases, while local anchors project to `unknown`.

HTTP, Job and Workflow starters reference the generated input/output names in
JSDoc and apply them to `CustomerOperationFeature<TInput, TOutput>`. This types
session submissions and operation results without changing browser runtime
behavior. Each starter also has a focused `tsc --noEmit` configuration and a
`typecheck` script that checks its browser app and generated declarations against
the packed SDK types. Relative browser imports resolve to the same allowlisted
URLs at runtime; the `/sdk/operations.js` route has an adjacent declaration that
re-exports the SDK's published type entry point for the checker.

## Consequences

Developers can regenerate compile-time types from the same manifest contracts
used by validation and deployment, detect stale declarations with `--check`,
and type-check starter usage with `npm run typecheck`. The CLI remains offline and credential-free.
Generated declarations are source artifacts and must be regenerated when the
manifest or schemas change. They do not replace runtime validation or alter
admission, persistence, execution, recovery or delivery behavior.
