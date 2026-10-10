# ADR-674: Shared customer Operations feature controller

## Status

Implemented locally — 2026-10-07. Operations admission remains closed.

## Context

The HTTP, Job and Workflow customer starters share credential resolution and
identity notifications, but each app still repeats client and session creation,
history loading, retained-submission resume, stale-connect checks and teardown.
Those are lifecycle mechanics common to the customer-facing feature. Execution
types and their recovery contracts remain different, and each UI still needs
control over rendering and explicit business actions.

## Decision

Add a framework-neutral `CustomerOperationFeature<TInput, TOutput>` controller
to the browser-safe Node SDK. It composes the host auth provider, customer
Operations client, receipt store and `GregaleOperationSession<TOutput, TInput>`.
`TInput` types calls to `start()` and `TOutput` types operation results and
updates. Both default to `unknown` for untyped callers. `connect()` resolves a
current credential before creating the client, loads history, resumes the local
submission receipt and returns the active session with the resume result.
Connection epochs fence overlapping connects and identity changes; stale
connections return no session. `close()` invalidates and closes the active
session while retaining the host identity subscription. `dispose()` also
unsubscribes from the host application.

The controller emits session updates and calls an identity-change callback only
after closing the old session. Rendering, operation-specific controls, and
explicit calls such as `start`, `cancel`, and `download` remain in the app. The
HTTP, Job and Workflow starters use the same controller, while keeping their
execution and recovery semantics intact. The lower-level `CustomerOperationAuth`
helper remains available for applications that need a custom lifecycle.

## Consequences

Starter integrations share one lifecycle contract and no longer need to
coordinate session epochs or auth subscriptions themselves. The controller
does not persist credentials or operation inputs, create application identity,
or change platform APIs, storage, execution or admission behavior. Hosts still
own token issuance and must report customer changes. TypeScript input types are
compile-time guidance; the server's operation input schema remains the runtime
contract. Operations admission stays closed pending the existing qualification
gates.
