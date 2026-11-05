# ADR-20261104212930: MCP live worker receipts and schema snapshots

## Status

Accepted; extends ADR-20261104205633.

## Context

Native releases relied on a single worker startup event. Healthy workers outlive
the platform's bounded live log ring, so normal log eviction could block later
handoffs. Preflight also required worker receipts from previous pools already
scaled to zero. Node catalogs copied plain metadata, but Zod schemas retained
mutable nested `.meta()` references despite their immutable structural APIs.

## Decision

1. Dedicated Node workers publish the same immutable process identity, claim-fence
   capability and effective shutdown budget at startup and every ten seconds.
   Stop the refresh timer before draining. Native identity and budget checks first
   use retained receipts, then follow a server-relative thirty-second live window
   with a thirty-second deadline if receipts are incomplete. Close the stream as
   soon as every current running replica is covered. Keep byte bounds and reject
   invalid or conflicting receipts, including when another replica is missing.
   Existing workers remain usable while their startup receipt is retained; older
   sources need the refresh implementation to recover after log eviction.
2. Previous pools with no current instances need no receipts during preflight.
   Record their deployment and retain generation checks and conditional parking.
   Re-read replicas before promotion and each park; a pool that has started again
   must satisfy the normal identity and stop-grace checks.
3. Catalog registration and replacement adapt Standard Schema validators to
   immutable wrappers with detached input/output JSON schema snapshots. Preserve
   validation, asynchronous transforms and SDK prompt completion callbacks.
   Return independent JSON metadata to readers. Input/argument contracts use the
   input representation and output contracts use the output representation,
   matching SDK discovery. A conversion unsupported in one direction retains
   that error without disabling the other direction. Explicit replacement is
   required to publish changed schema metadata and increment the revision.

These changes add no platform schema or quota. Worker receipts retain their
existing event name and fields. Standard Schema wrappers replace the earlier
decision to retain SDK schema objects themselves; callers should use validation
and catalog replacement APIs rather than depend on a Zod prototype in readers.
Review and refresh catalog pins when upgrading schemas whose input and output
representations differ.

## Validation

Native tests exercise an actual default-size log ring before and after eviction,
live stream completion/closure, incomplete and unrelated replicas, invalid and
conflicting receipts, cancellation, empty previous-pool preflight, and a zero pool
starting again with insufficient stop grace before parking. Node tests exercise
receipt refresh and timer cleanup, nested Zod metadata and registry mutations,
independent reader copies, asynchronous validation/transforms, completion
callbacks, stable pins and revisions, and explicit replacement notifications.
The disposable native-qualification fixture uses the current handler registration
API and keeps the starter's current protocol handlers and refreshed receipts.
