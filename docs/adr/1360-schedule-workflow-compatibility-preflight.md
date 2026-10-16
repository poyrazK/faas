# ADR-1360: Schedule workflow compatibility preflight

Status: Accepted

## Context

The management MCP catalog is static and performs no native API requests during
construction or discovery. Schedule activity recovery requires a newer native
API than some installations provide. A missing endpoint alone cannot distinguish
an older replica from a missing target, and a capability flag cannot establish
authorization to read a selected environment.

## Decision

Add optional `environment_cache_schedules` signals to authenticated
`GET /v1/capabilities`: bounded listing, contiguous activity, checkpoint reads,
and retention seconds. The serving apid derives these signals from its wired
state backend interfaces. Omission means unknown; explicit false or zero means
the corresponding feature is unavailable on that backend. These are separate
from plan entitlements and do not attest to migrations, maintenance health,
other replicas, or fleet writer upgrades.

Extend `gregale_preflight` for a consented named environment. Read capabilities
explicitly and, for advertised features, validate one listing page, a checkpoint,
and one activity page after that checkpoint. Pages use limit one and the same
scope, identity and cursor validation as normal tools. Do not scan an existing
backlog, create plans, or mutate intent. Report passed, unsupported, unknown,
and blocked independently; denied reads remain blocked, while 404/405/501 stay
ambiguous. Recovery passes only if every prerequisite passes. Include static,
customer-safe upgrade guidance; never echo upstream diagnostics in preflight.

Guided recovery checks the advertised prerequisites on every call, including
continuations. Missing or incompatible signals require a backend upgrade;
unavailable capability endpoints prevent verification. Permission and transient
errors remain errors. Never fall back to draining activity, skipping retention,
or replacing the original checkpoint. Native reads still validate responses and
authorization even after positive capability signals, including when another
replica handles the next request. Capability results are never cached as grants.

## Consequences

No startup I/O, dynamic tool catalog, new tool, new endpoint, schema migration,
or write permission is introduced. Existing clients can ignore the additive
response field. New recovery helpers require updated apid readers that advertise
the contract; deploy them before the helper. Apply the earlier schedule/feed/
retention migrations and upgrade all writers before relying on activity
completeness. A passing preflight is sequential evidence, not a health lease,
atomic snapshot, admission approval, or native execution qualification.
