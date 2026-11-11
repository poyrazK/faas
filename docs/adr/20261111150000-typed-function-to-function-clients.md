# ADR-20261111150000: Typed function-to-function clients

Date: 2026-11-11
Status: Accepted (owner decisions, 2026-11-11)
Builds on: ADR-985 (multi-function projects), ADR-1342 (function settings),
ADR-20261107193000 (config in code), ADR-20261108160000 (functions projects
from repository pushes), ADR-271 and ADR-20261022160248 (`.internal`
aliases), the service bindings in [networking.md](../networking.md), and
async invocations in [event-driven.md](../event-driven.md)

## Context

A functions project deploys each function as its own app (`<project>-<name>`).
When one function needs another today, its author has to:

- find the callee's app slug and address, either the public URL with an API
  key from a secret, or a private `<slug>.svc.gregale:10081` call that works
  only because the standalone binding policy defaults to `account`;
- use `createGregaleFetch` (Node) or the SDK transports (Python) by hand so
  the deadline and release travel with the call;
- serialize and parse JSON by hand, with no type for what the callee takes
  or returns; a renamed field is found in production;
- for work that needs no answer now, set up a queue or call the async
  invoke API, which needs an API key inside the guest.

The platform already has what a good client needs: private calls authorized
by the calling VM's identity, which wake a parked callee and carry the
deadline; `.internal` names for declared bindings; and durable async
invocations with retries and dead letters. A function project just does not
put them together.

## Decision

Owner decisions on 2026-11-11 (all were the recommended options).

1. **Types come from a schema in config.** A function entry in
   `gregale.yaml`, or its config in code, can declare `input` and `output`
   as JSON Schema (inline, or a path to a `.json` file in the function's
   root). The CLI generates, for every function in the project:
   - Node: a `gregale-functions.d.ts` declaring each function's input and
     output types and a typed `functions` client;
   - Python: a `gregale_functions.py` with `TypedDict`s and a typed client.
   `gregale functions types` writes them; `gregale deploy` and
   `gregale dev --local` regenerate them when a schema changed. A function
   without a schema gets `unknown` / `Any` types and still gets a client.
   The callee checks its input against its own schema before the handler
   runs and answers 422 `function_input_invalid` naming the failing path, so
   a caller built from an older schema fails loudly at the boundary.

2. **Transport is an automatic private binding.** At deploy every function
   in a functions project gets service bindings to each of its siblings
   (`service_binding_targets`, `declared` policy), so `<slug>.internal`
   resolves for exactly those apps. The SDK client calls the callee's
   `http://<slug>.internal:10081` through `createGregaleFetch` / the Python
   release transport: never public, authorized by the platform from the
   caller's VM, waking a parked callee, and carrying the remaining deadline,
   release and chain deadline. The deploy also sets `GREGALE_FUNCTIONS`, a
   JSON map from function name to app slug, so the runtime client needs no
   code generation to find a sibling. A rename or removal updates the
   bindings and the map on the next deploy.

3. **Two call styles.**
   - `await functions.users.call(input)` (Python `await functions.users.call(...)`
     or `functions.users.call_sync(...)`) sends a POST with the JSON input
     and returns the parsed output; a non-2xx answer raises a typed
     `FunctionCallError` carrying the RFC 7807 problem.
   - `await functions.users.send(input)` enqueues a durable async invocation
     of the callee and returns its invocation id at once. It goes over the
     same private binding with `X-Gregale-Invoke: async`; the service proxy
     admits it into the existing async invocation path (retries, dead
     letters, `gregale invocation get`), authorized as the caller's VM, so no
     API key is needed in the guest. Without the header the proxy behaves as
     today.

4. **Scope is the project.** A function reaches its siblings in the same
   functions project, and in the same environment as the caller (the
   existing binding rules: a staging caller reaches staging). Other apps of
   the account are reachable only when the entry lists them under `calls:`
   by app slug; the deploy adds them to the bindings and to
   `GREGALE_FUNCTIONS`. Cross-account calls stay refused by the proxy.

5. **Languages.** Node and Python functions get the client and generated
   types. Go functions keep calling `<slug>.internal` with the Go SDK
   transport until the Go SDK is published; the bindings and
   `GREGALE_FUNCTIONS` apply to them too.

## Slices

1. Manifest and deploy: `input`, `output` and `calls` in a function entry
   and config in code (validated: JSON Schema draft 2020-12 subset, files
   inside the function root, size cap); the deploy sets sibling and `calls`
   bindings with the `declared` policy and `GREGALE_FUNCTIONS` on each
   function app (CLI and repository pushes share `pkg/gregalemanifest`);
   `deploy --plan` lists each function's callees.
2. Runtime clients: `functions` in `@gregale/sdk-node` and `faas_sdk`
   (call over the binding, `FunctionCallError`), and the callee-side input
   check in the Node and Python adapters (`function_input_invalid`).
3. Generated types: `gregale functions types`, regeneration in `deploy` and
   `dev --local`; `dev --local` serves siblings through the same client.
4. `send()`: the service proxy's async admission for function targets
   (`X-Gregale-Invoke: async`), reusing the async route enqueue path.
5. Docs (functions.md "Call another function", handler-api.md), a journey,
   and a metal or native test of a call and a send between two parked
   functions.

## Consequences

- Calling a sibling is one typed line, private by default, and its deadline
  and release propagate without the author knowing they exist.
- A schema change shows up as a type error in the caller at
  `gregale functions types`, and as a 422 at the boundary if an old caller
  is still deployed.
- Every function in a project now has a `declared` binding policy and
  explicit targets instead of the `account` default, which narrows what it
  can call to its project plus `calls:`. A function that relied on calling
  an unrelated app by `.svc.gregale` must list it under `calls:`; the deploy
  plan names that change the first time.
- `GREGALE_FUNCTIONS` is a reserved env name for function apps.

## Alternatives considered

- **The caller annotates the types** (`call<In, Out>()`): smallest, but the
  types drift from the callee with nothing to catch it.
- **Infer types from handler code**: fragile across JS and Python, and
  config in code already chose a static, literal-only reader.
- **Public URL plus API key**: exposes the callee and puts a key in every
  caller.
- **Whole-account scope by default**: a wider blast radius, and policies
  become hard to reason about; `calls:` keeps it opt-in.
