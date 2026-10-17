# ADR-1655: Internal CI execution profiles

- Status: implemented behind existing default-off CI gates
- Date: 2026-10-16
- Extends: ADR-1654

## Decision

Save account-owned execution profiles so explicit CI submissions can reuse a
validated binding, immutable pipeline revision and Jobs template mapping. This
is configuration and explicit admission only; GitHub events continue to produce
blocked receipts. No automatic admission, Check publication changes, native
scheduling changes or production flag enablement are introduced.

A profile has a permanent UUID and account-unique name. Its configuration is
immutable: identical PUTs return the same profile, while different inputs or a
changed binding generation return a conflict. Changes require another name.
DELETE permanently disables the profile; repeated PUT cannot re-enable it.
Disabling prevents new admissions through that profile and does not cancel or
alter existing executions, whose full source/image authority is already frozen.
Existing explicit execution cancellation remains available.

Creation requires internal/GitHub opt-in, the account allowlist, live repository
verification, an active Jobs-capable plan, and exactly one account-owned,
materialized template for each planned job. It uses the existing Jobs template
validation for pinned images, runner command, memory, timeouts and environment.
The private snapshot freezes the binding generation and image provenance.
Public responses contain template UUIDs and the revision digest, without image
storage keys, credentials or command bodies. No provider bytes are fetched and
no execution or Jobs run is created while saving a profile.

Profiles are PostgreSQL-backed, account-serialized and bounded to 64 retained
rows/account, including disabled rows. The private snapshot is capped at 64 KiB.
Limits live in `pkg/api/limits.go`. Identical saves work at the quota ceiling.
The append-only migration and SQLC queries own persistence; there is no pruning
or name reuse contract in this internal release.

## Explicit admission and recovery

The existing execution endpoint accepts either `profile_id` or the existing
revision/binding/template fields, plus an exact commit SHA and idempotency key.
Combining the two configurations is rejected. A profile adds no execution gate:
worker, dispatch, transport, managed storage, HTTPS and fresh source verification
remain mandatory. The source verifier runs before the admission transaction.
Under the account lock, admission rechecks profile state, binding generation and
the current binding snapshot. Before inserting new intent it revalidates every
template and compares its materialized image identity with the saved snapshot.
Profile disable and binding edits therefore cannot race past admission. A
changed template cannot silently retarget a profile. Existing request replays
return their original execution and never create another attempt.

The execution stores its profile UUID as part of its immutable request identity.
A manual request cannot reuse a profile request key, nor can another profile
reuse it. Once admitted, normal engine/Jobs recovery uses the execution's own
authority and does not depend on profile availability. Dropping the profile
migration removes saved configuration but does not remove execution recovery
state; roll back profile API readers before dropping it.

All profile routes use authenticated admin scope, MFA, internal account policy
and the configured HTTPS origin with no-store responses. Creation additionally
requires GitHub opt-in. Read/list/disable remain available after new GitHub or
compute admission is disabled. Profile setup needs no worker, storage bucket or
native access, although the HTTPS origin must remain configured.

## Portable verification

PostgreSQL tests cover immutable saves, quota contention, account isolation,
restart-safe execution identity, disabled profiles, binding-generation changes,
image replacement, explicit override rejection and disable during source
verification. API/CLI tests cover setup with compute disabled, private-field
redaction, admission gates and recovery after disable. The authenticated HTTPS
service test submits through a saved profile, disables it, then checks that the
existing worker still dispatches, recovers and cancels the original execution.
These tests execute no repository steps and need no native host or real GitHub
publication. The prepared ADR-1654 native qualification candidate stays frozen;
this implementation is a subsequent source revision.
