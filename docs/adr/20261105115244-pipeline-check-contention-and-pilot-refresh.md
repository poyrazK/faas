# ADR-20261105115244: Pipeline check contention and pilot refresh

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-05
- **Amends:** ADR-1591

## Context

Connection checks share one catalog row per source. A manual check for a
separate target could replace a running setup check and the setup could replace
it back, repeatedly discarding leased work. Valid UUID spellings in operator
configuration and CLI input could fail literal string comparisons. Poll failures
could also discard the CLI's last known resource, and the independent alert
worker retained an old pilot account scope after configuration changes.

## Decision

Serialize checks on a connection without a schema migration. A pending or
running check with the current credential revision cannot be displaced by a
check for another target. While a setup is working through its source and
mirror checks and pipeline creation, reserve its required check scope under the
account, connection and setup locks. Conflicting external requests receive a
specific retryable conflict; the setup waits for a previously claimed check
instead of becoming blocked. Matching requests still coalesce. Credential
rotation can invalidate old-revision work as before.

Compare operator and CLI UUIDs by canonical identity. Preserve the last known
pipeline or check on a polling error. The alert process reloads its pilot scope
and identity paths on demand, with a bounded shared cache; invalid or disabled
configuration suspends new monitoring and delivery until a successful reload.
An HTTP request already in flight can still finish.

## Validation and rollout

PostgreSQL contention tests, UUID and CLI regressions, alert config refresh and
fail-closed tests cover these paths. No new database migration is required.
The existing staging-only admission and provider qualification gates remain.
