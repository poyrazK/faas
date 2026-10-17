# ADR-1480: Explicit inbound tracker lifecycle application

Date: 2026-10-15

Status: Accepted

## Context

ADR-1479 introduced inbound assessment without applying changes. Reading a local
lifecycle twice cannot detect edits away and back, and a provider resolution does
not supply the deployment evidence required by Gregale. Inbound writes must not
race an automatic outbound dispatch or overwrite a manual triage action.

## Decision

Add explicit pull controls to the owner's dashboard tracker preview. Preserve
session authorization, named Issues CSRF, MFA, deploy-write scope, app ownership,
Issues entitlement and deployment eligibility. Require the reviewed policy
revision, provider status and a durable app intent revision. Reread the provider
using the existing native connection and group checks immediately before applying.
A resolution requires an explicitly selected deployment; show a bounded list of
recent deployments scoped to this app and issue environment, and enforce the
existing deployment scope again in the transaction. A reopening needs no deployment.
A provider closure alone never supplies fixing evidence.

Use a new app-scoped `issue_tracker_inbound_guards` row as a monotonic revision.
Database triggers advance it for issue intent changes (including assignment and
regression), new groups, grouping links/rules, tracker references, policy revisions,
baselines, pending intents and worker claims, app ownership/deletion/GitHub binding,
Linear connection replacement or blocked OAuth status, and GitHub installation
identity changes. Changes away and back still increment the revision. Ordinary
occurrence counters, scheduler timestamps, installation token rotation and normal
Linear OAuth refresh do not advance it. This deliberately invalidates previews
for unrelated issue intent edits within the same app; conservative conflicts are
preferable to accepting a stale snapshot. Trigger writes roll back with their
originating transactions. Rollback refuses to drop a populated guard table so
intent revisions cannot be reused by old browser forms after reinstallation.
No traffic counters or retention sweep supply authority.

Capture the fence before loading policy/provider evidence and verify it after
observation. Apply under the app row lock, policy row lock, then intent fence lock.
The policy-before-fence order matches worker claim transactions. Recheck owner,
canonical source/reference membership, policy/baseline, source lifecycle, enabled
state, pending intent and live worker lease. A future or uncertain outbound write
blocks a pull. Require the exact mapped Linear workflow. Apply the ordinary
resolve/reopen action with tracker metadata in its durable issue activity, advance
the provider baseline, increment policy revision and disable that policy in the
same transaction. This pauses outbound sync; re-enabling requires explicit aligned
review. Duplicate or stale submissions fail without repeating the action.

Callbacks remain hints and cannot execute this explicit owner flow. This adds no
public Bearer endpoint or SDK contract: the existing session form dispatches the
operation. The provider and local database cannot share a transaction; a provider
edit after the final read may require another review. External visibility and
provider credentials are checked through the native read safeguards, without
holding database locks during provider calls.

## Consequences

Owners can pull reviewed GitHub/Linear reopenings and resolutions with durable
conflict detection and audit history. Missing deployment scope, stale evidence,
provider changes, ignored states, unmapped workflows and pending writes fail closed.
Failures before commit leave lifecycle, baseline, policy and audit unchanged.

Automatic inbound sync is still a separate opt-in feature. It needs an explicit
per-link direction policy, deployment-evidence selection, durable observations,
worker recovery and conflict controls. The app intent fence and atomic local
application are reusable prerequisites. Existing CLI pull continues to use the
ordinary action endpoint and does not claim this dashboard transaction guarantee.
