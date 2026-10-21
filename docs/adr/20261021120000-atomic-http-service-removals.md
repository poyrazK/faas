# ADR-20261021120000: Atomic HTTP service removals

## Status

Implemented behind `FAAS_ATOMIC_SERVICE_REMOVALS_ENABLED=1`. Admission defaults
off pending dedicated native qualification. Amends ADR-20261019120043 (atomic
HTTP service additions), which left removal to later work.

## Context

An atomic release keeps the exact serving membership. A project that stops
declaring a service had no atomic way to drop it: ordinary apply soft-deletes
it immediately, outside the release graph and without rollback. Environment
ingress already resolves project services only through the active release
graph, so a service missing from the graph fails closed.

## Decision

`gregale deploy --project --atomic --remove-services` (header
`Project-Remove-Services: required`) retires serving services the source no
longer declares, in the same cutover as the remaining services' new code.

- Only HTTP services can be retired. A retired service must have no enabled
  schedules, no enabled queue bindings and no custom domains, which would reach
  it outside the graph. Nothing remaining may depend on it: dependency lists of
  remaining services cannot change in an atomic release.
- Removals cannot be combined with additions, configuration, secrets,
  migrations, workers or progressive rollout, nor with pinned release
  configuration. `--verify` probes only the remaining services and needs at
  least one probe on them.
- The plan binds each retired app's ID and settings fingerprint. Staging records
  the retirement against the durable attempt in the service addition table while
  the service still serves; changed settings at staging or publication conflict.
- Publication commits the reduced graph and turns the records dormant in one
  transaction. A dormant retirement behaves like a dormant addition: ordinary
  publishers may omit it but not include it, ordinary deployments to it are
  refused, and the active graph cannot be deactivated while it exists.
- A verification regression restores the exact original graph and returns the
  retired services to serving in the same transaction. The previous graph stays
  retained for its normal window.
- A retired app keeps its ID and counts against the app quota until it is
  deleted through the app lifecycle; it can return through `--add-services`.

## Consequences

Projects can shrink atomically with rollback. Retirement is explicit and never
deletes anything; deletion stays a separate, deliberate step.

## Validation and rollout

Memory and PostgreSQL contracts cover retirement, rollback, settings drift and
readiness gating; planner tests cover eligibility. Before enabling admission on
dedicated x86_64 KVM hardware, retire a service of a running project and confirm
its environment ingress fails closed at cutover while remaining services switch
together; cause a verified regression and confirm it serves again; restart apid
during staging, build and rollback and resume each receipt.
