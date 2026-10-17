# ADR-1475: Named Linear workflow picker

Date: 2026-10-14

Status: Accepted

## Context

The dashboard tracker review requires owners to enter Linear workflow UUIDs.
Owners need named choices scoped to their connected team, while delegation must
continue using verified state identities rather than mutable display names.

## Decision

Discover the connected team's workflows during the existing explicit owner
preview. Read fixed-origin GraphQL pages and require the team to be active with
matching IDs on every node. Reject incomplete archive/pagination evidence,
duplicate identities, foreign teams, repeated cursors and partial GraphQL errors.
Omit archived, canceled and unfamiliar workflow types. Return no partial catalog
when discovery fails or reaches its bound.

Centralize limits: at most 200 scanned states over four pages of 50, names up to
256 bytes and cursors up to 512 bytes. Names must be valid UTF-8, nonempty and
contain no control characters. Provider responses retain the existing byte and
request-time bounds. Sort verified choices by lifecycle, name and ID.

Pin the native Linear connection and issue group throughout catalog discovery and
the linked-issue read. Render separate required open/resolved selectors, plus a
selector for the current Gregale lifecycle during manual alignment. Start with a
blank choice unless an existing policy destination is still active and eligible.
Hide automatic enable when either lifecycle has no eligible destination.

Display the current names associated with stored policy UUIDs in the owner review.
Missing destinations prompt an active replacement. Names are fetched during the
review and are not persisted or trusted as mutation authority. Renaming a state
does not change its identity. Existing enable/push APIs verify selected UUIDs,
team, archive status and lifecycle again before accepting delegation or writing.

Normal Issues page loads and offline disable do not discover workflows. No new
public API route, stored policy field or SDK contract is introduced; CLI workflow
selection continues using explicit UUIDs.

## Limits

Catalog pagination and issue reads are observations, not a provider transaction.
The final operation revalidates selection and native scope; a competing provider
writer can still race its final check. Very large catalogs require reducing the
team's state set or using explicit CLI UUID selection. Live Linear credentials
are not part of the local acceptance setup.

## Validation

Mocked provider tests cover scoped read-only pagination, filtering, malformed or
foreign evidence, duplicates, name bounds and bounded cursor/page handling.
Domain race tests reject connection/group changes across catalog and native
reads. Dashboard tests cover named selections, escaped labels, current names,
missing choices and unavailable stored destinations. PostgreSQL dashboard tests
cover discovery only on explicit preview and revalidation after workflow removal,
with existing native status/creation/delegation regression checks retained.
