# ADR-581: Accounting ownership through uncertain provider creation

- **Status:** Accepted
- **Date:** 2026-10-04
- **Decision:** Persist an irreversible accounting obligation before provisioning
  or restoring a resource. Recover an unknown provider identity with a read-only
  lookup and persist it under the lifecycle lease before deletion. Absence now
  does not prove that an earlier creation incurred no usage or cannot appear later.
- **Why:** A fault-injection regression creates an upstream resource and loses
  the response. The catalog retained no provider ID. Deletion rediscovered and
  destroyed it, reset its attempt counter, and left collection and admission
  reporting fresh zero usage. ADR-569 covered known IDs, so it could not settle
  this path.
- **Consequences:** `accounting_required` survives errors, retries, readiness,
  and tombstones. The database defaults it to true and conservatively backfills
  all historical rows because reset counters cannot prove that no provider call
  occurred. The new sqlc reservation explicitly records false, and the active
  provisioning lease commits true before any provider mutation. The database
  rejects clearing true. Downgrade is refused while an obligation has no identity;
  known IDs remain accountable under the previous ADR-569 implementation.
- **Rejected alternatives:** Inferring zero from an absent lookup loses both
  historical usage and late accepted creation. Returning identity only after
  deletion still loses it on a crash. Retry counters do not retain accounting
  ownership. Running all discovery during catalog preparation can exhaust quota
  before the fleet receives its recovery turns.

`ResourceDiscoverer` is a provider-neutral read-only capability. Neon resolves
stable organization-scoped project names or restore names in the recorded source
project, rejecting ambiguous matches. Providers without discovery defer unknown
resources. Customer lifecycle deletion always supplies a persisted opaque ID;
logical-name cleanup remains available for disposable adapter qualification.

The collector includes unknown obligations and, after active leases expire,
recovers nonterminal identities with owner/backend/fingerprint and immutable-ID
fences. Discovery occupies one turn in fleet recovery and the shared ceiling of
24 provider requests per database per sweep. Successful discovery is followed by
ordinary contiguous recovery and bounded correction replay. Restore descendants
retain shared-root accounting and never add duplicate quantities.

Snapshots mark unresolved identities explicitly, so even recorded observations
cannot make an unknown obligation fresh. New uncertain deletion stays pending
until identity is known and provider deletion is confirmed. A new reservation
that never began provider I/O can retire without a provider call.

Legacy unknown tombstones remain stale and are not rediscovered automatically:
their logical deletion timestamps do not prove identity-based provider shutdown.
They require explicit operator reconciliation. Missing or expired history, final
provider invoice settlement, and corrections beyond the bounded replay horizon
also remain reconciliation work. This decision does not qualify live Neon history
or credentials. Keep provisioning disabled during migration and control-plane
rollout so older admission readers cannot ignore the new obligations.

Validation covers memory and PostgreSQL: lost creation response, restart,
persist-before-delete, failed writes, expired leases, delayed discovery, unknown
legacy tombstones, ownership fences, bounded fleet scheduling, and shared restore
roots. HTTP fixtures verify read-only Neon lookup, namespace/parent selection,
and ambiguous or absent results. The migration test exercises populated upgrade,
legacy-writer defaults, irreversible flags, guarded downgrade, and replay.
