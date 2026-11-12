# ADR-20261112160000: Results of sent function calls, and Go typed clients

Date: 2026-11-12
Status: Accepted (owner decisions, 2026-11-12)
Builds on: ADR-20261111150000 (typed function-to-function clients),
ADR-20261021090000 (the guest platform API at `api.svc.gregale`), and async
invocations in [event-driven.md](../event-driven.md)

## Context

`send()` (ADR-20261111150000 §3) enqueues a durable async invocation of a
sibling and returns its id. The invocation's state, result and error are
stored, but a function cannot read them: `GET /v1/invocations/{id}` needs an
API key, and a guest has none. So fan-out works and fan-in does not.

Go functions get the bindings and `GREGALE_FUNCTIONS`, but no typed client,
and a Go callee does not check its input schema the way Node and Python
callees do.

## Decision

Owner decisions, 2026-11-12: both result paths, results readable only by the
sender, a generic Go client with generated structs, and the Go runner checks
input.

1. **The sender is recorded.** The service proxy's send admitter stores the
   calling app on the invocation (`invocations.sent_by_app_id`, an expand-only
   column). The caller's identity comes from node-local instance identity,
   never from a guest header. Invocations created any other way have none.

2. **`wait()` and `result()`: a VM-authorized read.** The guest-facing
   service proxy serves `GET http://api.svc.gregale:10081/v1/invocations/{id}`
   next to the rehearsal platform API. The caller is resolved from its
   address, as for every service call, and may read only invocations whose
   `sent_by_app_id` is its own app; anything else is 404, so ids are not an
   oracle. The answer is the invocation's id, state, outcome, attempts,
   result and error. `?wait=<seconds>` (at most 30) holds the request until
   the invocation is terminal or the time passes, so the SDK long-polls
   instead of hammering the store. The SDKs add
   `invocation.wait({ timeoutMs })` (resolves with the result, raises
   `FunctionCallError` on a failed invocation) and `invocation.status()`.

3. **Completion callback.** `send(input, { onComplete: "<name>" })` adds
   `X-Gregale-On-Complete: <name>`. The admitter resolves the name against the
   caller's own `function_callables` (or `self`) and stores the target app on
   the invocation (`invocations.on_complete_app_id`); an unknown name is 400.
   When the invocation reaches a terminal state, the same transaction that
   records it enqueues an async invocation of the target app, `POST /` with
   `{invocation_id, function, status, outcome, result | error, attempts}` and
   `X-Gregale-Invocation-Completion: <id>`, exactly as the job-finished
   destination outbox does today. A completion invocation never schedules a
   completion of its own. The target keeps its own retries and dead letters.

4. **Go typed clients.** gregale-go gains a `functions` package:
   `functions.Call[In, Out](ctx, name, in, opts...)`, `functions.Send(ctx,
   name, in, opts...)` returning an `Invocation` with `Wait` and `Status`,
   over the SDK's release transport and the same wire contract as Node and
   Python. `gregale function types` writes `gregale_functions.go` into a Go
   function's root: input and output structs from the schemas and typed
   wrappers (`functions.Users(ctx, in)`), package-named after the handler's
   package.

5. **Go callees check input.** The Go function runner (`go124`) checks
   requests marked `X-Gregale-Function-Call: 1` against
   `FAAS_FUNCTION_INPUT_SCHEMA` with the same JSON Schema subset and the same
   422 `function_input_invalid` problem as the Node and Python adapters.

## Slices

1. Sender and completion columns, the admitter recording both, the terminal
   transition enqueuing the completion invocation (Pg and Mem stores).
2. The guest read route with long-poll, and Node/Python `wait()`/`status()`
   and `onComplete`.
3. Go: the runner's input check, the `functions` package, Go output from
   `gregale function types`.
4. Docs (functions.md) and tests at each layer.

## Consequences

- Fan-in is a few lines: send several, then wait on each, or hand the
  results to a completion function.
- Two new nullable invocation columns; no backfill, and old rows read as
  "no sender, no completion".
- A completion is ordinary async work billed to the target as any
  invocation; it cannot loop because completions schedule none.
- Every runtime now behaves the same as a callee.

## Alternatives considered

- **An API key in the guest** for `GET /v1/invocations`: puts a credential in
  every function and reads every invocation of the account.
- **Any app of the project may read** an invocation: wider than send's own
  authorization; the owner chose sender-only.
- **Go client without generated types:** smaller, but no compile-time check
  against the callee's schema.
