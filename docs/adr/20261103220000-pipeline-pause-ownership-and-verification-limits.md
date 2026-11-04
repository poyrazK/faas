# ADR-20261103220000: Pipeline pause, subscription ownership and verification limits

- **Status:** implemented for local/staging qualification; production disabled
- **Date:** 2026-11-03
- **Amends:** ADR-1591

## Context

Resync can advance intent before an older native stream is retired. Disabling
only the newest subscription and returning early when source login fails could
report a paused pipeline while an older generation keeps applying changes.
Current-generation cleanup also lacked the subscription ownership checks already
used when recovering retired generations.

Discovery accepted unlogged tables that PostgreSQL cannot publish. Pipeline
names beginning with a digit could also generate invalid managed database names,
leaving otherwise valid saved setups unable to provision their mirror.

PostgreSQL numeric index keys can be small on disk but expand to megabytes as
text. A valid row below the 16 MiB verification row limit could produce a cursor
larger than the private checkpoint's 4 MiB bound. Repeated checkpoint failures
then prevented verification from making durable progress.

## Decision

Validate subscription ownership, slot identity and exact publication list before
disabling, reconnecting, enabling or deleting it. Explicitly conflicting markers
always block changes. A lost CREATE reply with no comment can be recovered using
the matching private mirror receipt or an ownership-checked source publication.

When source login fails, enumerate target subscriptions belonging to the pipeline
across its unfinished generations and validate the complete set before disabling
any of them. Exact markers or the private receipt establish ownership without a
source connection. Keep the existing destination advisory lock and catalog lease
and revision fence. A paused observation requires successful suspension. Deletion
can suspend streams while retaining pending publisher cleanup. With source access
available, preserve publication ownership checks before retiring generations.

Require permanent logged tables in the shared source inspector. Discovery and
publication-time reinspection return the same actionable compatibility blocker.

Prefix generated mirror names with p- for digit-led pipeline names. Existing
valid generated names remain stable. The setup projection recognizes only the
exact older invalid derivation for an auto-provisioned mirror and normalizes it
on read. Such a name could never provision a database, so retries safely retain
the reserved database ID and immutable customer request without a data migration.

Limit each serialized primary-key tuple to 1 MiB in pkg/api/limits.go, independently
of the existing row limit. Detect excess before retaining a cursor or writing
range bounds or approved backfill values. Save a small terminal limited result,
clear its cursor, and retire helpers through the existing verification cleanup.
Manual verification reports inconclusive with verification_key_limit; schema
workflows keep reads gated. This preserves the 4 MiB checkpoint bound and avoids
claiming equality when verification cannot retain its proof. Explicit retry is
available after resolving the key size; changing the key definition needs resync.

## Validation and scope

Native TLS/password tests revoke source login after an interrupted resync, then
verify pause and deletion stop old streams and recover after credential rotation.
They also reject foreign current-generation markers without changing subscriptions
or receipts and verify large-key checks terminate inconclusively with helpers
removed. PostgreSQL tests cover unlogged tables, ordinary and extreme numeric
keys, valid digit-led and maximum-length names, and legacy setup retry with the
same database ID. The full native acceptance suite, race detector and repository
checks remain required. Production admission and provider qualification are unchanged.
