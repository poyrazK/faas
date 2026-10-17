# Atomic internal hosting preview capacity reservations

Date: 2026-10-17

Status: accepted for internal implementation; live qualification deferred.

CI previously discovered a full preview catalog after uploading artifacts. New
explicit `reserve_preview` CI intents reserve capacity before release allocation,
under the account/site locks already used for admission. The request's omitted
false field preserves existing fingerprints. The reservation choice is immutable
and fingerprinted; retries return the same IDs and expiry without allocating.

Stored preview rows plus unconverted reserved runs cannot exceed the existing
preview quota. All manual and CI preview writers enforce this invariant and acquire the account
lock before the site lock, preventing foreign-key key-share deadlocks with CI
release admission. Conversion
excludes only its own run under the same site lock and atomically marks conversion.
Unused holds count only while the original TTL remains and the original release
is pending or ready. Retirement/GC cannot recreate an identity; expiry never
extends. Stored revoked/expired previews count until safe preview garbage collection.

A separate default-off API runtime flag controls new reserved admission and the
operator-only capacity snapshot. CLI, Action and dashboard require explicit opt-ins.
Disabling admission preserves existing holds, exact retries and conversion under
existing automation/preview prerequisites. It never turns off invariant enforcement.
The capacity snapshot is advisory stored evidence, not a promise of admission or
serving health. Existing resource limits remain independently enforced.

Deploy migrations and upgrade ALL API writers before enabling; old writers do not
understand reservations. Do not roll back binaries while reserved identities exist.
Downgrade refuses to discard any reservation marker, even converted/expired ones.
Existing rows backfill false. No live provider changes or external validation are
required for local implementation; live qualification remains a release gate.
