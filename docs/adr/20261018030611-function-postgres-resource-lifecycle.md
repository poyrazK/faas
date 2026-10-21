# ADR-20261018030611 · Automatic PostgreSQL ownership in Node functions

- **Status:** accepted implementation; native qualification pending; production off
- **Date:** 2026-10-17
- **Decision:** Extend ADR-20261018030610's qualified Node adapter with automatic registration
  of SDK PostgreSQL pools, all-interpreter checkpoint drainage and fresh restore
  probes. Keep activation restricted to `metal,function_background_native` builds.
- **Why:** Broadcasting a customer callback does not establish that every pool
  in every interpreter is registered, drained and rebuilt before readiness.

## Registration

The trusted execution runtime appends a versioned worker argument before dispatch.
The generated adapter installs its registry before importing the application.
Its ready handshake advertises resource lifecycle version 1 separately from
background completion. Qualified pools reject missing or unknown versions,
never fall back to an unmarked one-shot adapter, and cannot reuse an unqualified
pool identity. Old artifacts fail before handler dispatch and require rebuilding.
Automatic pool ownership also requires the updated SDK runtime or regenerated
bundled helper; direct `pg.Pool` instances and older helpers are not discovered.
Copies of `@gregale/sdk-node/postgres/runtime` and the generated starter helper
find the same versioned symbol, register before creating a driver pool and remove
registration only after explicit close has actually drained every owned pool.
A timed-out close retains ownership until drainage completes; a failed close
cannot produce a successful checkpoint. Driver/registry errors remain sanitized.

The registry accepts at most 16 pool owners per interpreter, defined by
`api.FunctionPostgresMaxPools`. This structural bound limits callback fan-out;
it grants no additional database connections or plan concurrency. Account for
all worker processes and resident VMs when sizing actual driver pools. Capacity
refusal occurs before the driver factory runs. Registration is closed while
checkpointing, checkpointed or restoring. The symbol coordinates code inside one
interpreter; it is not a security boundary against the application's own code.

Ordinary adapters receive no trusted argument and install no registry. The
standalone HTTP starter keeps its explicit hooks. Request bodies, forwarded
headers, application environment and manifest settings cannot turn on the
qualified runtime. This change adds no SQL replay, schema migration, entitlement
or billing change.

## Checkpoint and restore

Qualified imaged artifacts supply reserved before-checkpoint/after-restore paths
when the function has no custom callback. Those paths run platform lifecycle work
without calling the application handler. Explicit custom paths are preserved.
The trusted loopback lifecycle envelope selects this dispatch; an ordinary
request to the same URL cannot invoke the resource callback.

For checkpoint, a custom callback runs first so it can flush database work. Its
response stream and cleanup must finish before pool drainage starts. The adapter
then calls every registered pool's `beforeCheckpoint` and waits with
`Promise.allSettled`; one failed pool never hides unfinished neighbours. It emits
one successful internal response only after all callbacks succeed. The runner's
existing worker batch and host execution reservation remain owned through actual
cleanup or physical retirement. Lifecycle callbacks cannot register `waitUntil`.

For restore or in-place thaw, each registered pool first disposes old sessions,
uses its factory to create a fresh pool and passes its configured read-only probe.
Only then can the custom after-restore callback run or ordinary invocation resume.
The host forces the resume hook for a qualified frozen Node function even if its
caller omitted the optional flag. Admission remains governed by the existing
host boot/capture flight and runner cleanup fence.

The acceptance build supports terminal capture with the before-checkpoint flag.
ADR-20261018030612 adds continuing-VM recovery for qualified warm captures; migration
capture remains refused. Existing
artifacts lacking the automatic hook manifest must be rebuilt; a qualified host
does not infer successful drainage for an old artifact. Snapshot reuse still
establishes idle admission before teardown through ADR-20261018030610's freeze barrier.

The existing SDK budgets and query cancellation remain in force. A successful
pool close is not proof that the database did not commit a prior mutation.
Applications must use idempotency for uncertain COMMIT results; the platform
never retries SQL or promises durable execution after worker/VM replacement.

## Evidence and activation boundary

Portable tests use the generated adapter, actual HTTP runner, four interpreters
and eight pools. They hold pool ends and a custom response body to prove no early
acknowledgement, verify registration capacity before factory effects and exercise
fresh restore probes. A disposable TLS/SCRAM PostgreSQL test independently observes
eight sessions, blocked post-response writes, zero sessions while acknowledgement
is deliberately held, fresh restore sessions and no mutation replay after a real
interpreter exit. Its injected host transport does not qualify real vsock or KVM.

The strict native target also contains a deployed SQL function phase without
customer lifecycle endpoints. It requires all-interpreter drainage before
capture, real snapshot restore, in-place thaw after connection loss, and worker
and runner death. Its authenticated test-only control peer holds acknowledgement
while SQL/proxy observers verify closure, so VM destruction cannot masquerade as
pool drainage. The peer carries no SQL or database credentials.

Passing compilation or portable tests does not qualify that native phase. Require
the supported isolated native host, current artifacts and leak checks, plus the
remaining native lost/duplicate acknowledgements, watchdog, uncertain cleanup and
vmmd restart/recovery cases before changing activation. Node 24 native execution
and private environment qualification remain pending. No rollout is authorized
by this ADR. See the [acceptance procedure](../ops/function-backend-concurrency-acceptance.md)
and [evidence](../ops/evidence/20261017-function-postgres-lifecycle/REPORT.md).
