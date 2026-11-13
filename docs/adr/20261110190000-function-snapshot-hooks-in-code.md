# ADR-20261110190000: Snapshot hooks in function code

Date: 2026-11-10
Status: Accepted (owner decisions, 2026-11-10)
Builds on: ADR-005 (cold boot always works), the `lifecycle:` hooks in
[cold-wake.md](../cold-wake.md) (`before_checkpoint`, `after_restore`),
ADR-20261106105015 (SDK lifecycle helpers), ADR-20261107193000 (config in
code), ADR-1342 (concurrent function workers)

## Context

A Node or Python function is loaded once and captured warm in the init
snapshot, so module-scope code already runs before the snapshot. What a
function cannot do well today is react to the snapshot itself:

- Restore and checkpoint logic runs only through `lifecycle:` hooks in
  `gregale.yaml`: a loopback HTTP path the function must route and guard
  itself (`ctx.lifecycle_hook`). A function has no router, so this is
  boilerplate in every handler.
- In production a hook request reaches **one** worker. A function with
  several workers per VM (ADR-1342) cannot close or reopen the pools of the
  others (documented in functions.md). The runner can already visit every
  worker (`serveFunctionLifecycle`), but only in the acceptance-only
  qualified build.
- Async setup at module scope (loading a model, priming a cache) has no
  defined completion point: the snapshot may be captured while it is still
  running, and its failure surfaces as a crash instead of a deploy error.

## Decision (owner decisions in bold)

1. **A function declares hooks as methods on its export.** Node:
   `export default { fetch, init, beforeSnapshot, afterRestore }`; a handler
   written as `export async function handler` uses named exports with the
   same names. Python: module functions `init()`, `before_snapshot()` and
   `after_restore()`, sync or async. Each receives a context with an abort
   signal and its deadline. No path is configured and no request is routed.

2. **All three hooks ship together.**
   - `init` runs in each worker after the module loads and before the worker
     reports ready, on cold boot and before the init snapshot. Readiness, and
     so the snapshot, waits for it. It is bounded by the function's startup
     deadline; a failure fails the deployment with a new code,
     `function_init_failed`, carrying the error (vmmd reads the adapter's
     `gregale: function init failed:` line in the startup output). An `init`
     that never finishes fails as `app_startup_timeout`, like any workload
     that never becomes ready.
   - `beforeSnapshot` runs before a new terminal init snapshot is captured.
     It has the `before_checkpoint` contract: 500 ms by default, at most
     2 s; a failure on the first capture fails the deployment with
     `before_checkpoint_failed`, a failure on a later park publishes no
     snapshot; a function that declares it does not get warm-tier captures.
   - `afterRestore` runs after a restore and when a frozen instance resumes,
     after guest-init has repaired the clock and entropy, before the
     instance takes traffic. It has the `after_restore` contract: 500 ms by
     default, at most 2 s.

3. **Hooks run in every worker.** The runner visits every live worker for
   `beforeSnapshot` and `afterRestore` and answers guest-init only when all
   of them finished. This lifts the existing every-worker lifecycle path out
   of the qualified build into the ordinary runner; it does not need the
   qualified execution lease. A worker started after a restore is a fresh
   process and runs `init`, not `afterRestore`.

4. **A failed or timed-out `afterRestore` means a cold boot.** The restored
   instance never takes traffic: guest-init rejects it as it rejects a failed
   `after_restore` today, and the wake falls back to a fresh cold boot
   (ADR-005). The waiting request is held, not failed. The hook's error is
   in `gregale logs`, and the wake timeline names the closed reason.

5. **The platform knows which functions declare hooks.** `beforeSnapshot`
   disables warm-tier captures, so hooks cannot be on for every function.
   The deploy reads the declared hook names from the handler with the same
   static reader as config in code (no code runs), and sets the function
   app's lifecycle to the runner's reserved loopback hook path with the
   documented default timeouts. `gregale deploy --plan` lists them. The
   adapter also reports its hooks in its ready line; when the two disagree
   (a hook built dynamically) the runner logs it, and a hook the deploy did
   not see is not called.

6. **One source for hooks.** A function that declares hooks in code and
   also sets `lifecycle:` paths for the same hook is refused at deploy,
   naming both. Apps keep `lifecycle:` paths unchanged; the SDK helpers of
   ADR-20261106105015 keep working for apps and for functions that do not
   declare hooks.

7. **Scope.** Node and Python functions. Go and `binary` functions restart
   after a restore and keep the Go SDK's hook middleware. Timeouts are set
   per function with `before_snapshot_timeout_ms` and
   `after_restore_timeout_ms` (gregale.yaml entry or config in code), within
   the same 2-second limit.

## Slices

1. Adapters call the three hooks (Node and Python), report them in the ready
   line (`function_hooks_version: 1`), and run `init` before ready; the
   ordinary runner answers guest-init on reserved paths
   (`/__gregale/function/before-snapshot`, `/__gregale/function/after-restore`)
   by broadcasting the hook to every worker, exclusive or shared, and refuses
   when a worker's adapter does not advertise hooks. Adapter, worker-pool and
   runner tests.
2. Static hook detection, the lifecycle wiring to the reserved paths and the
   one-source check at deploy (CLI and repository pushes share
   `pkg/gregalemanifest`), `function_init_failed`, `--plan` output, and a
   metal test that restores one snapshot into two instances with two workers
   each (guest-init calls the reserved paths only once this slice wires
   them).
3. Docs (functions.md, handler-api.md, cold-wake.md), TypeScript and Python
   types in the SDKs, `init` under `gregale dev --local`, and a journey.

## Consequences

- A function that keeps a database pool declares `beforeSnapshot` to close
  it and `afterRestore` to reopen it, in every worker, with no routing code.
- The snapshot is never captured with async `init` work half done, and an
  `init` failure is a named deploy failure instead of a crash loop.
- `afterRestore` adds to wake latency within the existing 2-second cap; the
  docs keep advising closing idle connections over full reconnects.
- A function declaring `beforeSnapshot` loses warm-tier captures, as an app
  with `before_checkpoint` does today.

## Alternatives considered

- **Named exports only**: does not fit the `export default { fetch }` form
  most Node functions use.
- **SDK helper only** (`defineFunction`): typed, but makes the SDK a
  requirement for a platform feature; types can come from the SDK while the
  runtime contract stays plain exports.
- **One worker per VM**: today's behaviour; leaves other workers' pools
  stale after a restore.
- **Fail the request or serve anyway on a failed `afterRestore`**: the
  first turns a broken hook into errors on every wake, the second serves
  with stale connections and duplicated state. Cold boot is the existing
  `after_restore` contract.
- **Turn hooks on for every Node/Python function**: no detection needed, but
  `before_checkpoint` disables warm-tier captures for every function.
