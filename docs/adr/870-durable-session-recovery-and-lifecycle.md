# ADR-870: Durable session recovery and lifecycle

Status: Implemented and qualified by focused CI; native acceptance pending.

Provide an opt-in schema-version-2 session helper alongside the existing version-1
contract. Commit fresh-query status snapshots through serialized invocation.
Persist explicit monotonic application ticks for activity-based idle expiry and
closure retention. Reclaim only a fully closed, retained cohort; advance a durable
generation before reusing its bounded slots. Include generation in command IDs and
reject obsolete generations independently of replay receipt retention.

Expose application-only pure migration callbacks for later schema versions while
preserving lifecycle metadata. Built-in legacy migration preserves ordering but
requires generation-qualified new commands and a coordinated client upgrade.

Status creates a receipt and can be replayed stale; it is not read-only inspection.
Time advancement is an authenticated service operation, not guest wall-clock time
or an automatic alarm. Reclamation frees session slots, not platform receipts.
Single-session recycling, autonomous expiry and public end-user authorization
remain outside this contract.

See [protocol, migration and recovery guide](../durable-entity-session-lifecycle.md).

## Validation

[CI run 38127768064](https://github.com/poyrazK/faas/actions/runs/38127768064)
passed both jobs for source `0fbfaf3116f18a0e8d8db7df3d0ae2cbb9fa557a`.
SDK compilation/build and all 15 focused SDK tests passed. The packed-starter suite
passed with 8 nested cases, including the recoverable room and isolated validator.
All four durableentity packages, focused CLI checks and authenticated socket API
tests passed with Go race detection. Local tests/builds and native acceptance were
not run. Preview gates remain off.
