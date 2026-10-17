# ADR-1342: Per-function settings in gregale.yaml and concurrent requests per function worker

- **Status:** accepted
- **Date:** 2026-10-14
- **Related:** ADR-985 (multi-function projects), ADR-091 (edge rules,
  `kind=route`), ADR-687 / GHSA-24j2-p895-mwc9 (worker recycle on restore),
  ADR-999 (streamed responses), ADR-1150 (request timeouts), spec §4.9,
  §4.9.1, issue #559 (per-VM concurrency bound)

## Context

A `functions:` manifest (ADR-985) deploys each function as its own app, but
only a function's idle timeout, cron triggers, bucket events and inbound
webhooks were declarative. Its RAM, request timeout, minimum instances, env
and secrets had to be set with `gregale app`, `gregale env` and
`gregale secrets` after the first deploy, and a function set had no single
address: each function answered on its own host.

The generated Node and Python adapters serve one request per interpreter
process, with at most four processes per VM
(`api.FunctionInterpreterMaxWorkers`). The gateway admits up to the plan's
`concurrency_per_vm` (Pro 25, Scale 80) requests to a VM, so an I/O-bound
handler that spends its time waiting on a database or an HTTP call ran four
requests at a time while the rest queued in the runner. More processes
would cost about 40–60 MB each against plan RAM of 128–1024 MB. Node's event
loop and Python's asyncio can wait on many requests in one process.

## Decision

1. **Per-function settings.** A `functions:` entry may set `ram_mb`,
   `request_timeout_s`, `min_instances`, `concurrency`, `env` and
   `secrets_from_env`. An omitted key leaves the app's setting alone.
   `gregale deploy` applies the settings that shape the build or the first
   wake (RAM, request timeout, concurrency, env, secrets) before the
   function's deployment is built, reserving a new function's app first
   with `--create-only`; `min_instances` is applied after the deploy, with
   the idle timeout. Env values are upserted and keys the manifest does not
   list are kept, unless the function sets `prune_env: true`: then `env:`
   is its whole default-scope env and the deploy removes every other key.
   Pruning is opt-in because env rows carry no owner, and environment sync
   and `gregale env set` write the same rows. `secrets_from_env` names secrets whose values are read
   from the deployer's environment and sealed on every deploy; a name unset
   there keeps the app's existing secret, and a secret the app has never had
   fails that function before it deploys. No secret value is in the
   manifest.

2. **One address for a function set.** One function may set
   `entry: true`; every other function may list `routes:`, request path
   globs such as `/users/*`. After every function is deployed, the CLI gives
   the entry app's platform host and each of its verified custom domains one
   `kind=route` edge rule (ADR-091) per route, targeting the declaring
   function. `kind=route` runs before the host lookup, so a routed request
   reaches its function without an extra hop. Managed rules are named
   `functions:<function>` and sit at priority 500 and up, most specific
   route first, after rules added by hand at the default priority 100. A
   managed rule the manifest no longer declares is deleted from whichever
   function app holds it; any other rule is never touched. The entry
   function serves every path no route claims.

3. **`function_concurrency`.** A new app setting (API `function_concurrency`,
   `gregale app <slug> --function-concurrency N`, manifest `concurrency`)
   says how many requests one Node or Python function worker serves at once.
   Zero and one keep today's behaviour. It is bounded by the plan's
   `concurrency_per_vm`, so the gateway's per-VM admission remains the only
   request limit. imaged stamps `FAAS_FUNCTION_CONCURRENCY` on a function
   deployment whose setting is above one; the value is fixed at build, so a
   change takes effect on the next deploy.

4. **Protocol.** The persistent adapter protocol is extended compatibly. A
   worker's ready line may carry `"concurrency": K`; without it K is 1 and
   the exclusive protocol is unchanged. With K above 1, the runner prefixes
   each request object with `"__faas_id"`, the adapter writes that id as the
   first field of every response line and frame, and the runner cancels an
   abandoned request with `{"__faas_cancel": id}`, which the adapter always
   acknowledges with `{"__faas_cancelled": id}`. Go SDK handlers, custom
   adapters and app layers built before this change never advertise
   concurrency and keep one request per process. A `binary` function's own
   executable (ADR-987) may implement the shared protocol; the metal
   acceptance does exactly that.

5. **Shared workers.** The runner's worker pool sends a request to the
   least busy current worker with a free slot and starts another worker
   only when every slot is busy, still at most four workers; while a shared
   worker starts, other requests wait for its slots instead of starting
   more interpreters. A reader goroutine routes each response line to its
   request; a request whose reader falls more than 16 MiB behind fails and
   is cancelled. A request the runner abandons (client gone, deadline) is
   cancelled, not its worker. A worker that does not acknowledge a cancel
   within 5 s has exceeded its cleanup budget and is restarted; a worker that
   exits fails only its in-flight requests. Recycle on snapshot restore
   (ADR-687) closes idle shared workers at once and busy ones when their
   last request leaves.

6. **Runtimes.** Node multiplexes any handler. Python runs an `async def`
   handler as one asyncio task per request, with real cancellation, and a
   sync handler on a thread pool sized to the slots. A thread cannot be
   interrupted, so a cancelled sync request's output is dropped, a generator
   body stops at its next piece, and the cancel is acknowledged only when
   the handler returns: the runner never counts a slot a running handler
   still holds, and a handler stuck past the 5 s acknowledgement deadline
   costs its worker, as every cancel did before this ADR. Threads share the
   interpreter lock, so only handlers that wait gain.
   The setting is accepted on every function runtime: a handler that does
   not advertise slots keeps one request per process.

7. **Queue and event delivery per function.** A `functions:` entry may list
   `queue_bindings` and `event_triggers`; cron triggers stay in the
   top-level `triggers:` with `app:` naming the function. Function deploys
   pass `--no-triggers`, so after each function deploys the CLI applies
   them: queue bindings through the queue binding API, matched by name with
   push/http defaults (an omitted key leaves the app's bindings alone, an
   empty list removes them), and event triggers through
   `POST /v1/triggers:batch_create`, which now applies a manifest's
   `event_triggers` for the target app with the same code a source deploy
   uses and rolls back on failure. Event subscriptions are only added or
   updated, never deleted, as on any deploy.

## Consequences

- An I/O-bound function serves up to its plan's per-VM bound from one
  interpreter, in one process's memory instead of four.
- Opting in means a handler's module state is shared by concurrent
  requests. Handlers must not keep per-request data in globals; this is why
  the default stays one.
- [ADR-1540](1540-function-cancellation-cleanup-and-lifecycle-context.md)
  completes cancellation: Node gets an abort signal and Python cancellation
  waits for cleanup. The shared slot stays occupied until acknowledgement;
  an uncooperative handler loses its worker after five seconds.
- A streamed response to a slow client is bounded by the 16 MiB per-request
  queue instead of pipe backpressure.
- Billing is unchanged: plan RAM + 8 MB per running second.
- Snapshots capture the prewarmed shared worker; restore still replaces it
  before traffic (GHSA-24j2-p895-mwc9).
- A `functions:` deploy makes extra API calls only for functions that set
  something, plus one edge-rule listing per function app to reconcile
  routes.

## Alternatives

- **More interpreter processes per VM.** Each costs tens of MB of plan RAM,
  and an I/O-bound handler does not need more CPU.
- **Node `worker_threads`.** A thread per request still costs an isolate's
  memory and gains nothing for waiting on I/O.
- **Passing the concurrency at wake time.** That needs a vmmd and guest-init
  path, and the snapshot already holds a worker started with the build's
  setting.
- **A gateway-native function-set object for routing.** That adds a table
  and an owner; `kind=route` edge rules already route before the host
  lookup.
- **Concurrency by default.** Handlers written for one request at a time
  could share per-request state across requests.
