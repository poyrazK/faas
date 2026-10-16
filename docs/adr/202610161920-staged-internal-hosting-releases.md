# ADR-202610161920: Staged internal hosting releases

## Status

Accepted for the existing default-off internal hosting pilot.

## Context

CLI/browser deployments currently verify then immediately promote. The API
already separates these operations, but making a release ready also places it
in edge routing inventory, allowing new staged paths into retained asset lookup.
Operators need to inspect files and response rules before changing live content.

## Decision

Separate verification from routing eligibility with nullable `first_promoted_at`.
Backfill existing ready releases to preserve their legacy visibility. Successful
revision-CAS promotion sets the marker atomically with active intent and outbox
updates; readiness alone never sets it. Routing includes active or previously
promoted ready releases. Existing retention, suspension and cleanup semantics
stay in effect. Verification still bumps the existing outbox generation, but
never introduces new staged bytes to the head. No Worker schema change is needed.

Add CLI stage-only deployment, read-only manifest comparison and promotion with
an explicitly supplied reviewed revision. Preserve default combined CLI deploy
for compatibility; default the internal dashboard to staging. Require a new
implementation capability before clients stage against an older API. Comparisons
hold immutable manifests, active baseline and revision; missing baselines fail
closed. Dashboard polls cannot replace a review with newer intent. Retire/conflict
and HTTP errors require refresh/review; no automatic retry adopts a new revision.

Add bounded CLI waiting for the exact accepted durable publication receipt.
Limits live in `pkg/api/limits.go`. Reject unsupported leased waiting and invalid
options before writes. Return accepted intent with structured wait outcomes;
nonzero timeout/supersession/provider failure never reverses or repeats promotion.
All hosting API responses use no-store. Receipts remain distinct from CDN
propagation, serving health, domain readiness and live infrastructure qualification.

## Consequences

A coordinated migration and writer/publisher upgrade is required before staging;
old publishers can expose every verified release, and old writers do not mark
promotion. Downgrade refuses while never-promoted ready releases remain.
Staged data uses normal retained-release quotas and grace-period cleanup.
There is no preview hostname, new runtime process, public entitlement or provider
provisioning. Existing hosting gates remain default off. Detailed operating and
recovery instructions are in the [release workflow guide](../internal-frontend-hosting-releases.md).
