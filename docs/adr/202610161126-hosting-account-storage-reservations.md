# ADR-202610161126 · Account storage reservations for internal hosting

- **Status:** accepted for the default-off internal pilot.
- **Date:** 2026-10-16
- **Amends:** [internal static hosting](20261015-internal-static-hosting.md).

## Decision

Enforce a uniform internal-pilot account cap of 1 GiB reserved artifact storage
and four pending upload reservations, independently of VM plan/billing quotas.
Keep the existing file, manifest, release, site and retained-release limits.
All limits remain in `pkg/api/limits.go`; this is not a customer hosting plan.

At release admission, validate the manifest and persist its total file bytes.
Reserve two file copies (staging and immutable publication) plus the maximum
manifest size, including for pending and retiring releases. This bounds admitted
artifact capacity without trusting a client's claim of upload completion. Quota
errors report the resource-specific code, limit, attempted usage and docs URL.

PostgreSQL locks the account before the site, aggregates persisted release sizes,
and admits the new release in the same transaction. All admission paths serialize
across sites; lifecycle transitions only preserve or reduce reservations.
Memory storage performs the same checks under its existing mutex. Finalization
frees a pending slot while retaining byte capacity. Promotion and rollback select
retained releases without allocating storage. Retirement/site deletion retain byte
reservations until the existing grace, artifact removal and catalog purge succeed.
A failed provider cleanup does not make room for another deployment.

## Observability and limits

A gated operator-only `/v1/internal/hosting/usage` read reports account-wide
catalog totals, ready payload, retiring reservations and pending upload slots.
It performs no provider reads or writes. Counts span every project, environment
and origin owned by that account. The dashboard distinguishes catalog accounting
from a provider bucket inventory and from the separate workload cost calculator.

The byte cap covers admitted release artifacts. Permanent routing/domain heads,
manual/orphan provider objects, object versions and other services are not a
physical bucket ceiling. Pending slots count unfinalized releases, not live TCP
connections, signed-URL replays, daily uploaded bytes or provider operation counts.
Existing upload TTL, signed size bounds, API rate limits and publish concurrency
still apply. No Worker request/CPU billing meter is introduced.

## Migration and estimates

The append-only migration backfills file bytes from existing immutable manifests.
Existing over-quota catalogs are retained and reported; new releases are refused
until there is room. There is no default for the new non-null column, so old
release writers fail rather than silently create unaccounted rows. Deploy every
apid reader and writer before admitting releases; old SQLC `SELECT *` readers
cannot scan the added column. Disable hosting API management during replacement,
while durable sites retain independent Worker serving. Legacy leased sites
follow their existing admission/renewal behavior and require maintenance planning. Downgrade requires draining releases.

The operations console offers an explicitly hypothetical monthly provider usage
calculator. It uses integer USD millicents and public Standard R2/Workers Paid
usage rates, links its sources and rate-check date, and excludes included/shared
allowances, the Workers base subscription, SaaS/Access/domain
fees, taxes and Gregale infrastructure. It includes R2 aggregate billing-unit rounding. It assumes the current reservation is
held for a whole month and takes operator-supplied operations/requests/CPU counts.
It is neither measured traffic, an invoice nor a customer price.

Portable checks must exercise cross-site concurrent admission, account isolation,
pending-slot release, rollback, retirement, failed cleanup and migration backfill.
Provider costs and regional traffic still need a separately authorized live pilot.
