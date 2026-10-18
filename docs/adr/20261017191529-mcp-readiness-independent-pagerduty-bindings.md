# ADR 20261017191529: Independent PagerDuty acknowledgement bindings

## Status

Accepted, 2026-10-17.

## Context

ADR 20261017191528 supports a native PagerDuty primary and fallback but permits only one
inbound acknowledgement binding per readiness rule. Installing fallback sync
replaces primary sync. Operators need acknowledgements from either destination
to pause reminders without sharing signing secrets or rotation schedules.

## Decision

Store at most one binding per `(rule_id, destination)`, with destination checked
as `primary` or `fallback`. Each binding has independent optimistic versions,
write-only encrypted signing credentials, enable/disable state, and captured
outgoing receiver identity. Existing app-before-rule locking serializes changes
and incoming attention decisions. Disabled rows keep their destination and
version while clearing credentials and receiver metadata.

The existing management endpoint accepts `GET ?destination=primary|fallback`;
`PUT destination` selects the binding to change, including disable. Enable without
a destination continues to select primary. GET and disable without a destination
select the binding owning the original callback URL. Updating one destination
never revokes or retargets the other.

Preserve every pre-upgrade rule callback URL by marking the existing row as its
immutable legacy receiver. For newly configured rules the first saved binding
owns that original URL. A partial unique index allows only one owner per rule.
The alias remains reserved even when disabled. Add explicitly scoped callbacks
at `/v1/webhooks/mcp-readiness/pagerduty/{id}/{destination}`; explicit destination
reads always return these stable URLs, including before configuration. Both
callback routes share failed-auth limiting and HMAC validation. Recheck the
verified destination, policy version and legacy ownership under the write locks.

Keep event deduplication at `(rule_id, provider_event_id)`. The destination is an
authenticated routing selector, excluded from the semantic digest, so the same
provider event delivered to both valid subscriptions returns the original receipt
and cannot reapply attention after a local release. Conflicting semantic payloads
remain conflicts. Different provider events retain separate minimal receipts;
only one attention transition occurs while already acknowledged. Retain existing
tenant, application, service, regional endpoint and pinned integration checks.
Fallback must already have activated. Readiness probes continue to own recovery.

## Consequences

An upgrade preserves existing primary or fallback binding versions, encrypted
secrets, receipts and original subscription URLs without a provider change.
Clients managing both destinations should read each explicitly and use its own
version and returned callback URL. Dashboard and CLI expose independent settings.
Existing single-primary clients continue to work. Switching destinations now
leaves the other active; explicitly disable it to revoke that subscription.

Pause acknowledgement writes and incoming subscriptions while applying the
migration and upgrading every apid instance: the old writer uses a single-column
conflict target that the new composite primary key replaces. Resume only after
the upgrade.

Migration 20261017181315175 is replay-safe and retains disabled version tombstones.
Rollback refuses any nonlegacy binding, or a disabled fallback tombstone that the
prior schema cannot represent. It never silently discards a second credential or
CAS history. Downgrading application binaries also removes destination-specific
routes: stop incoming callbacks during rollback and reconfigure their subscriptions
to the original callback URL only after preserving required data.

Automatic provider acknowledgement release, more fallback tiers and live provider
qualification remain separate work.
