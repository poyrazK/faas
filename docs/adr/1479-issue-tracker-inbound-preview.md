# ADR-1479: Read-only inbound tracker lifecycle assessment

Date: 2026-10-15

Status: Accepted

## Context

Tracker callbacks are durable hints and outbound sync deliberately pauses when a
provider changes. Inbound sync needs to distinguish a provider-only edit from a
concurrent Gregale edit and an uncertain outbound write. A provider closure does
not identify the deployment that fixed a Gregale issue. Existing CLI pulls use
ordinary issue actions after rereading evidence; that path does not provide an
atomic snapshot comparison for unattended inbound updates.

## Decision

Add a read-only inbound assessment to the owner's explicit tracker preview for
GitHub and Linear. Use the existing native fresh provider reads and freeze the
source lifecycle, group and connection throughout observation, including Linear
workflow catalog discovery. Display the source state from that same observation
rather than a separate later detail read.

Compare against the stored delegated baseline, scoped to app, owner, canonical
source issue, reference, group fingerprint and connection identity/revision.
Block assessments with no matching baseline, disabled policies, outstanding
outbound intents, unsupported states, foreign provider identities and other
paused conflicts. A provider_changed conflict can be explained without resuming
the policy. Local lifecycle divergence from the recorded source state is a
conflict even if both sides now have the same lifecycle. Linear changes must use
the policy's selected workflow mapping.

A provider reopening with an unchanged local baseline is an inbound candidate.
A provider resolution requires an explicitly selected fixing deployment scoped
to this app and environment. The assessment never supplies deployment evidence,
changes lifecycle, advances a baseline, clears a pending intent, or enables sync.
Normal issue page GETs remain provider-free; owner, CSRF, MFA and entitlement
checks on the explicit preview remain in force.

## Consequences

Owners can inspect inbound conflicts and deployment requirements today. Automatic
inbound sync remains unavailable. Before adding apply or opt-in worker execution,
persist an explicit direction policy and compare source revision and grouping,
connection, current policy and fixing-deployment scope in the same transaction
as the local action and baseline advancement. A lifecycle string alone cannot
detect edits that change away and back between observations. Recheck provider
evidence before that transaction and retain audit evidence and recovery behavior.
Read-only classifications are advisory and cannot serve as a mutation token.
