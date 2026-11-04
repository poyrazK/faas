# ADR-20261103190000: GitOps scoped ownership and manual handoff

- **Status:** accepted
- **Date:** 2026-11-03
- **Related:** ADR-568, ADR-20261103180001

## Context

Traffic and canary guards treated any GitOps deployment in an app as authority
over every environment. Separately, immutable frozen deployment inputs kept
blocking ordinary deployment promotion after the source was disconnected.
Removing those inputs would discard reviewed provenance and weaken execution
and artifact protections.

## Decision

1. Scope rollout ownership to the app and deployment environment. A bound
   source retains ownership in report mode and while suspended. Only explicit
   detach releases it; missing or malformed authority remains blocked.
2. Keep frozen deployment inputs, holds, artifacts and qualification evidence.
   A detached candidate cannot be activated through ordinary promotion, status,
   traffic or rollback APIs. Fresh manual deployments can take over the scope.
3. Permit release membership replacement and deactivation once the owning source
   is detached. Preserve active release selection, optimistic concurrency,
   checked binding policies and revision retention. Disconnect itself neither
   changes traffic nor publishes a replacement release.
4. Enforce the same ownership rule in memory, PostgreSQL owner APIs and the
   database promotion guard. Retain all preparation and activation checks on
   frozen deployments. A detached live deployment may be superseded normally
   when it no longer needs release or revision retention.

## Validation

Exercise manual staging traffic and canary advancement before and after a
production candidate exists in both stores. Verify report mode still blocks
ordinary promotion, detach permits a fresh manual deployment, and the old held
candidate remains protected. Qualify and activate a complete release through
owner APIs, detach its source, and replace the active release with manual
deployments. Database guard tests cover direct SQL promotion and immutable
history. These tests do not replace dedicated native acceptance.
